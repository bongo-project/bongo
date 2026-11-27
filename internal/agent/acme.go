package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bongo-project/bongo/internal/acmecert"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// acmeFirstCheck is the delay before the leader's first issuance check, so a
// fresh process has a leader and a serving web listener first.
const acmeFirstCheck = 20 * time.Second

// acmeTestHooks lets tests replace the network edges and the timing of the
// ACME wiring. It is an unexported Agent field: nothing in the configuration
// reaches it, so production can only use the safe client and the real clock.
type acmeTestHooks struct {
	// client talks to the CA instead of netx.NewACMEClient().
	client *http.Client
	// settle, first, tick and syncEvery replace the production timing when set.
	settle, first, tick, syncEvery time.Duration
	// settleSet distinguishes a zero settle from an unset one.
	settleSet bool
}

// acmeStore adapts the agent to acmecert.Store: reads are Pebble point reads,
// writes are single Raft commands (a Put on a follower fails).
type acmeStore struct{ a *Agent }

func (s acmeStore) Get(key string) ([]byte, error) {
	v, err := s.a.Store.Get([]byte(key))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return v, err
}

func (s acmeStore) List(prefix string) ([]string, error) {
	iter, err := s.a.Store.PrefixScan([]byte(prefix))
	if err != nil {
		return nil, err
	}
	var keys []string
	for iter.Next() {
		keys = append(keys, string(iter.Key()))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (s acmeStore) Put(key string, value []byte) error {
	return s.a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(key), Value: value}, 5*time.Second)
}

func (s acmeStore) Delete(key string) error {
	return s.a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: []byte(key)}, 5*time.Second)
}

// acmeState is the certificate material shared by the listeners. There is one
// static holder per listener group, deliberately not registered with
// certReloader: they are not file-backed and its poll would never touch them.
type acmeState struct {
	store acmecert.Store
	// web, smtp and mail (IMAP and POP3) each hold the same certificate.
	web, smtp, mail *security.CertHolder
}

// set gives every holder the certificate.
func (st *acmeState) set(c *tls.Certificate) error {
	var errs []error
	for _, h := range []*security.CertHolder{st.web, st.smtp, st.mail} {
		if err := h.Set(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// serial returns the hex serial of the certificate the web holder serves.
func (st *acmeState) serial() string {
	if c := st.web.Current(); c != nil && c.Leaf != nil {
		return c.Leaf.SerialNumber.Text(16)
	}
	return ""
}

// acmeEnabled reports whether this node obtains its certificate over ACME.
func (a *Agent) acmeEnabled() bool {
	return a.Config.Web.TLS.ACMEEnabled() && a.Store != nil && a.Config.Server.Role != "agent"
}

// acme returns the ACME state, building it on first use, or nil when ACME is
// off or could not be set up (a.acmeErr then says why, and Run refuses to
// start).
func (a *Agent) acme() *acmeState {
	if !a.acmeEnabled() {
		return nil
	}
	a.acmeOnce.Do(func() {
		a.acmeSt, a.acmeErr = a.buildACMEState()
	})
	return a.acmeSt
}

// buildACMEState builds the holders. The initial certificate is the stored
// one when it parses, matches its key and has not expired; otherwise a
// self-signed one is served until the leader issues. It fails when no initial
// certificate can be produced, rather than serving through empty holders.
func (a *Agent) buildACMEState() (*acmeState, error) {
	st := &acmeState{store: acmeStore{a}}
	domains := a.Config.Web.TLS.ACMESettings().Domains

	var initial *tls.Certificate
	_, cert, err := acmecert.LoadCertificate(st.store)
	switch {
	case err != nil:
		a.logger.Warn("acme: stored certificate is unusable, serving a self-signed certificate until one is issued")
	case cert == nil:
		a.logger.Warn("acme: no certificate issued yet, serving a self-signed certificate that clients will NOT trust until one is issued")
	case !cert.Leaf.NotAfter.After(time.Now()):
		a.logger.Warn("acme: stored certificate has expired, serving a self-signed certificate until one is issued",
			"serial", cert.Leaf.SerialNumber.Text(16), "not_after", cert.Leaf.NotAfter)
	default:
		initial = cert
		a.logger.Info("acme: serving the stored certificate", "serial", cert.Leaf.SerialNumber.Text(16), "not_after", cert.Leaf.NotAfter)
	}
	if initial == nil {
		cfg, err := security.SelfSignedTLSConfig(domains)
		if err != nil {
			return nil, fmt.Errorf("acme: generating the fallback certificate: %w", err)
		}
		initial = &cfg.Certificates[0]
	}
	st.web = security.NewStaticCertHolder("web", initial)
	st.smtp = security.NewStaticCertHolder("smtp", initial)
	st.mail = security.NewStaticCertHolder("imap/pop3", initial)

	if a.Config.Cluster.TLS.CertFile != "" || a.Config.Cluster.TLS.KeyFile != "" {
		a.logger.Info("acme: IMAP and POP3 serve the ACME certificate, not cluster.tls; the cluster files still secure Raft and peer traffic")
	}
	return st, nil
}

// acmeWebTLS builds the web listener's TLS config for ACME, or nil when ACME
// is off. Browsers get the holder's certificate; a hello offering only
// acme-tls/1 is answered from the replicated challenge entry (a Pebble point
// read, never a write).
func (a *Agent) acmeWebTLS() *tls.Config {
	st := a.acme()
	if st == nil {
		return nil
	}
	settings := a.Config.Web.TLS.ACMESettings()
	if port := a.Config.Web.ListenPort(false); port != 443 {
		a.logger.Warn("acme: the web listener is not on port 443; TLS-ALPN-01 validation connects to port 443, so forward it to this port",
			"web_port", port)
	}
	// http.Server adds h2 and http/1.1 to the base config itself; only a
	// hello that offers acme-tls/1 alone is answered as a CA probe.
	return acmecert.TLSConfig(st.web.TLSConfig(), st.store, settings.Domains, st.web.GetCertificate)
}

// acmeHTTPClient is the client for the CA: the safe netx client unless a test
// hook replaced it.
func (a *Agent) acmeHTTPClient() *http.Client {
	if a.acmeTest != nil && a.acmeTest.client != nil {
		return a.acmeTest.client
	}
	return netx.NewACMEClient()
}

// startACME starts the leader-only issuer and the all-node sync loop. Both are
// bgWG workers and stop through acmeCancel (and ctx).
func (a *Agent) startACME(ctx context.Context) {
	st := a.acme()
	if st == nil || a.Raft == nil {
		return
	}
	settings := a.Config.Web.TLS.ACMESettings()
	hooks := a.acmeTest
	if hooks == nil {
		hooks = &acmeTestHooks{}
	}
	client := a.acmeHTTPClient()
	settle, first, tick, syncEvery := acmecert.DefaultSettle, acmeFirstCheck, time.Hour, certPollInterval
	if hooks.settleSet {
		settle = hooks.settle
	}
	if hooks.first > 0 {
		first = hooks.first
	}
	if hooks.tick > 0 {
		tick = hooks.tick
	}
	if hooks.syncEvery > 0 {
		syncEvery = hooks.syncEvery
	}

	mgr := acmecert.New(st.store, acmecert.Options{
		Email:        settings.Email,
		Domains:      settings.Domains,
		DirectoryURL: settings.DirectoryURL,
		HTTPClient:   client,
		OnIssued:     st.set,
		Logger:       a.logger,
		Settle:       settle,
	})

	acmeCtx, cancel := context.WithCancel(ctx)
	a.acmeCancel = cancel
	a.goWorker("acme", func() { mgr.Run(acmeCtx, a.IsLeader, first, tick) })
	a.goWorker("acme-sync", func() { a.runACMESync(acmeCtx, st, syncEvery) })
}

// runACMESync keeps this node's holders in step with the replicated
// certificate: followers pick up what the leader issued, and a restarted node
// catches a renewal it missed. Every node runs it; it only reads.
func (a *Agent) runACMESync(ctx context.Context, st *acmeState, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.syncACMEOnce(st)
		}
	}
}

// syncACMEOnce reads /system/acme/cert and calls Set on every holder when the
// leaf serial differs from the one served. It logs serial and not_after only.
func (a *Agent) syncACMEOnce(st *acmeState) {
	_, cert, err := acmecert.LoadCertificate(st.store)
	if err != nil || cert == nil {
		if err != nil {
			a.logger.Warn("acme: stored certificate could not be read, keeping the current one")
		}
		return
	}
	if cert.Leaf.SerialNumber.Text(16) == st.serial() {
		return
	}
	if err := st.set(cert); err != nil {
		a.logger.Warn("acme: replicated certificate refused, keeping the current one",
			"serial", cert.Leaf.SerialNumber.Text(16), "not_after", cert.Leaf.NotAfter)
		return
	}
	a.logger.Info("acme: now serving the replicated certificate",
		"serial", cert.Leaf.SerialNumber.Text(16), "not_after", cert.Leaf.NotAfter)
}

// acmeStatus feeds the admin dashboard.
type acmeStatus struct{ st *acmeState }

// ACMEExpiry returns the stored certificate's expiry; issued is false when
// none has been issued yet.
func (s acmeStatus) ACMEExpiry() (notAfter time.Time, issued bool, err error) {
	rec, _, lerr := acmecert.LoadCertificate(s.st.store)
	if rec == nil || lerr != nil {
		// A record that does not load (key mismatch, unparsable chain) is
		// not "issued": the dashboard warns.
		return time.Time{}, false, lerr
	}
	return time.Unix(rec.GetNotAfter(), 0).UTC(), true, nil
}

// acmeStatusReader returns the dashboard reader, or nil when ACME is off.
func (a *Agent) acmeStatusReader() handlers.ACMEStatusReader {
	st := a.acme()
	if st == nil {
		return nil
	}
	return acmeStatus{st: st}
}
