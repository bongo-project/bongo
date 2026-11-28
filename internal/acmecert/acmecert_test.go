package acmecert_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/acmecert"
	"github.com/bongo-project/bongo/internal/acmecert/acmetest"
	"github.com/bongo-project/bongo/internal/security"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// memStore is an in-memory Store that counts reads and writes.
type memStore struct {
	mu      sync.Mutex
	m       map[string][]byte
	gets    int
	puts    int
	deletes int
	noWrite bool // a non-leader: Put and Delete fail
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Get(k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	return s.m[k], nil
}

func (s *memStore) Put(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noWrite {
		return errors.New("not the leader")
	}
	s.puts++
	s.m[k] = v
	return nil
}

func (s *memStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noWrite {
		return errors.New("not the leader")
	}
	s.deletes++
	delete(s.m, k)
	return nil
}

func (s *memStore) List(prefix string) ([]string, error) { return s.keys(prefix), nil }

func (s *memStore) keys(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	dir := "https://ca.example/directory"
	rec := func(daysLeft int, domains []string, directory string) *bongov1.AcmeCertificate {
		return &bongov1.AcmeCertificate{
			NotAfter:     now.Add(time.Duration(daysLeft) * 24 * time.Hour).Unix(),
			Domains:      domains,
			DirectoryUrl: directory,
		}
	}
	d := []string{"mail.example.com", "imap.example.com"}
	tests := []struct {
		name string
		cur  *bongov1.AcmeCertificate
		want acmecert.Reason
	}{
		{"no certificate", nil, acmecert.ReasonNoCert},
		{"31 days left", rec(31, d, dir), acmecert.ReasonNone},
		{"29 days left", rec(29, d, dir), acmecert.ReasonRenewal},
		{"expired", rec(-1, d, dir), acmecert.ReasonRenewal},
		{"domains changed", rec(60, []string{"mail.example.com"}, dir), acmecert.ReasonDomains},
		{"domain order is irrelevant", rec(60, []string{"imap.example.com", "mail.example.com"}, dir), acmecert.ReasonNone},
		{"directory changed", rec(60, d, "https://staging.example/directory"), acmecert.ReasonDirectory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acmecert.Due(tt.cur, d, dir, now); got != tt.want {
				t.Errorf("Due = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		n    int
		rnd  float64
		want time.Duration
	}{
		{1, 0, 5 * time.Minute},
		{2, 0, 10 * time.Minute},
		{3, 0, 20 * time.Minute},
		{8, 0, 12 * time.Hour}, // 5m*2^7 = 10h40m -> capped at 12h only from n=9
		{9, 0, 12 * time.Hour},
		{40, 0, 12 * time.Hour},
		{1, 0.5, 5*time.Minute + 30*time.Second},
		{0, 0, 5 * time.Minute},
	}
	for _, tt := range tests {
		got := acmecert.Backoff(tt.n, tt.rnd)
		if tt.n == 8 {
			if got != 10*time.Hour+40*time.Minute {
				t.Errorf("Backoff(8) = %v, want 10h40m", got)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("Backoff(%d, %v) = %v, want %v", tt.n, tt.rnd, got, tt.want)
		}
	}
	if acmecert.Backoff(1, 0.999) >= 6*time.Minute {
		t.Error("jitter exceeds 20 percent")
	}
}

func TestExpiryWarning(t *testing.T) {
	now := time.Now()
	if !acmecert.ExpiryWarning(now.Add(13*24*time.Hour), now) {
		t.Error("13 days should warn")
	}
	if acmecert.ExpiryWarning(now.Add(20*24*time.Hour), now) {
		t.Error("20 days should not warn")
	}
}

func TestKeysAndRecords(t *testing.T) {
	if a, b := acmecert.AccountKey("https://a/d"), acmecert.AccountKey("https://b/d"); a == b || !strings.HasPrefix(a, "/system/acme/account/") || len(a) != len("/system/acme/account/")+32 {
		t.Errorf("account keys: %q %q", a, b)
	}
	if got := acmecert.ChallengeKey("Mail.Example.COM"); got != "/system/acme/challenge/mail.example.com" {
		t.Errorf("challenge key = %q", got)
	}
	if _, err := acmecert.DecodeCertificate([]byte{0xff, 0xff, 0xff}); !errors.Is(err, acmecert.ErrBadRecord) {
		t.Errorf("garbage decode err = %v", err)
	}
	if _, err := acmecert.TLSCertificate(&bongov1.AcmeCertificate{}); !errors.Is(err, acmecert.ErrBadRecord) {
		t.Errorf("empty record err = %v", err)
	}
}

// node stands in for a node's web listener: a TLS server whose GetCertificate
// is the production wrapper over the shared store.
type node struct {
	addr   string
	closed func()
}

func startNode(t *testing.T, store acmecert.Store, domains []string, normal *tls.Certificate) *node {
	t.Helper()
	cfg := acmecert.TLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}, store, domains, func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return normal, nil
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.(*tls.Conn).Handshake()
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-done })
	return &node{addr: ln.Addr().String()}
}

// lockedBuf is a bytes.Buffer safe for the logger's goroutines.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newLogger(buf *lockedBuf) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

var testDomains = []string{"imap.example.com", "mail.example.com"}

func setup(t *testing.T) (*acmetest.Server, *memStore, *node) {
	t.Helper()
	store := newMemStore()
	var n *node
	ca := acmetest.New(t, func(string) string { return n.addr })
	n = startNode(t, store, testDomains, &tls.Certificate{})
	return ca, store, n
}

func TestIssueEndToEnd(t *testing.T) {
	ca, store, _ := setup(t)
	var logs lockedBuf
	var served *tls.Certificate
	m := acmecert.New(store, acmecert.Options{
		Email:        "admin@example.com",
		Domains:      testDomains,
		DirectoryURL: ca.DirectoryURL(),
		HTTPClient:   ca.Client(),
		Logger:       newLogger(&logs),
		OnIssued:     func(c *tls.Certificate) error { served = c; return nil },
	})

	issued, err := m.Check(context.Background())
	if err != nil || !issued {
		t.Fatalf("Check = %v, %v", issued, err)
	}
	if served == nil || served.Leaf == nil {
		t.Fatal("the holder callback was not given the certificate")
	}
	if !slices.Equal(served.Leaf.DNSNames, testDomains) {
		t.Errorf("served names = %v", served.Leaf.DNSNames)
	}
	if got := ca.Validated(); len(got) != 2 {
		t.Errorf("validated = %v, want both domains through a real TLS-ALPN-01 probe", got)
	}

	// The certificate and the account are in the store; the challenges are gone.
	rec, cert, err := acmecert.LoadCertificate(store)
	if err != nil || rec == nil || cert == nil {
		t.Fatalf("LoadCertificate = %v %v %v", rec, cert, err)
	}
	if cert.Leaf.SerialNumber.Cmp(served.Leaf.SerialNumber) != 0 {
		t.Error("stored and served serial differ")
	}
	if got := store.keys("/system/acme/account/"); len(got) != 1 || got[0] != acmecert.AccountKey(ca.DirectoryURL()) {
		t.Errorf("account keys = %v", got)
	}
	if got := store.keys("/system/acme/challenge/"); len(got) != 0 {
		t.Errorf("challenge keys left behind: %v", got)
	}

	// A second Check inside the window does nothing.
	if issued, err := m.Check(context.Background()); err != nil || issued {
		t.Errorf("second Check = %v, %v", issued, err)
	}

	// Nothing secret in the log.
	out := logs.String()
	if strings.Contains(out, "PRIVATE KEY") {
		t.Error("log mentions a private key")
	}
	for _, k := range store.keys("/system/") {
		raw, _ := store.Get(k)
		for _, secret := range secretsIn(t, raw) {
			if strings.Contains(out, base64.StdEncoding.EncodeToString(secret)) || strings.Contains(out, base64.RawURLEncoding.EncodeToString(secret)) {
				t.Errorf("log contains key material from %s", k)
			}
		}
	}
	for _, tok := range ca.Tokens() {
		if strings.Contains(out, tok) {
			t.Error("log contains a challenge token")
		}
	}
	if !strings.Contains(out, "serial=") || !strings.Contains(out, "not_after=") {
		t.Errorf("success log lacks serial and not_after: %s", out)
	}
}

// A CA that hands back a defective leaf never gets it stored or served: the
// chain is verified before the Put of /system/acme/cert.
func TestIssueRefusesTamperedCertificate(t *testing.T) {
	tests := []struct {
		name   string
		tamper acmetest.Tamper
	}{
		{"a requested name missing", acmetest.TamperDropName},
		{"a name not requested", acmetest.TamperExtraName},
		{"another key", acmetest.TamperOtherKey},
		{"already expired", acmetest.TamperExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ca, store, _ := setup(t)
			ca.SetTamper(tt.tamper)
			called := false
			m := acmecert.New(store, acmecert.Options{
				Domains:      testDomains,
				DirectoryURL: ca.DirectoryURL(),
				HTTPClient:   ca.Client(),
				Logger:       newLogger(&lockedBuf{}),
				OnIssued:     func(*tls.Certificate) error { called = true; return nil },
			})
			issued, err := m.Check(context.Background())
			if err == nil || issued {
				t.Fatalf("Check = %v, %v; want a refusal", issued, err)
			}
			if len(ca.Issued()) != 1 {
				t.Fatalf("the CA issued %d certificates, want 1 (the refusal must come after issuance)", len(ca.Issued()))
			}
			if raw, _ := store.Get(acmecert.CertKey); raw != nil {
				t.Error("a defective certificate was stored")
			}
			if called {
				t.Error("a defective certificate reached the holders")
			}
		})
	}
}

// secretsIn returns the key_der of any record that has one.
func secretsIn(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	var out [][]byte
	var c bongov1.AcmeCertificate
	if proto.Unmarshal(raw, &c) == nil && len(c.KeyDer) > 0 {
		out = append(out, c.KeyDer)
	}
	var a bongov1.AcmeAccount
	if proto.Unmarshal(raw, &a) == nil && len(a.KeyDer) > 0 {
		out = append(out, a.KeyDer)
	}
	return out
}

func TestIssueFailureBacksOffAndKeepsOldCertificate(t *testing.T) {
	ca, store, _ := setup(t)
	// A valid certificate is already stored.
	first := acmecert.New(store, acmecert.Options{
		Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
		Logger: newLogger(&lockedBuf{}),
	})
	if issued, err := first.Check(context.Background()); err != nil || !issued {
		t.Fatalf("seeding: %v, %v", issued, err)
	}
	oldRaw, _ := store.Get(acmecert.CertKey)

	ca.SetFailing(true)
	// 70 days later the certificate is inside the renewal window.
	now := time.Now().Add(70 * 24 * time.Hour)
	var logs lockedBuf
	called := false
	m := acmecert.New(store, acmecert.Options{
		Domains:      testDomains,
		DirectoryURL: ca.DirectoryURL(),
		HTTPClient:   ca.Client(),
		Logger:       newLogger(&logs),
		OnIssued:     func(*tls.Certificate) error { called = true; return nil },
		Now:          func() time.Time { return now },
		Rand:         func() float64 { return 0 },
		IssueTimeout: 20 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, func() bool { return true }, 0, time.Hour)
	waitFor(t, func() bool { return m.Failures() == 1 })

	// The counter is published after the log line and the saved state, so
	// both are visible as soon as Failures() reports the failure.
	if !strings.Contains(logs.String(), "issuance failed") || !strings.Contains(logs.String(), "problem_type") {
		t.Errorf("failure not logged with the CA problem: %s", logs.String())
	}
	if raw, _ := store.Get(acmecert.StateKey); len(raw) != 16 {
		t.Errorf("retry state not saved: %d bytes", len(raw))
	}
	if called {
		t.Error("a failed issuance reached the holders")
	}
	if got := m.RetryAt().Sub(now); got != 5*time.Minute {
		t.Errorf("retry in %v, want 5m", got)
	}
	if got := store.keys("/system/acme/challenge/"); len(got) != 0 {
		t.Errorf("challenges not cleaned after failure: %v", got)
	}
	if newRaw, _ := store.Get(acmecert.CertKey); !bytes.Equal(newRaw, oldRaw) {
		t.Error("the stored certificate was replaced after a failed validation")
	}
}

// clock is a settable time source safe for the Run goroutine.
type clock struct{ ns atomic.Int64 }

func newClock(t time.Time) *clock { c := &clock{}; c.set(t); return c }
func (c *clock) set(t time.Time)  { c.ns.Store(t.UnixNano()) }
func (c *clock) now() time.Time   { return time.Unix(0, c.ns.Load()) }

func TestBackoffSurvivesRestartAndNewLeader(t *testing.T) {
	ca, store, _ := setup(t)
	ca.SetFailing(true)
	clk := newClock(time.Now())
	opts := func() acmecert.Options {
		return acmecert.Options{
			Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
			Logger: newLogger(&lockedBuf{}), Now: clk.now, Rand: func() float64 { return 0 },
			IssueTimeout: 20 * time.Second,
		}
	}
	m1 := acmecert.New(store, opts())
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() { m1.Run(ctx1, func() bool { return true }, 0, 10*time.Millisecond); close(done1) }()
	waitFor(t, func() bool { return m1.Failures() == 1 })
	cancel1()
	<-done1
	hits := ca.Hits()

	// A restart (or another leader) with a healthy CA must still wait out the
	// persisted floor.
	ca.SetFailing(false)
	m2 := acmecert.New(store, opts())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go m2.Run(ctx2, func() bool { return true }, 0, 10*time.Millisecond)
	waitFor(t, func() bool { return m2.Failures() == 1 })
	time.Sleep(200 * time.Millisecond)
	if got := ca.Hits(); got != hits {
		t.Fatalf("the CA was contacted %d times inside the persisted backoff", got-hits)
	}

	// Past the floor it issues, and the state is cleared.
	clk.set(clk.now().Add(6 * time.Minute))
	waitFor(t, func() bool { rec, _, _ := acmecert.LoadCertificate(store); return rec != nil })
	waitFor(t, func() bool { raw, _ := store.Get(acmecert.StateKey); return raw == nil })
	if m2.Failures() != 0 {
		t.Errorf("failures = %d after success", m2.Failures())
	}
}

func TestLosingLeadershipCancelsIssuance(t *testing.T) {
	ca, store, _ := setup(t)
	var leader atomic.Bool
	leader.Store(true)
	m := acmecert.New(store, acmecert.Options{
		Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
		Logger: newLogger(&lockedBuf{}),
		// A settle pause far longer than the test: only cancellation ends it.
		Settle:     5 * time.Minute,
		LeaderPoll: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, leader.Load, 0, time.Hour)

	// Wait for every challenge: before the last one is published the issuer
	// may still be fetching an authorisation, which is not a deposed request.
	// After it, the issuer only sleeps through the settle pause.
	waitFor(t, func() bool { return len(store.keys("/system/acme/challenge/")) == len(testDomains) })
	hits := ca.Hits()
	leader.Store(false)
	// The deferred cleanup removes the challenges once Issue returns.
	waitFor(t, func() bool { return len(store.keys("/system/acme/challenge/")) == 0 })
	if got := ca.Hits(); got != hits {
		t.Errorf("the deposed leader made %d more CA requests", got-hits)
	}
	if rec, _, _ := acmecert.LoadCertificate(store); rec != nil {
		t.Error("a deposed leader stored a certificate")
	}
	if m.Failures() != 0 {
		t.Errorf("abandoning on lost leadership counted %d failures", m.Failures())
	}
	if raw, _ := store.Get(acmecert.StateKey); raw != nil {
		t.Error("abandoning on lost leadership saved a retry state")
	}
}

func TestLeaderSweepsStaleChallenges(t *testing.T) {
	ca, store, _ := setup(t)
	stale := acmecert.ChallengeKey("gone.example.com")
	_ = store.Put(stale, []byte("x"))
	m := acmecert.New(store, acmecert.Options{
		Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
		Logger: newLogger(&lockedBuf{}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan struct{})
	go func() { m.Run(ctx, func() bool { return true }, 0, time.Hour); close(leaderDone) }()
	waitFor(t, func() bool { rec, _, _ := acmecert.LoadCertificate(store); return rec != nil })
	waitFor(t, func() bool { raw, _ := store.Get(stale); return raw == nil })
	// Stop the leader before the follower half: its sweep after issuance
	// could otherwise delete the entry put back below.
	cancel()
	<-leaderDone

	// A follower sweeps nothing.
	_ = store.Put(stale, []byte("x"))
	f := acmecert.New(store, acmecert.Options{Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(), Logger: newLogger(&lockedBuf{})})
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx2, func() bool { return false }, 0, 10*time.Millisecond); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel2()
	<-done
	if raw, _ := store.Get(stale); raw == nil {
		t.Error("a follower deleted a challenge entry")
	}
}

func TestDueShortLivedCertificate(t *testing.T) {
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	dir := "https://ca.example/directory"
	d := []string{"mail.example.com"}
	rec := func(lifetime time.Duration) *bongov1.AcmeCertificate {
		return &bongov1.AcmeCertificate{
			IssuedAt: now.Unix(), NotAfter: now.Add(lifetime).Unix(), Domains: d, DirectoryUrl: dir,
		}
	}
	day := 24 * time.Hour
	tests := []struct {
		name string
		cur  *bongov1.AcmeCertificate
		at   time.Duration // time since issuance
		want acmecert.Reason
	}{
		{"6-day cert right after issuance", rec(6 * day), 0, acmecert.ReasonNone},
		{"6-day cert after 3 days", rec(6 * day), 3 * day, acmecert.ReasonNone},
		{"6-day cert with under 2 days left", rec(6 * day), 4*day + time.Hour, acmecert.ReasonRenewal},
		{"60-day cert renews a third before expiry", rec(60 * day), 41 * day, acmecert.ReasonRenewal},
		{"60-day cert with 30+ days left", rec(60 * day), 29 * day, acmecert.ReasonNone},
		{"90-day cert keeps the 30 day window", rec(90 * day), 61 * day, acmecert.ReasonRenewal},
		{"90-day cert with 31 days left", rec(90 * day), 59 * day, acmecert.ReasonNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acmecert.Due(tt.cur, d, dir, now.Add(tt.at)); got != tt.want {
				t.Errorf("Due = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFollowerNeverCallsTheCA(t *testing.T) {
	ca, store, _ := setup(t)
	m := acmecert.New(store, acmecert.Options{
		Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
		Logger: newLogger(&lockedBuf{}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx, func() bool { return false }, 0, 10*time.Millisecond); close(done) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
	if ca.Hits() != 0 {
		t.Errorf("a follower made %d requests to the CA", ca.Hits())
	}
	if store.puts != 0 {
		t.Error("a follower wrote to the store")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ca, store, _ := setup(t)
	m := acmecert.New(store, acmecert.Options{Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(), Logger: newLogger(&lockedBuf{})})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx, func() bool { return false }, time.Hour, time.Hour); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestNonLeaderPutFailsCleanly(t *testing.T) {
	ca, store, _ := setup(t)
	store.noWrite = true
	m := acmecert.New(store, acmecert.Options{
		Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
		Logger: newLogger(&lockedBuf{}),
	})
	if _, err := m.Check(context.Background()); err == nil {
		t.Fatal("expected an error when the store refuses writes")
	}
	if ca.Hits() != 0 {
		t.Error("the CA was contacted although the account could not be stored")
	}
}

func TestGetCertificateRefusals(t *testing.T) {
	store := newMemStore()
	normal := &tls.Certificate{Certificate: [][]byte{{1}}}
	normalCalls := 0
	get := acmecert.GetCertificate(store, testDomains, func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		normalCalls++
		return normal, nil
	})

	tests := []struct {
		name       string
		hello      *tls.ClientHelloInfo
		wantErr    bool
		wantNormal bool
	}{
		{"browser hello", &tls.ClientHelloInfo{ServerName: "mail.example.com", SupportedProtos: []string{"h2", "http/1.1"}}, false, true},
		{"no ALPN at all", &tls.ClientHelloInfo{ServerName: "mail.example.com"}, false, true},
		{"acme alongside h2 is not a probe", &tls.ClientHelloInfo{ServerName: "mail.example.com", SupportedProtos: []string{"acme-tls/1", "h2"}}, false, true},
		{"probe, unknown name", &tls.ClientHelloInfo{ServerName: "evil.example.org", SupportedProtos: []string{"acme-tls/1"}}, true, false},
		{"probe, no SNI", &tls.ClientHelloInfo{SupportedProtos: []string{"acme-tls/1"}}, true, false},
		{"probe, known name, no challenge stored", &tls.ClientHelloInfo{ServerName: "mail.example.com", SupportedProtos: []string{"acme-tls/1"}}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := normalCalls
			c, err := get(tt.hello)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got := normalCalls > before; got != tt.wantNormal {
				t.Errorf("normal handler used = %v, want %v", got, tt.wantNormal)
			}
			if tt.wantErr && c != nil {
				t.Error("a refused hello still got a certificate")
			}
		})
	}
	if store.puts != 0 || store.deletes != 0 {
		t.Error("a handshake wrote to the store")
	}
}

// challengeRecord builds a stored TLS-ALPN-01 record that parses: a
// self-signed certificate for name and its PKCS#8 key.
func challengeRecord(t *testing.T, name string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&bongov1.AcmeChallenge{CertDer: der, KeyDer: keyDER})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A challenge stored under a name that is not configured (left behind by a
// domain since removed, say) is never served to a probe, and the store is not
// even read for that name. The configured case is the control: the same kind
// of record is served there.
func TestGetCertificateIgnoresUnconfiguredChallenge(t *testing.T) {
	store := newMemStore()
	for _, name := range []string{"evil.example.org", "mail.example.com"} {
		store.m[acmecert.ChallengeKey(name)] = challengeRecord(t, name)
	}
	get := acmecert.GetCertificate(store, testDomains, func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		t.Error("a probe reached the normal handler")
		return nil, errors.New("unexpected")
	})

	tests := []struct {
		name     string
		sni      string
		wantCert bool
		wantGets int
	}{
		{"unconfigured name with a stored challenge", "evil.example.org", false, 0},
		{"unconfigured name in upper case", "EVIL.example.org", false, 0},
		{"configured name", "mail.example.com", true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store.mu.Lock()
			store.gets = 0
			store.mu.Unlock()
			c, err := get(&tls.ClientHelloInfo{ServerName: tt.sni, SupportedProtos: []string{acme.ALPNProto}})
			if tt.wantCert {
				if err != nil || c == nil {
					t.Fatalf("configured probe refused: %v", err)
				}
			} else if err == nil || c != nil {
				t.Fatalf("probe for %q was answered with a challenge certificate", tt.sni)
			}
			store.mu.Lock()
			gets := store.gets
			store.mu.Unlock()
			if gets != tt.wantGets {
				t.Errorf("store reads = %d, want %d", gets, tt.wantGets)
			}
		})
	}
}

func TestProbeHandshakeCausesNoWrite(t *testing.T) {
	ca, store, _ := setup(t)
	m := acmecert.New(store, acmecert.Options{
		Domains: testDomains, DirectoryURL: ca.DirectoryURL(), HTTPClient: ca.Client(),
		Logger: newLogger(&lockedBuf{}),
	})
	// Count writes made by the issuance, then probe a node by hand: the
	// handshake itself must add none.
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err, errors.Unwrap(err))
	}
	puts, deletes := store.puts, store.deletes
	n := startNode(t, store, testDomains, &tls.Certificate{})
	conn, err := tls.Dial("tcp", n.addr, &tls.Config{ServerName: "mail.example.com", NextProtos: []string{acme.ALPNProto}, InsecureSkipVerify: true}) // #nosec G402 -- test probe
	if err == nil {
		conn.Close()
		t.Error("a probe without a stored challenge should fail the handshake")
	}
	if store.puts != puts || store.deletes != deletes {
		t.Error("a handshake wrote to the store")
	}
}

// TestBrowsersStillNegotiateH2 checks that appending acme-tls/1 to NextProtos
// leaves http.Server's own h2 and http/1.1 negotiation intact.
func TestBrowsersStillNegotiateH2(t *testing.T) {
	cert := selfSigned(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	})}
	srv.TLSConfig = acmecert.TLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}, newMemStore(), testDomains, func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return cert, nil
	})
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	defer srv.Close()
	srvURL := "https://" + ln.Addr().String()

	client := &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- test certificate
	}}
	resp, err := client.Get(srvURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Errorf("browser-style client negotiated HTTP/%d, want 2", resp.ProtoMajor)
	}

	h1 := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}, // #nosec G402 -- test certificate
	}}
	resp1, err := h1.Get(srvURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.ProtoMajor != 1 {
		t.Errorf("http/1.1 client got HTTP/%d", resp1.ProtoMajor)
	}

	// A client that offers acme-tls/1 next to h2 is a normal client: it must
	// get h2, never acme-tls/1.
	both, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "mail.example.com", NextProtos: []string{acme.ALPNProto, "h2"}, InsecureSkipVerify: true}) // #nosec G402 -- test client
	if err != nil {
		t.Fatalf("client offering acme-tls/1 and h2: %v", err)
	}
	if got := both.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Errorf("client offering acme-tls/1 and h2 negotiated %q, want h2", got)
	}
	both.Close()

	// A probe for a name with no challenge is refused at the handshake.
	if c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "mail.example.com", NextProtos: []string{acme.ALPNProto}, InsecureSkipVerify: true}); err == nil { // #nosec G402 -- test probe
		c.Close()
		t.Error("acme-tls/1 was answered with the normal certificate")
	}
}

// selfSigned returns a throwaway certificate for the h2 test.
func selfSigned(t *testing.T) *tls.Certificate {
	t.Helper()
	cfg, err := security.SelfSignedTLSConfig([]string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return &cfg.Certificates[0]
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
