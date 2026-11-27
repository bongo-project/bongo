package acmecert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// defaultIssueTimeout bounds one whole issuance.
	defaultIssueTimeout = 5 * time.Minute
	// defaultSettle is the pause between publishing the challenge
	// certificates and telling the CA to validate. A follower can apply a
	// committed entry a few milliseconds after the leader, and the CA may
	// connect to any node. A rare miss is retried by the backoff.
	defaultSettle = 2 * time.Second
	// defaultTick is how often the leader checks whether to issue.
	defaultTick = time.Hour
	// maxDetail bounds CA-supplied text in a log line.
	maxDetail = 200
)

// Options configures a Manager.
type Options struct {
	// Email is the optional account contact.
	Email        string
	Domains      []string
	DirectoryURL string
	// HTTPClient talks to the CA. Production passes netx.NewACMEClient();
	// tests pass a loopback client that trusts the fake CA. It is never read
	// from configuration.
	HTTPClient *http.Client
	// OnIssued receives the verified certificate after it is stored, so the
	// listeners' holders can serve it.
	OnIssued func(*tls.Certificate) error
	Logger   *slog.Logger
	// Now, Settle, IssueTimeout, LeaderPoll and Rand default to the clock, 2s,
	// 5m, 1s and math/rand/v2; tests replace them.
	Now          func() time.Time
	Settle       time.Duration
	IssueTimeout time.Duration
	LeaderPoll   time.Duration
	Rand         func() float64
}

// Manager issues and renews the certificate. Only the leader's agent runs it.
type Manager struct {
	store Store
	o     Options
	log   *slog.Logger

	// Backoff state. Run is the only writer; the accessors are for tests. It
	// is mirrored under StateKey so a restart or a new leader keeps it.
	mu       sync.Mutex
	failures int
	retryAt  time.Time
	// loaded is true while this Manager is the leader and has read the
	// persisted state and swept stale challenges. Run's goroutine only.
	loaded bool
}

// New returns a Manager. Domains are lower-cased and sorted.
func New(s Store, o Options) *Manager {
	o.Domains = SortedCopy(o.Domains)
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Settle < 0 {
		o.Settle = 0
	}
	if o.IssueTimeout <= 0 {
		o.IssueTimeout = defaultIssueTimeout
	}
	if o.LeaderPoll <= 0 {
		o.LeaderPoll = time.Second
	}
	if o.Rand == nil {
		o.Rand = mrand.Float64
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Manager{store: s, o: o, log: o.Logger}
}

// DefaultSettle is the settle pause production uses.
const DefaultSettle = defaultSettle

// Failures returns the number of consecutive failed attempts.
func (m *Manager) Failures() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failures
}

// RetryAt returns the earliest time of the next attempt after a failure.
func (m *Manager) RetryAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retryAt
}

// Run checks on every tick until ctx ends. The first check happens after
// first. A node that is not the leader does nothing: it reads nothing and
// calls nothing.
func (m *Manager) Run(ctx context.Context, isLeader func() bool, first, tick time.Duration) {
	if tick <= 0 {
		tick = defaultTick
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		m.step(ctx, isLeader)
		// Up to ten percent jitter on the tick.
		timer.Reset(tick + time.Duration(float64(tick)*0.1*m.o.Rand()))
	}
}

// step is one tick: leader check, backoff check, then Check. Losing
// leadership cancels the issuance in flight.
func (m *Manager) step(ctx context.Context, isLeader func() bool) {
	if !isLeader() {
		m.loaded = false
		return
	}
	if !m.loaded {
		m.loadState()
		m.sweepChallenges()
		m.loaded = true
	}
	if now := m.o.Now(); now.Before(m.RetryAt()) {
		return
	}
	ictx, cancel := m.leaderContext(ctx, isLeader)
	issued, err := m.Check(ictx)
	abandoned := ictx.Err() != nil
	cancel()
	if err != nil {
		if abandoned {
			// Shutdown or lost leadership, not a CA failure: no backoff.
			m.log.Info("acme: issuance abandoned", "reason", "leadership lost or shutting down")
			return
		}
		m.mu.Lock()
		n := m.failures + 1
		m.mu.Unlock()
		wait := Backoff(n, m.o.Rand())
		retryAt := m.o.Now().Add(wait)
		// Log and persist before the counters are published, so whoever
		// observes the new count also finds the line and the state.
		m.log.Error("acme: issuance failed, will retry",
			append(errAttrs(err), "failures", n, "retry_in", wait.Round(time.Second).String())...)
		if perr := m.store.Put(StateKey, encodeState(n, retryAt)); perr != nil {
			m.log.Warn("acme: saving the retry state failed", "error", truncate(perr.Error()))
		}
		m.mu.Lock()
		m.failures, m.retryAt = n, retryAt
		m.mu.Unlock()
		return
	}
	if issued {
		m.sweepChallenges()
	}
	m.mu.Lock()
	had := m.failures > 0
	m.failures, m.retryAt = 0, time.Time{}
	m.mu.Unlock()
	if had {
		if err := m.store.Delete(StateKey); err != nil {
			m.log.Warn("acme: clearing the retry state failed", "error", truncate(err.Error()))
		}
	}
}

// leaderContext derives a context that ends when ctx does or when isLeader
// turns false, polled every LeaderPoll.
func (m *Manager) leaderContext(ctx context.Context, isLeader func() bool) (context.Context, context.CancelFunc) {
	ictx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(m.o.LeaderPoll)
		defer t.Stop()
		for {
			select {
			case <-ictx.Done():
				return
			case <-t.C:
				if !isLeader() {
					cancel()
					return
				}
			}
		}
	}()
	return ictx, cancel
}

// loadState restores the backoff a previous run or leader persisted, so a
// restart loop or a leadership change cannot retry sooner than the floor.
func (m *Manager) loadState() {
	raw, err := m.store.Get(StateKey)
	if err != nil || raw == nil {
		return
	}
	n, retryAt, ok := decodeState(raw)
	if !ok {
		return
	}
	m.mu.Lock()
	m.failures, m.retryAt = n, retryAt
	m.mu.Unlock()
	m.log.Info("acme: restored the retry state", "failures", n, "retry_at", retryAt.UTC())
}

// sweepChallenges deletes every challenge entry. Only the leader's manager
// runs it, never during an issuance (step is sequential), so nothing live is
// removed; it clears what a failed delete or a crash left behind.
func (m *Manager) sweepChallenges() {
	keys, err := m.store.List(challengePrefix)
	if err != nil {
		m.log.Warn("acme: listing stale challenges failed", "error", truncate(err.Error()))
		return
	}
	for _, k := range keys {
		if err := m.store.Delete(k); err != nil {
			m.log.Warn("acme: deleting a stale challenge failed", "error", truncate(err.Error()))
		}
	}
}

// Check issues a certificate when Due says so. It reports whether it did.
func (m *Manager) Check(ctx context.Context) (bool, error) {
	rec, _, err := LoadCertificate(m.store)
	reason := Due(rec, m.o.Domains, m.o.DirectoryURL, m.o.Now())
	if err != nil && (errors.Is(err, ErrBadRecord) || errors.Is(err, ErrKeyMatch)) {
		reason = ReasonUnreadable
	} else if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	if reason == ReasonNone {
		return false, nil
	}
	m.log.Info("acme: issuing certificate", "reason", string(reason), "domains", m.o.Domains)
	ctx, cancel := context.WithTimeout(ctx, m.o.IssueTimeout)
	defer cancel()
	if err := m.Issue(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// stepError names the failed step. The wrapped error keeps its type, so
// errAttrs can read an *acme.Error, but Error() carries no CA text.
type stepError struct {
	step string
	err  error
}

func (e *stepError) Error() string { return "acme: " + e.step + " failed" }
func (e *stepError) Unwrap() error { return e.err }

func fail(step string, err error) error { return &stepError{step: step, err: err} }

// errAttrs turns an error into log attributes: the step, and for a CA problem
// document its type, status and detail truncated. Nothing else from the CA is
// logged.
func errAttrs(err error) []any {
	var attrs []any
	var se *stepError
	if errors.As(err, &se) {
		attrs = append(attrs, "step", se.step)
	}
	var ae *acme.Error
	if errors.As(err, &ae) {
		attrs = append(attrs, "problem_type", ae.ProblemType, "status", ae.StatusCode, "detail", truncate(ae.Detail))
	}
	var az *acme.AuthorizationError
	if errors.As(err, &az) {
		attrs = append(attrs, "identifier", az.Identifier)
		for _, e := range az.Errors {
			var inner *acme.Error
			if errors.As(e, &inner) {
				attrs = append(attrs, "problem_type", inner.ProblemType, "detail", truncate(inner.Detail))
				break
			}
		}
	}
	var oe *acme.OrderError
	if errors.As(err, &oe) {
		attrs = append(attrs, "order_status", oe.Status)
	}
	if se != nil && se.err != nil && ae == nil && az == nil && oe == nil {
		// Our own errors and transport errors: the text carries no key
		// material (it is built from fixed phrases and URLs).
		attrs = append(attrs, "error", truncate(se.err.Error()))
	}
	if len(attrs) == 0 {
		attrs = append(attrs, "error", truncate(err.Error()))
	}
	return attrs
}

func truncate(s string) string {
	if len(s) > maxDetail {
		return s[:maxDetail] + "..."
	}
	return s
}

// Issue runs one full issuance: account, order, challenges, finalise, verify,
// store, serve, clean up.
func (m *Manager) Issue(ctx context.Context) error {
	client, err := m.account(ctx)
	if err != nil {
		return err
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(m.o.Domains...))
	if err != nil {
		return fail("order", err)
	}

	var published []string
	defer func() { m.cleanChallenges(published) }()

	var pending []*acme.Challenge
	var waits []string
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return fail("authorization", err)
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		domain := authz.Identifier.Value
		if !slices.Contains(m.o.Domains, domain) {
			return fail("authorization", errors.New("CA asked to validate a name that was not requested"))
		}
		var chal *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "tls-alpn-01" {
				chal = c
				break
			}
		}
		if chal == nil {
			return fail("challenge", errors.New("the CA did not offer tls-alpn-01"))
		}
		cert, err := client.TLSALPN01ChallengeCert(chal.Token, domain)
		if err != nil {
			return fail("challenge", errors.New("building the challenge certificate"))
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
		if err != nil {
			return fail("challenge", errors.New("encoding the challenge key"))
		}
		raw, err := proto.Marshal(&bongov1.AcmeChallenge{CertDer: cert.Certificate[0], KeyDer: keyDER})
		if err != nil {
			return fail("challenge", errors.New("encoding the challenge"))
		}
		// Registered before the Put so a half-applied write is cleaned up.
		published = append(published, domain)
		if err := m.store.Put(ChallengeKey(domain), raw); err != nil {
			return fail("publish challenge", err)
		}
		pending = append(pending, chal)
		waits = append(waits, authzURL)
	}

	if len(pending) > 0 {
		if err := sleep(ctx, m.o.Settle); err != nil {
			return fail("publish challenge", err)
		}
		for _, c := range pending {
			if _, err := client.Accept(ctx, c); err != nil {
				return fail("accept challenge", err)
			}
		}
		for _, u := range waits {
			if _, err := client.WaitAuthorization(ctx, u); err != nil {
				return fail("validation", err)
			}
		}
	}

	if _, err := client.WaitOrder(ctx, order.URI); err != nil {
		return fail("order", err)
	}

	key, keyDER, err := newKey()
	if err != nil {
		return fail("key", errors.New("generating the certificate key"))
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  csrSubject(m.o.Domains[0]),
		DNSNames: m.o.Domains,
	}, key)
	if err != nil {
		return fail("csr", errors.New("building the request"))
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fail("finalise", err)
	}
	if err := m.verify(chain, key); err != nil {
		return fail("verify", err)
	}

	raw, err := EncodeCertificate(chain, keyDER, m.o.Domains, m.o.DirectoryURL, m.o.Now())
	if err != nil {
		return fail("store", errors.New("encoding the certificate"))
	}
	if err := m.store.Put(CertKey, raw); err != nil {
		return fail("store", err)
	}
	rec, err := DecodeCertificate(raw)
	if err != nil {
		return fail("store", err)
	}
	cert, err := TLSCertificate(rec)
	if err != nil {
		return fail("store", err)
	}
	if m.o.OnIssued != nil {
		if err := m.o.OnIssued(cert); err != nil {
			return fail("serve", err)
		}
	}
	m.log.Info("acme: certificate issued", "domains", m.o.Domains,
		"serial", cert.Leaf.SerialNumber.Text(16), "not_after", cert.Leaf.NotAfter)
	return nil
}

func csrSubject(first string) pkix.Name {
	if len(first) <= 64 {
		return pkix.Name{CommonName: first}
	}
	return pkix.Name{}
}

// verify checks the issued chain before it is stored: it parses, the leaf's
// names are exactly the requested set, the key is ours and it is not expired.
func (m *Manager) verify(chain [][]byte, key *ecdsa.PrivateKey) error {
	if len(chain) == 0 {
		return errors.New("the CA returned no certificate")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return errors.New("the certificate does not parse")
	}
	if !slices.Equal(SortedCopy(leaf.DNSNames), m.o.Domains) {
		return errors.New("the certificate names differ from the request")
	}
	if !key.PublicKey.Equal(leaf.PublicKey) {
		return errors.New("the certificate is for a different key")
	}
	if !m.o.Now().Before(leaf.NotAfter) {
		return errors.New("the certificate is already expired")
	}
	return nil
}

// cleanChallenges deletes the challenge entries, best effort. It takes no
// context: each delete is a Raft command with its own timeout, so a cancelled
// issuance still cleans up, and sweepChallenges catches what a failure leaves.
func (m *Manager) cleanChallenges(domains []string) {
	for _, d := range domains {
		if err := m.store.Delete(ChallengeKey(d)); err != nil {
			m.log.Warn("acme: deleting challenge failed", "domain", d, "error", truncate(err.Error()))
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// account returns a client bound to the stored account for the directory,
// registering one when there is none. The key is stored before Register, so a
// retry after a failure reuses it.
func (m *Manager) account(ctx context.Context) (*acme.Client, error) {
	key := AccountKey(m.o.DirectoryURL)
	var rec bongov1.AcmeAccount
	raw, err := m.store.Get(key)
	if err != nil {
		return nil, fail("account", err)
	}
	if raw != nil {
		if err := proto.Unmarshal(raw, &rec); err != nil {
			return nil, fail("account", ErrBadRecord)
		}
	}
	var signer crypto.Signer
	if len(rec.GetKeyDer()) > 0 {
		k, err := x509.ParsePKCS8PrivateKey(rec.GetKeyDer())
		s, ok := k.(crypto.Signer)
		if err != nil || !ok {
			return nil, fail("account", ErrBadRecord)
		}
		signer = s
	} else {
		k, der, err := newKey()
		if err != nil {
			return nil, fail("account", errors.New("generating the account key"))
		}
		signer = k
		rec = bongov1.AcmeAccount{KeyDer: der, DirectoryUrl: m.o.DirectoryURL}
		if err := m.putAccount(key, &rec); err != nil {
			return nil, err
		}
	}

	client := &acme.Client{
		Key:          signer,
		DirectoryURL: m.o.DirectoryURL,
		HTTPClient:   m.o.HTTPClient,
		UserAgent:    "bongo-acme",
	}
	if rec.GetUri() != "" {
		client.KID = acme.KeyID(rec.GetUri())
		return client, nil
	}

	acct := &acme.Account{}
	if m.o.Email != "" {
		acct.Contact = []string{"mailto:" + m.o.Email}
	}
	got, err := client.Register(ctx, acct, acme.AcceptTOS)
	if errors.Is(err, acme.ErrAccountAlreadyExists) {
		got, err = client.GetReg(ctx, "")
	}
	if err != nil {
		return nil, fail("register", err)
	}
	rec.Uri = got.URI
	if err := m.putAccount(key, &rec); err != nil {
		return nil, err
	}
	client.KID = acme.KeyID(got.URI)
	return client, nil
}

func (m *Manager) putAccount(key string, rec *bongov1.AcmeAccount) error {
	raw, err := proto.Marshal(rec)
	if err != nil {
		return fail("account", errors.New("encoding the account"))
	}
	if err := m.store.Put(key, raw); err != nil {
		return fail("account", err)
	}
	return nil
}

// decodeChallenge parses a stored challenge record.
func decodeChallenge(raw []byte) (*bongov1.AcmeChallenge, error) {
	var rec bongov1.AcmeChallenge
	if err := proto.Unmarshal(raw, &rec); err != nil {
		return nil, ErrBadRecord
	}
	return &rec, nil
}

// challengeCertificate rebuilds a tls.Certificate from the stored DER pair.
func challengeCertificate(certDER, keyDER []byte) (*tls.Certificate, error) {
	if len(certDER) == 0 || len(keyDER) == 0 {
		return nil, ErrBadRecord
	}
	key, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, ErrBadRecord
	}
	return &tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key}, nil
}
