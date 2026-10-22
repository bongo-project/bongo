package deliver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/pipeline"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeTargets is a ForwardTargetChecker over a fixed "user|target" allow set.
type fakeTargets struct {
	allowed map[string]bool
	err     error
}

func (f *fakeTargets) Allowed(userID, target string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.allowed[userID+"|"+target], nil
}

// allowAllTargets approves every forward target; for tests that are about
// something other than verification.
type allowAllTargets struct{}

func (allowAllTargets) Allowed(string, string) (bool, error) { return true, nil }

// countLimiter allows the first max calls per key.
type countLimiter struct {
	max   int
	count map[string]int
}

func (c *countLimiter) Allow(key string) bool {
	if c.count == nil {
		c.count = make(map[string]int)
	}
	c.count[key]++
	return c.count[key] <= c.max
}

const extTarget = "external@other.com"

type fwdFixture struct {
	proc    *DeliverProcessor
	applier *mockApplier
	queuer  *mockQueuer
}

// newFwdFixture builds a processor with alice and bob as local users, carol
// as a third local mailbox (a hosted forward target), and the given checker.
func newFwdFixture(t *testing.T, targets ForwardTargetChecker, prefs map[string]*bongov1.UserPreferences) *fwdFixture {
	t.Helper()
	f := &fwdFixture{applier: &mockApplier{}, queuer: &mockQueuer{}}
	resolver := &mockResolver{mapping: map[string]string{
		"alice@example.com": "user-alice",
		"bob@example.com":   "user-bob",
		"carol@example.com": "user-carol",
		"alias@example.com": "user-alice",
		// A verified send-as identity on an external domain resolves to its
		// owner, exactly as the store's identity tier does.
		"alice@gmail.com": "user-alice",
	}}
	f.proc = NewDeliverProcessor(f.applier, resolver, newTestLogger())
	f.proc.SetQueuer(f.queuer)
	f.proc.SetHostedDomains(func(d string) bool { return d == "example.com" })
	if prefs != nil {
		f.proc.SetPreferences(&mockPrefs{prefs: prefs})
	}
	if targets != nil {
		f.proc.SetForwardTargets(targets)
	}
	return f
}

func (f *fwdFixture) deliver(t *testing.T, env *bongov1.QueueEnvelope, raw string) {
	t.Helper()
	if env.ReceivedAt == 0 {
		env.ReceivedAt = time.Now().Unix()
	}
	if env.Sender == "" {
		env.Sender = "sender@remote.example"
	}
	if env.BlobHash == "" {
		env.BlobHash = "fwdblob"
	}
	if _, err := f.proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
}

func rawFor(to string) string {
	return minimalMessage("sender@remote.example", to, "Hello", "Hi!\r\n")
}

func accountFwd(keepCopy bool) map[string]*bongov1.UserPreferences {
	return map[string]*bongov1.UserPreferences{
		"user-alice": {ForwardAddress: extTarget, ForwardKeepCopy: keepCopy},
	}
}

func ruleFwd(userID, target string) []*bongov1.ForwardRequest {
	return []*bongov1.ForwardRequest{{UserId: userID, Target: target}}
}

// The positive controls share the denial cases' dataset: bob is verified for
// extTarget, alice is not, and the same message shape is used throughout.
func TestForwardRequiresVerifiedTarget(t *testing.T) {
	bobOnly := &fakeTargets{allowed: map[string]bool{"user-bob|" + extTarget: true}}
	aliceOK := &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}

	tests := []struct {
		name     string
		targets  ForwardTargetChecker
		prefs    map[string]*bongov1.UserPreferences
		requests []*bongov1.ForwardRequest
		legacy   []string
		want     int
	}{
		{"account forward unverified for this user", bobOnly, accountFwd(true), nil, nil, 0},
		{"account forward verified", aliceOK, accountFwd(true), nil, nil, 1},
		{"rule forward unverified for this user", bobOnly, nil, ruleFwd("user-alice", extTarget), nil, 0},
		{"rule forward verified", aliceOK, nil, ruleFwd("user-alice", extTarget), nil, 1},
		{"no checker configured fails closed (account)", nil, accountFwd(true), nil, nil, 0},
		{"no checker configured fails closed (rule)", nil, nil, ruleFwd("user-alice", extTarget), nil, 0},
		{"checker error denies", &fakeTargets{err: errors.New("pebble down")}, accountFwd(true), nil, nil, 0},
		{"unattributed rule request is never sent", allowAllTargets{}, nil, ruleFwd("", extTarget), nil, 0},
		{"legacy unattributed forward_addresses never sent", allowAllTargets{}, nil, nil, []string{extTarget}, 0},
		{"hosted target needs no verification", &fakeTargets{}, nil, ruleFwd("user-alice", "carol@example.com"), nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFwdFixture(t, tt.targets, tt.prefs)
			f.deliver(t, &bongov1.QueueEnvelope{
				Id: "env-v", Recipients: []string{"alice@example.com"},
				ForwardRequests: tt.requests, ForwardAddresses: tt.legacy,
			}, rawFor("alice@example.com"))
			if got := len(f.queuer.messages); got != tt.want {
				t.Fatalf("forwarded = %d, want %d", got, tt.want)
			}
			if len(f.applier.commands) != 1 {
				t.Errorf("local delivery commands = %d, want 1", len(f.applier.commands))
			}
		})
	}
}

// An external address that resolves to another user (their verified send-as
// identity) is not hosted here: bob must verify it himself.
func TestForwardToOthersExternalIdentityNeedsOwnVerification(t *testing.T) {
	const aliceExt = "alice@gmail.com"
	bobPrefs := map[string]*bongov1.UserPreferences{
		"user-bob": {ForwardAddress: aliceExt, ForwardKeepCopy: true},
	}
	tests := []struct {
		name     string
		targets  ForwardTargetChecker
		prefs    map[string]*bongov1.UserPreferences
		requests []*bongov1.ForwardRequest
		want     int
	}{
		{"account forward, not verified by bob", &fakeTargets{allowed: map[string]bool{"user-alice|" + aliceExt: true}}, bobPrefs, nil, 0},
		{"rule forward, not verified by bob", &fakeTargets{allowed: map[string]bool{"user-alice|" + aliceExt: true}}, nil, ruleFwd("user-bob", aliceExt), 0},
		{"control: account forward verified by bob", &fakeTargets{allowed: map[string]bool{"user-bob|" + aliceExt: true}}, bobPrefs, nil, 1},
		{"control: rule forward verified by bob", &fakeTargets{allowed: map[string]bool{"user-bob|" + aliceExt: true}}, nil, ruleFwd("user-bob", aliceExt), 1},
		{"control: hosted-domain target needs no verification", &fakeTargets{}, nil, ruleFwd("user-bob", "carol@example.com"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFwdFixture(t, tt.targets, tt.prefs)
			f.deliver(t, &bongov1.QueueEnvelope{
				Id: "env-ext-id", Recipients: []string{"bob@example.com"}, ForwardRequests: tt.requests,
			}, rawFor("bob@example.com"))
			if got := len(f.queuer.messages); got != tt.want {
				t.Fatalf("forwarded = %d, want %d", got, tt.want)
			}
		})
	}
}

// A forward to another address of the same user would loop, and with keep_copy
// off it would swallow the message: it must count as not forwarded.
func TestSelfForwardStillDeliversLocally(t *testing.T) {
	f := newFwdFixture(t, allowAllTargets{}, map[string]*bongov1.UserPreferences{
		"user-alice": {ForwardAddress: "alias@example.com", ForwardKeepCopy: false},
	})
	f.deliver(t, &bongov1.QueueEnvelope{Id: "env-self", Recipients: []string{"alice@example.com"}}, rawFor("alice@example.com"))
	if len(f.queuer.messages) != 0 {
		t.Errorf("self-forward enqueued %d messages", len(f.queuer.messages))
	}
	if len(f.applier.commands) != 1 {
		t.Errorf("local deliveries = %d, want 1", len(f.applier.commands))
	}
}

func TestForwardNeverSendsSpam(t *testing.T) {
	allow := &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}
	spam := func(score float32) []*bongov1.StageResult {
		return []*bongov1.StageResult{{Stage: "antispam", Action: "pass", Score: score}}
	}
	tagged := func(score float32) []*bongov1.StageResult {
		return []*bongov1.StageResult{{Stage: "antispam", Backend: "rspamd", Action: "tag", Score: score}}
	}
	tests := []struct {
		name    string
		stages  []*bongov1.StageResult
		mailbox string
		want    int
	}{
		{"clean mail forwarded", spam(0.1), "", 1},
		{"score above tag threshold", spam(0.9), "", 0},
		{"score just under the threshold forwarded", spam(0.49), "", 1},
		{"tag verdict below threshold", tagged(0.4), "", 0},
		{"tag verdict with target overwritten by a rule", tagged(0.4), "Work", 0},
		{"pass verdict with target moved by a rule", spam(0.1), "Work", 1},
		{"routed to Junk", spam(0.1), "Junk", 0},
		{"routed to Quarantine", spam(0.1), "quarantine", 0},
		{"quarantine action from another stage", []*bongov1.StageResult{{Stage: "antivirus", Action: "quarantine"}}, "", 0},
		{"quarantine verdict released and rescanned", []*bongov1.StageResult{{Stage: "antispam", Action: "quarantine", Score: 0.1}}, "", 0},
	}
	for _, tt := range tests {
		for _, via := range []string{"account", "rule"} {
			t.Run(tt.name+"/"+via, func(t *testing.T) {
				var prefs map[string]*bongov1.UserPreferences
				var reqs []*bongov1.ForwardRequest
				if via == "account" {
					prefs = accountFwd(true)
				} else {
					reqs = ruleFwd("user-alice", extTarget)
				}
				f := newFwdFixture(t, allow, prefs)
				f.deliver(t, &bongov1.QueueEnvelope{
					Id: "env-spam", Recipients: []string{"alice@example.com"},
					Stages: tt.stages, TargetMailbox: tt.mailbox, ForwardRequests: reqs,
				}, rawFor("alice@example.com"))
				if got := len(f.queuer.messages); got != tt.want {
					t.Fatalf("forwarded = %d, want %d", got, tt.want)
				}
			})
		}
	}
}

func TestForwardRateLimitedPerUser(t *testing.T) {
	allow := &fakeTargets{allowed: map[string]bool{
		"user-alice|" + extTarget: true,
		"user-bob|" + extTarget:   true,
	}}
	f := newFwdFixture(t, allow, map[string]*bongov1.UserPreferences{
		"user-alice": {ForwardAddress: extTarget, ForwardKeepCopy: true},
		"user-bob":   {ForwardAddress: extTarget, ForwardKeepCopy: true},
	})
	f.proc.SetForwardLimiter(&countLimiter{max: 3})

	// Alice sends 5 messages against a limit of 3; bob's single message must
	// still go because the budget is per user.
	for i := range 5 {
		f.deliver(t, &bongov1.QueueEnvelope{Id: "env-rl-a" + string(rune('0'+i)), Recipients: []string{"alice@example.com"}}, rawFor("alice@example.com"))
	}
	f.deliver(t, &bongov1.QueueEnvelope{Id: "env-rl-b", Recipients: []string{"bob@example.com"}}, rawFor("bob@example.com"))

	if got := len(f.queuer.messages); got != 4 {
		t.Fatalf("forwarded = %d, want 4 (3 for alice, 1 for bob)", got)
	}
	if got := len(f.applier.commands); got != 6 {
		t.Errorf("local deliveries = %d, want 6 (the limit never drops the local copy)", got)
	}
}

// The budget belongs to the user, not to each (user, target) pair.
func TestForwardRateLimitSharedAcrossTargets(t *testing.T) {
	const second = "second@other.com"
	allow := &fakeTargets{allowed: map[string]bool{
		"user-alice|" + extTarget: true,
		"user-alice|" + second:    true,
	}}
	f := newFwdFixture(t, allow, accountFwd(true))
	f.proc.SetForwardLimiter(&countLimiter{max: 3})
	for i := range 4 {
		f.deliver(t, &bongov1.QueueEnvelope{
			Id: "env-multi" + string(rune('0'+i)), Recipients: []string{"alice@example.com"},
			ForwardRequests: ruleFwd("user-alice", second),
		}, rawFor("alice@example.com"))
	}
	total := 0
	for _, m := range f.queuer.messages {
		total += len(m.Recipients)
	}
	if total != 3 {
		t.Fatalf("forwarded copies = %d, want 3 (one budget across both targets)", total)
	}
}

// Two recipients of one message share a target; only the verified one forwards,
// whichever order they are processed in.
func TestForwardDecisionNotSharedAcrossUsers(t *testing.T) {
	for _, order := range [][]string{
		{"alice@example.com", "bob@example.com"},
		{"bob@example.com", "alice@example.com"},
	} {
		allow := &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}
		f := newFwdFixture(t, allow, map[string]*bongov1.UserPreferences{
			"user-alice": {ForwardAddress: extTarget, ForwardKeepCopy: false},
			"user-bob":   {ForwardAddress: extTarget, ForwardKeepCopy: false},
		})
		f.deliver(t, &bongov1.QueueEnvelope{Id: "env-multi-rcpt", Recipients: order}, rawFor("alice@example.com"))
		if got := len(f.queuer.messages); got != 1 {
			t.Fatalf("%v: forwarded = %d, want 1", order, got)
		}
		// Alice's copy is replaced by the forward; bob is unverified so keeps his.
		if got := len(f.applier.commands); got != 1 {
			t.Fatalf("%v: local deliveries = %d, want 1 (bob only)", order, got)
		}
	}
}

// Without keep_copy a blocked forward used to lose the message outright.
func TestBlockedForwardStillDeliversLocally(t *testing.T) {
	spamStages := []*bongov1.StageResult{{Stage: "antispam", Action: "tag", Score: 0.9}}
	tests := []struct {
		name    string
		targets ForwardTargetChecker
		stages  []*bongov1.StageResult
		raw     string
		limiter ForwardLimiter
		wantFwd int
	}{
		{"unverified target", &fakeTargets{}, nil, rawFor("alice@example.com"), nil, 0},
		{"spam", &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}, spamStages, rawFor("alice@example.com"), nil, 0},
		{"rate limited", &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}, nil, rawFor("alice@example.com"), &countLimiter{max: 0}, 0},
		{"hop limit", &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}, nil,
			strings.Repeat("Received: from a by b; Mon, 01 Jan 2024 12:00:00 +0000\r\n", maxForwardHops+1) + rawFor("alice@example.com"), nil, 0},
		{"control: forward goes and local copy is skipped", &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}, nil, rawFor("alice@example.com"), nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFwdFixture(t, tt.targets, accountFwd(false))
			if tt.limiter != nil {
				f.proc.SetForwardLimiter(tt.limiter)
			}
			f.deliver(t, &bongov1.QueueEnvelope{Id: "env-keep", Recipients: []string{"alice@example.com"}, Stages: tt.stages}, tt.raw)
			if got := len(f.queuer.messages); got != tt.wantFwd {
				t.Fatalf("forwarded = %d, want %d", got, tt.wantFwd)
			}
			wantLocal := 1 - tt.wantFwd
			if got := len(f.applier.commands); got != wantLocal {
				t.Fatalf("local deliveries = %d, want %d", got, wantLocal)
			}
		})
	}
}

var _ = pipeline.MsgFlagDelivered
