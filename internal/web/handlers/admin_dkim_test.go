package handlers

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// captureWriter records log output so we can assert the private key never leaks.
type captureWriter struct {
	buf bytes.Buffer
}

func (cw *captureWriter) Write(p []byte) (int, error) {
	return cw.buf.Write(p)
}

func (cw *captureWriter) String() string {
	return cw.buf.String()
}

func captureLogger() (*slog.Logger, *captureWriter) {
	cw := &captureWriter{}
	return slog.New(slog.NewTextHandler(cw, &slog.HandlerOptions{Level: slog.LevelDebug})), cw
}

// newDKIMTestHandler creates a test AdminHandler with a capture logger.
func newDKIMTestHandler(t *testing.T) (*AdminHandler, *directApplierTags, *store.PebbleStore, *mail.DKIMRegistry, *captureWriter) {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	reg := mail.NewDKIMRegistry()
	logger, capture := captureLogger()

	h := NewAdminHandler(ps, nil, nil, applier, logger)
	h.SetDKIMRegistry(reg)
	return h, applier, ps, reg, capture
}

// generateDKIMRequest performs POST /admin/domains/{domain}/dkim/generate.
// Uses a global-admin scope so the test is not affected by tenant isolation checks.
func generateDKIMRequest(t *testing.T, h *AdminHandler, domain string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/domains/%s/dkim/generate", domain), nil)
	// Apply user context first, then chi params on top (order matters: each
	// r.WithContext call creates a new request; the last call wins).
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, middleware.AdminScope{Global: true})
	r = r.WithContext(ctx)
	r = withChiParam(r, "domain", domain)
	w := httptest.NewRecorder()
	h.GenerateDKIMKey(w, r)
	return w
}

// TestGenerateDKIMKey_StoresAndRegisters verifies that generating a DKIM key:
//   - stores a DomainRecord proto in Pebble under /system/domain/{domain}/dkim
//   - adds the signer to the registry
//   - the signer's signature contains d=<domain>
//   - the private key PEM is never written to the log
func TestGenerateDKIMKey_StoresAndRegisters(t *testing.T) {
	h, _, ps, reg, logOutput := newDKIMTestHandler(t)
	domain := "example.com"

	w := generateDKIMRequest(t, h, domain)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GenerateDKIMKey status = %d, want %d; body = %s", w.Code, http.StatusSeeOther, w.Body.String())
	}

	// The DomainRecord must be stored in Pebble.
	key := []byte(fmt.Sprintf("/system/domain/%s/dkim", domain))
	data, err := ps.Get(key)
	if err != nil {
		t.Fatalf("DomainRecord not found in Pebble: %v", err)
	}
	var rec bongov1.DomainRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal DomainRecord: %v", err)
	}
	if !rec.DkimEnabled {
		t.Error("DomainRecord.DkimEnabled must be true after generation")
	}
	if rec.DkimSelector == "" {
		t.Error("DomainRecord.DkimSelector must not be empty")
	}
	if rec.DkimPrivateKey == "" {
		t.Error("DomainRecord.DkimPrivateKey must not be empty")
	}

	// The registry must have a signer for the domain.
	signer := reg.Get(domain)
	if signer == nil {
		t.Fatal("registry.Get(domain) returned nil after GenerateDKIMKey")
	}

	// The signer must produce a signature with d=<domain>.
	rawMsg := buildTestMessage(domain)
	signed, err := signer.Sign(rawMsg)
	if err != nil {
		t.Fatalf("signer.Sign: %v", err)
	}
	if !bytes.Contains(signed, []byte("d="+domain)) {
		t.Errorf("signed message does not contain d=%s; got:\n%s", domain, string(signed[:200]))
	}

	// The private key PEM must never appear in the log output.
	logs := logOutput.String()
	if strings.Contains(logs, "-----BEGIN") {
		t.Errorf("log output contains PEM block (private key leak):\n%s", logs)
	}
	if rec.DkimPrivateKey != "" && strings.Contains(logs, rec.DkimPrivateKey[:20]) {
		t.Errorf("log output contains private key material")
	}
}

// TestGenerateDKIMKey_Regenerate verifies that re-generating a key rotates the
// public key — the stored TXT record changes.
func TestGenerateDKIMKey_Regenerate(t *testing.T) {
	h, _, ps, _, _ := newDKIMTestHandler(t)
	domain := "rotate.example.com"

	// First generation.
	w1 := generateDKIMRequest(t, h, domain)
	if w1.Code != http.StatusSeeOther {
		t.Fatalf("first GenerateDKIMKey status = %d", w1.Code)
	}
	rec1 := loadStoredRecord(t, ps, domain)
	txt1 := mustDKIMTXTRecord(t, domain, rec1.DkimSelector, rec1.DkimPrivateKey)

	// Second generation (rotation).
	w2 := generateDKIMRequest(t, h, domain)
	if w2.Code != http.StatusSeeOther {
		t.Fatalf("second GenerateDKIMKey status = %d", w2.Code)
	}
	rec2 := loadStoredRecord(t, ps, domain)
	txt2 := mustDKIMTXTRecord(t, domain, rec2.DkimSelector, rec2.DkimPrivateKey)

	// Public key / TXT record must have changed.
	if txt1 == txt2 {
		t.Errorf("TXT record did not change after re-generation: %s", txt1)
	}
	// Private key must have changed.
	if rec1.DkimPrivateKey == rec2.DkimPrivateKey {
		t.Error("private key did not change after re-generation")
	}
}

// TestDKIMTXTRecord_WellFormed verifies the TXT record is correctly formatted.
func TestDKIMTXTRecord_WellFormed(t *testing.T) {
	h, _, _, _, _ := newDKIMTestHandler(t)
	domain := "wellformed.example.com"

	w := generateDKIMRequest(t, h, domain)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GenerateDKIMKey status = %d", w.Code)
	}

	// Load the stored record and compute TXT.
	// The handler must redirect to the domain list; check via loadDomains.
	domains := h.loadDomains()
	var found bool
	for _, d := range domains {
		if d.Name == domain {
			found = true
			if d.DkimTXTRecord == "" {
				t.Error("DomainInfo.DkimTXTRecord is empty after generation")
			}
			// Must be of form: <selector>._domainkey.<domain> TXT "v=DKIM1; k=rsa; p=<base64>"
			if !strings.HasSuffix(d.DkimTXTRecord, fmt.Sprintf("._domainkey.%s TXT \"v=DKIM1; k=rsa; p=", domain)) &&
				!strings.Contains(d.DkimTXTRecord, "._domainkey."+domain) {
				t.Errorf("TXT record has wrong prefix: %s", d.DkimTXTRecord)
			}
			if !strings.HasSuffix(d.DkimTXTRecord, "\"") {
				t.Errorf("TXT record does not end with quote: %s", d.DkimTXTRecord)
			}
			// Extract base64 public key part.
			const prefix = "v=DKIM1; k=rsa; p="
			i := strings.Index(d.DkimTXTRecord, prefix)
			if i < 0 {
				t.Errorf("TXT record missing v=DKIM1 k=rsa p= part: %s", d.DkimTXTRecord)
				break
			}
			b64Part := d.DkimTXTRecord[i+len(prefix):]
			b64Part = strings.TrimSuffix(b64Part, "\"")
			if b64Part == "" {
				t.Error("base64 public key part is empty")
			}
			if _, err := base64.StdEncoding.DecodeString(b64Part); err != nil {
				t.Errorf("base64 public key part is not valid base64: %v", err)
			}
			break
		}
	}
	if !found {
		// Domain not in SQLite (no metadata manager in test); check via Pebble
		rec := loadStoredRecord(t, h.store, domain)
		txt := mustDKIMTXTRecord(t, domain, rec.DkimSelector, rec.DkimPrivateKey)
		wantPrefix := rec.DkimSelector + "._domainkey." + domain + " TXT \"v=DKIM1; k=rsa; p="
		if !strings.HasPrefix(txt, wantPrefix) {
			t.Errorf("TXT record = %q, want prefix %q", txt, wantPrefix)
		}
		if !strings.HasSuffix(txt, "\"") {
			t.Errorf("TXT record does not end with quote: %s", txt)
		}
		const prefix = "v=DKIM1; k=rsa; p="
		b64Part := strings.TrimSuffix(strings.TrimPrefix(txt[strings.Index(txt, prefix):], prefix), "\"")
		if _, err := base64.StdEncoding.DecodeString(b64Part); err != nil {
			t.Errorf("base64 public key part is not valid base64: %v", err)
		}
	}
}

// TestDKIMStartupLoad verifies that LoadStoredDKIMKeys registers stored keys
// into a fresh registry, so signing survives a restart.
func TestDKIMStartupLoad(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	applier := &directApplierTags{ps: ps}

	// Generate a key via the handler into Pebble.
	reg1 := mail.NewDKIMRegistry()
	h := NewAdminHandler(ps, nil, nil, applier, discardLogger())
	h.SetDKIMRegistry(reg1)
	domain := "startup.example.com"
	w := generateDKIMRequest(t, h, domain)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GenerateDKIMKey status = %d", w.Code)
	}

	// Simulate restart: fresh registry, load from Pebble.
	reg2 := mail.NewDKIMRegistry()
	LoadStoredDKIMKeys(ps, reg2, discardLogger())

	signer := reg2.Get(domain)
	if signer == nil {
		t.Fatal("after startup load, registry.Get(domain) returned nil")
	}

	// Signer must produce a valid signature with d=<domain>.
	rawMsg := buildTestMessage(domain)
	signed, err := signer.Sign(rawMsg)
	if err != nil {
		t.Fatalf("signer.Sign after startup load: %v", err)
	}
	if !bytes.Contains(signed, []byte("d="+domain)) {
		t.Errorf("signed message after startup load does not contain d=%s", domain)
	}
}

// TestDKIMStartupLoad_FiltersDKIMSuffix verifies that the startup load does NOT
// try to proto-unmarshal the plain /system/domain/{domain} enabled/disabled keys.
func TestDKIMStartupLoad_FiltersDKIMSuffix(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	applier := &directApplierTags{ps: ps}

	// Write plain domain enable key (as CreateDomain would).
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/system/domain/nodkim.example.com"),
		Value: []byte("enabled"),
	}, time.Second); err != nil {
		t.Fatalf("seed domain key: %v", err)
	}

	// Also write a DKIM key for another domain.
	reg1 := mail.NewDKIMRegistry()
	h := NewAdminHandler(ps, nil, nil, applier, discardLogger())
	h.SetDKIMRegistry(reg1)
	w := generateDKIMRequest(t, h, "hasdkim.example.com")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GenerateDKIMKey status = %d", w.Code)
	}

	// Startup load must not panic or error on the plain enabled key.
	reg2 := mail.NewDKIMRegistry()
	LoadStoredDKIMKeys(ps, reg2, discardLogger())

	// hasdkim.example.com should be registered.
	if reg2.Get("hasdkim.example.com") == nil {
		t.Error("hasdkim.example.com should be registered after startup load")
	}
	// nodkim.example.com should NOT be registered (no DKIM key).
	if reg2.Get("nodkim.example.com") != nil {
		t.Error("nodkim.example.com should not be registered (no /dkim key stored)")
	}
}

// --- Helpers ---

// buildTestMessage constructs a minimal RFC 5322 message from the given domain.
func buildTestMessage(domain string) []byte {
	return []byte(fmt.Sprintf(
		"From: test@%s\r\nTo: dest@other.com\r\nSubject: test\r\nDate: Mon, 01 Jan 2024 00:00:00 +0000\r\nMessage-ID: <test@%s>\r\n\r\nbody\r\n",
		domain, domain,
	))
}

// loadStoredRecord reads the DomainRecord from Pebble for the given domain.
func loadStoredRecord(t *testing.T, ps *store.PebbleStore, domain string) *bongov1.DomainRecord {
	t.Helper()
	key := []byte(fmt.Sprintf("/system/domain/%s/dkim", domain))
	data, err := ps.Get(key)
	if err != nil {
		t.Fatalf("loadStoredRecord(%s): %v", domain, err)
	}
	var rec bongov1.DomainRecord
	if err := proto.Unmarshal(data, &rec); err != nil {
		t.Fatalf("loadStoredRecord unmarshal(%s): %v", domain, err)
	}
	return &rec
}

// mustDKIMTXTRecord computes the DKIM TXT record from a PEM private key, failing
// the test if the key cannot be parsed. Delegates to the same dkimTXTRecord
// helper used by the handler so the test exercises the real derivation.
func mustDKIMTXTRecord(t *testing.T, domain, selector, pemKey string) string {
	t.Helper()
	txt := dkimTXTRecord(domain, selector, pemKey)
	if txt == "" {
		t.Fatalf("mustDKIMTXTRecord: dkimTXTRecord returned empty string for domain=%s selector=%s", domain, selector)
	}
	return txt
}
