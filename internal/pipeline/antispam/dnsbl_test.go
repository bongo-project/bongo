package antispam

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/dnsbl"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// fakeDNSBLResolver is a minimal dnsbl.Resolver double, used so every test in
// this file runs with zero network access. It mirrors the shape of
// internal/dnsbl/dnsbl_test.go's own fakeResolver (map-driven, atomic call
// counter) for the same reason: dnsbl.Checker.Check fans out one lookup
// goroutine per configured zone, so this fake is itself invoked
// concurrently, and a plain int++ counter would be a guaranteed -race
// failure.
//
// Matching is by zone suffix rather than replicating dnsbl's internal
// reversed-octet query-name encoding (queryName is unexported and this test
// lives in a different package): any query name ending in "."+zone is
// treated as a query against that zone, which is all these tests need to
// know.
type fakeDNSBLResolver struct {
	listed map[string]bool // zone name -> answer "listed" (127.0.0.2) when true
	calls  atomic.Int64
}

func newFakeDNSBLResolver(listedZones ...string) *fakeDNSBLResolver {
	listed := make(map[string]bool, len(listedZones))
	for _, z := range listedZones {
		listed[z] = true
	}
	return &fakeDNSBLResolver{listed: listed}
}

func (f *fakeDNSBLResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	f.calls.Add(1)
	for zone, listed := range f.listed {
		if listed && strings.HasSuffix(host, "."+zone) {
			return []net.IP{net.ParseIP("127.0.0.2")}, nil
		}
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// newDNSBLProcessor builds a BuiltinProcessor wired with WithDNSBL against a
// fake resolver, so tests never touch the network. weights maps zone name to
// score contribution, matching what buildAntispamProcessor's dnsblOption
// derives from cfg.Antispam.DNSBLZones() in production.
func newDNSBLProcessor(t *testing.T, resolver dnsbl.Resolver, zones []string, weights map[string]float64, opts ...Option) *BuiltinProcessor {
	t.Helper()
	checker := dnsbl.NewCheckerWithResolver(resolver, zones, time.Second, time.Minute, nil)
	allOpts := append([]Option{WithDNSBL(checker, weights), WithThreshold(0.5), WithHighThreshold(0.9)}, opts...)
	return NewBuiltinProcessor(antispam.NewScorer(), allOpts...)
}

// cleanMessage is a low-scoring message: well-formed headers, benign
// content, so the heuristic-only score sits comfortably below the 0.5
// default threshold and DNSBL score deltas are easy to isolate.
const cleanMessage = "From: sender@example.com\r\n" +
	"To: recipient@example.com\r\n" +
	"Subject: Weekly update\r\n" +
	"Message-ID: <clean@example.com>\r\n" +
	"Date: Thu, 01 Jan 2026 00:00:00 +0000\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Hi, just a routine note. Nothing to see here."

func cleanEnv(sourceIP string, trusted bool) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
		SourceIp:   sourceIP,
		Trusted:    trusted,
	}
}

const dnsblEpsilon = 1e-5

func dnsblApproxEqual(a, b float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < dnsblEpsilon
}

// TestApplyDNSBL_ListingAddsScoreAndReason is the FR-6/FR-7 core case: a
// listed zone raises the score by exactly its configured weight and adds a
// reason naming the zone.
func TestApplyDNSBL_ListingAddsScoreAndReason(t *testing.T) {
	const zone = "zen.example-bl.test"
	const weight = 0.3

	baseline := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
	baseResult, err := baseline.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}

	resolver := newFakeDNSBLResolver(zone)
	proc := newDNSBLProcessor(t, resolver, []string{zone}, map[string]float64{zone: weight})

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if resolver.calls.Load() == 0 {
		t.Fatal("expected the resolver to be queried for a public, untrusted source IP")
	}

	wantScore := baseResult.Score + weight
	if !dnsblApproxEqual(result.Score, wantScore) {
		t.Errorf("Score = %.6f, want %.6f (baseline %.6f + weight %.2f)", result.Score, wantScore, baseResult.Score, weight)
	}

	found := false
	for _, r := range result.Reasons {
		if r == "dnsbl_"+zone {
			found = true
		}
	}
	if !found {
		t.Errorf("Reasons = %v, want to contain %q", result.Reasons, "dnsbl_"+zone)
	}
}

// TestApplyDNSBL_NotListedNoChange covers the negative FR-6 case: an IP
// listed in no zone leaves the score unchanged and adds no reason, even
// though the lookup did happen (proving this is "queried and clean", not
// "skipped").
func TestApplyDNSBL_NotListedNoChange(t *testing.T) {
	const zone = "zen.example-bl.test"

	baseline := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
	baseResult, err := baseline.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}

	// No listed zones configured on the fake -- every query answers NXDOMAIN.
	resolver := newFakeDNSBLResolver()
	proc := newDNSBLProcessor(t, resolver, []string{zone}, map[string]float64{zone: 0.5})

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if resolver.calls.Load() == 0 {
		t.Fatal("expected the resolver to be queried")
	}
	if !dnsblApproxEqual(result.Score, baseResult.Score) {
		t.Errorf("Score = %.6f, want unchanged from baseline %.6f", result.Score, baseResult.Score)
	}
	for _, r := range result.Reasons {
		if strings.HasPrefix(r, "dnsbl_") {
			t.Errorf("Reasons = %v, want no dnsbl_ reason for a clean IP", result.Reasons)
		}
	}
}

// TestApplyDNSBL_MultipleZonesSumBothWeights is the FR-6 two-zone case: two
// zones listing the same IP contribute both weights and both reasons.
func TestApplyDNSBL_MultipleZonesSumBothWeights(t *testing.T) {
	const zoneA = "zen.example-bl.test"
	const zoneB = "sbl.example-bl.test"
	const weightA = 0.2
	const weightB = 0.15

	baseline := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
	baseResult, err := baseline.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}

	resolver := newFakeDNSBLResolver(zoneA, zoneB)
	proc := newDNSBLProcessor(t, resolver, []string{zoneA, zoneB}, map[string]float64{zoneA: weightA, zoneB: weightB})

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	wantScore := baseResult.Score + weightA + weightB
	if !dnsblApproxEqual(result.Score, wantScore) {
		t.Errorf("Score = %.6f, want %.6f", result.Score, wantScore)
	}

	wantReasons := map[string]bool{"dnsbl_" + zoneA: false, "dnsbl_" + zoneB: false}
	for _, r := range result.Reasons {
		if _, ok := wantReasons[r]; ok {
			wantReasons[r] = true
		}
	}
	for reason, seen := range wantReasons {
		if !seen {
			t.Errorf("Reasons = %v, missing %q", result.Reasons, reason)
		}
	}
}

// TestApplyDNSBL_ClampToOne pins FR-8: a weight sum that would exceed 1.0
// clamps at 1.0 rather than overflowing the score/threshold contract.
func TestApplyDNSBL_ClampToOne(t *testing.T) {
	const zoneA = "zen.example-bl.test"
	const zoneB = "sbl.example-bl.test"

	resolver := newFakeDNSBLResolver(zoneA, zoneB)
	proc := newDNSBLProcessor(t, resolver, []string{zoneA, zoneB}, map[string]float64{zoneA: 0.9, zoneB: 0.9})

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if result.Score != 1.0 {
		t.Errorf("Score = %.6f, want exactly 1.0 (clamped)", result.Score)
	}
}

// TestApplyDNSBL_HighWeightQuarantinesButNeverRejects pins FR-9 / US-004's
// "never rejects at SMTP time" requirement. A maximum-weight listing crosses
// the high (quarantine) threshold, but the returned action must still come
// from the ordinary "pass"/"tag"/"quarantine" set the pipeline stage
// produces -- "reject" is a DMARC-only action (see applyDMARC) and DNSBL
// must never introduce it. This holds by construction (this stage runs on
// the delivery path, not at SMTP time), but is pinned here so a future
// change to applyDNSBL cannot silently add a reject path.
func TestApplyDNSBL_HighWeightQuarantinesButNeverRejects(t *testing.T) {
	const zone = "zen.example-bl.test"

	resolver := newFakeDNSBLResolver(zone)
	proc := newDNSBLProcessor(t, resolver, []string{zone}, map[string]float64{zone: 1.0})

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// The maximum possible weight lands on "quarantine" and stops there.
	// This single assertion IS the never-rejects pin: if DNSBL could ever
	// produce a reject, weight=1.0 is the input that would do it. A separate
	// `Action == "reject"` check after this one would be unreachable, and
	// would imply to a future reader that reject is a state this stage can
	// reach at all.
	if result.Action != "quarantine" {
		t.Errorf("Action = %q, want %q (weight=1.0 must cross the high threshold and go no further)", result.Action, "quarantine")
	}
}

// TestApplyDNSBL_AfterBayesBlend_NotDiluted is the FR-7 placement test: the
// same zone weight must move the score by the same amount whether or not a
// Bayes blend ran, proving the DNSBL contribution is added AFTER the blend
// (score + w) rather than being folded into it (0.6*score + 0.4*bayes + w,
// or worse, blended at the 0.6 factor -- which would shrink a configured
// weight of 0.4 down to 0.24). Comparisons use an epsilon: computing
// "blend then add" vs "add directly" involves different floating-point
// rounding even when the underlying arithmetic is equivalent, so an exact
// equality assertion would be flaky on entirely correct code.
func TestApplyDNSBL_AfterBayesBlend_NotDiluted(t *testing.T) {
	const zone = "zen.example-bl.test"
	// Deliberately small: the bayes-blended baseline below lands around
	// 0.61 (0.6*heuristic + 0.4*bayes, with the heuristic side already
	// elevated by missing Message-ID/Date headers). A weight anywhere near
	// 0.4 would push that baseline past the 1.0 clamp and the clamp itself
	// -- not dilution -- would eat part of the delta, making this test
	// fail on entirely correct code. 0.1 keeps every case well inside
	// [0, 1] so the only thing that can shrink the delta is dilution.
	const weight = 0.1
	const userID = "user@example.test"
	const subject = "Quarterly figures"
	const body = "Please review the attached quarterly figures before Friday."

	msg := "From: reports@example.test\r\n" +
		"To: " + userID + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" + body

	newEnv := func(sourceIP string) *bongov1.QueueEnvelope {
		return &bongov1.QueueEnvelope{
			Sender:     "reports@example.test",
			Recipients: []string{userID},
			SpfResult:  "pass",
			SourceIp:   sourceIP,
		}
	}

	// -- Without Bayes --
	plainBaseline := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.99), WithHighThreshold(0.999))
	plainBase, err := plainBaseline.Process(context.Background(), newEnv("203.0.113.5"), strings.NewReader(msg))
	if err != nil {
		t.Fatalf("plain baseline Process: %v", err)
	}

	plainResolver := newFakeDNSBLResolver(zone)
	plainProc := newDNSBLProcessor(t, plainResolver, []string{zone}, map[string]float64{zone: weight}, WithThreshold(0.99), WithHighThreshold(0.999))
	plainWith, err := plainProc.Process(context.Background(), newEnv("203.0.113.5"), strings.NewReader(msg))
	if err != nil {
		t.Fatalf("plain+dnsbl Process: %v", err)
	}
	plainDelta := plainWith.Score - plainBase.Score

	// -- With a trained Bayes filter in play --
	store := newFakeBayesStore()
	bayes := antispam.NewBayesFilter(store, store)
	spamTokens := antispam.Tokenize(subject, body)
	for range 20 {
		if err := bayes.Train(userID, spamTokens, true); err != nil {
			t.Fatalf("Train: %v", err)
		}
		if err := bayes.IncrementTrained(userID); err != nil {
			t.Fatalf("IncrementTrained: %v", err)
		}
	}
	if got := bayes.TrainedCount(userID); got < 20 {
		t.Fatalf("TrainedCount = %d, want >= 20 (blend gate)", got)
	}

	bayesBaseline := NewBuiltinProcessor(antispam.NewScorer(), WithBayes(bayes), WithThreshold(0.99), WithHighThreshold(0.999))
	bayesBaseline.SetResolver(identityResolver{})
	bayesBase, err := bayesBaseline.Process(context.Background(), newEnv("198.51.100.7"), strings.NewReader(msg))
	if err != nil {
		t.Fatalf("bayes baseline Process: %v", err)
	}

	bayesResolver := newFakeDNSBLResolver(zone)
	bayesProc := newDNSBLProcessor(t, bayesResolver, []string{zone}, map[string]float64{zone: weight}, WithBayes(bayes), WithThreshold(0.99), WithHighThreshold(0.999))
	bayesProc.SetResolver(identityResolver{})
	bayesWith, err := bayesProc.Process(context.Background(), newEnv("198.51.100.7"), strings.NewReader(msg))
	if err != nil {
		t.Fatalf("bayes+dnsbl Process: %v", err)
	}
	bayesDelta := bayesWith.Score - bayesBase.Score

	if !dnsblApproxEqual(plainDelta, float32(weight)) {
		t.Errorf("plain delta = %.6f, want ~%.6f (the configured weight)", plainDelta, weight)
	}
	if !dnsblApproxEqual(bayesDelta, float32(weight)) {
		t.Errorf("bayes-blended delta = %.6f, want ~%.6f (the configured weight) -- if this differs from the plain delta, DNSBL is being diluted by the 0.6 blend factor", bayesDelta, weight)
	}
	if !dnsblApproxEqual(plainDelta, bayesDelta) {
		t.Errorf("plain delta %.6f != bayes-blended delta %.6f -- the same configured weight must move the score by the same amount regardless of whether Bayes blended", plainDelta, bayesDelta)
	}
}

// TestApplyDNSBL_UnknownZoneContributesNothing covers the config/checker
// drift case from the revised task definition: a zone the checker reports
// as listed but that has no entry in the weights map (because config and
// checker zone lists have drifted) must contribute nothing, not panic, and
// not silently guess a weight.
func TestApplyDNSBL_UnknownZoneContributesNothing(t *testing.T) {
	const zone = "zen.example-bl.test"

	baseline := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
	baseResult, err := baseline.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}

	resolver := newFakeDNSBLResolver(zone)
	// Empty weights map: the checker is configured with the zone (so it can
	// list it), but nothing maps that zone to a weight.
	proc := newDNSBLProcessor(t, resolver, []string{zone}, map[string]float64{})

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if !dnsblApproxEqual(result.Score, baseResult.Score) {
		t.Errorf("Score = %.6f, want unchanged from baseline %.6f (unknown zone must contribute nothing)", result.Score, baseResult.Score)
	}
	for _, r := range result.Reasons {
		if strings.HasPrefix(r, "dnsbl_") {
			t.Errorf("Reasons = %v, want no dnsbl_ reason for an unweighted zone", result.Reasons)
		}
	}
}

// TestApplyDNSBL_DeadZoneFailsOpen pins that 13.1's fail-open guarantee
// survives the 13.3 wiring: a zone that never answers (simulating a
// dead/timing-out resolver) must leave the message accepted and scored as
// not-listed, not stuck or erroring.
func TestApplyDNSBL_DeadZoneFailsOpen(t *testing.T) {
	const zone = "zen.example-bl.test"

	baseline := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
	baseResult, err := baseline.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("baseline Process: %v", err)
	}

	// hangingResolver never returns until ctx is cancelled, simulating a
	// dead zone; the Checker's own shared timeout (not a second layer added
	// here) is what bounds this.
	resolver := &hangingResolver{}
	checker := dnsbl.NewCheckerWithResolver(resolver, []string{zone}, 50*time.Millisecond, time.Minute, nil)
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithDNSBL(checker, map[string]float64{zone: 0.5}), WithThreshold(0.5), WithHighThreshold(0.9))

	result, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Action == "reject" {
		t.Fatal("a dead zone must never cause rejection")
	}
	if !dnsblApproxEqual(result.Score, baseResult.Score) {
		t.Errorf("Score = %.6f, want unchanged from baseline %.6f (a timed-out zone scores as not-listed)", result.Score, baseResult.Score)
	}
}

type hangingResolver struct{}

func (hangingResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestBuiltinDNSBLDisabled_ScoringUnaffectedBySourceIP pins the "no dnsbl
// block configured" requirement: with WithDNSBL never applied, p.dnsbl stays
// nil and scoring is byte-identical regardless of SourceIp -- including a
// SourceIp that would have been listed had DNSBL been wired.
func TestBuiltinDNSBLDisabled_ScoringUnaffectedBySourceIP(t *testing.T) {
	proc := NewBuiltinProcessor(antispam.NewScorer(), WithThreshold(0.5), WithHighThreshold(0.9))
	if proc.dnsbl != nil {
		t.Fatal("expected dnsbl to be nil by default")
	}

	withoutIP, err := proc.Process(context.Background(), cleanEnv("", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process (no source IP): %v", err)
	}
	withIP, err := proc.Process(context.Background(), cleanEnv("203.0.113.5", false), strings.NewReader(cleanMessage))
	if err != nil {
		t.Fatalf("Process (with source IP): %v", err)
	}

	if withoutIP.Score != withIP.Score {
		t.Errorf("Score differs (%.6f vs %.6f) purely based on SourceIp with DNSBL disabled -- must be byte-identical", withoutIP.Score, withIP.Score)
	}
	if len(withIP.Reasons) != len(withoutIP.Reasons) {
		t.Errorf("Reasons differ (%v vs %v) with DNSBL disabled", withoutIP.Reasons, withIP.Reasons)
	}
}

// TestApplyDNSBL_SkipRules covers every "zero resolver calls" case from
// US-003: each of these must return before the checker is ever consulted.
// Every case configures a resolver that WOULD answer "listed" for the
// configured zone, so a call count of zero really does prove the lookup was
// skipped, not merely that this particular IP happens to be clean.
func TestApplyDNSBL_SkipRules(t *testing.T) {
	const zone = "zen.example-bl.test"

	tests := []struct {
		name     string
		sourceIP string
		trusted  bool
	}{
		{name: "trusted envelope", sourceIP: "203.0.113.5", trusted: true},
		{name: "RFC1918 private", sourceIP: "10.1.2.3"},
		{name: "RFC1918 private (192.168)", sourceIP: "192.168.1.1"},
		{name: "loopback", sourceIP: "127.0.0.1"},
		{name: "link-local v4", sourceIP: "169.254.1.1"},
		{name: "link-local v6", sourceIP: "fe80::1"},
		{name: "unspecified v4", sourceIP: "0.0.0.0"},
		{name: "unspecified v6", sourceIP: "::"},
		{name: "empty source IP", sourceIP: ""},
		{name: "unparseable source IP", sourceIP: "not-an-ip-address"},
		// No "feature disabled" row here on purpose. Building a processor
		// with no DNSBL option leaves the fake resolver unwired, so calls==0
		// would hold no matter what applyDNSBL did -- the row would read as
		// evidence while asserting nothing. The disabled case is covered for
		// real by TestBuiltinDNSBLDisabled_ScoringUnaffectedBySourceIP.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeDNSBLResolver(zone)
			proc := newDNSBLProcessor(t, resolver, []string{zone}, map[string]float64{zone: 0.5})

			result, err := proc.Process(context.Background(), cleanEnv(tc.sourceIP, tc.trusted), strings.NewReader(cleanMessage))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}

			if got := resolver.calls.Load(); got != 0 {
				t.Errorf("resolver was called %d time(s), want 0", got)
			}
			for _, r := range result.Reasons {
				if strings.HasPrefix(r, "dnsbl_") {
					t.Errorf("Reasons = %v, want no dnsbl_ reason when the lookup is skipped", result.Reasons)
				}
			}
		})
	}
}
