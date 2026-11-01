package handlers

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// keyMarkers returns strings that must never appear in logs, DNS views or
// redirects: the PEM armour and a body line from the middle of the key (which
// encodes the private exponent and primes, unlike the shared PKCS8 prefix).
func keyMarkers(t *testing.T, pemKey string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(pemKey), "\n")
	if len(lines) < 6 {
		t.Fatalf("PEM too short: %d lines", len(lines))
	}
	mid := strings.TrimSpace(lines[len(lines)/2])
	if len(mid) < 32 {
		t.Fatalf("middle PEM line too short: %q", mid)
	}
	return []string{"-----BEGIN", "PRIVATE KEY", mid}
}

func assertNoKeyMaterial(t *testing.T, what, got string, markers []string) {
	t.Helper()
	for _, m := range markers {
		if strings.Contains(got, m) {
			t.Errorf("%s contains private key material %q", what, m)
		}
	}
}

func TestDKIMStartupLoad_DoesNotLogKey(t *testing.T) {
	h, _, ps, _, _ := newDKIMTestHandler(t)
	h.logger = discardLogger()
	if w := generateDKIMRequest(t, h, "leak.example.com"); w.Code != http.StatusSeeOther {
		t.Fatalf("generate status = %d", w.Code)
	}
	rec := loadStoredRecord(t, ps, "leak.example.com")
	markers := keyMarkers(t, rec.DkimPrivateKey)

	logger, capture := captureLogger()
	reg := mail.NewDKIMRegistry()
	LoadStoredDKIMKeys(ps, reg, logger)

	if reg.Get("leak.example.com") == nil {
		t.Fatal("signer not registered; log assertion would be vacuous")
	}
	if !strings.Contains(capture.String(), "registered signer") {
		t.Fatalf("expected startup log line, got %q", capture.String())
	}
	assertNoKeyMaterial(t, "startup load log", capture.String(), markers)
}

func TestDKIMTXTRecord_NoPrivateKeyMaterial(t *testing.T) {
	h, _, ps, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, h, "txt.example.com")
	rec := loadStoredRecord(t, ps, "txt.example.com")
	markers := keyMarkers(t, rec.DkimPrivateKey)

	txt := mustDKIMTXTRecord(t, "txt.example.com", rec.DkimSelector, rec.DkimPrivateKey)
	if !strings.Contains(txt, "p=") {
		t.Fatalf("positive control: TXT lacks p=: %q", txt)
	}
	assertNoKeyMaterial(t, "dkimTXTRecord", txt, markers)
}

func TestDomainDetailRender_NoPrivateKeyMaterial(t *testing.T) {
	h, _, ps, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, h, "render.example.com")
	rec := loadStoredRecord(t, ps, "render.example.com")
	markers := keyMarkers(t, rec.DkimPrivateKey)

	di := templates.DomainInfo{Name: "render.example.com", Enabled: true}
	h.enrichDomainDKIM(&di)
	if di.DkimTXTRecord == "" {
		t.Fatal("enrichDomainDKIM produced no TXT record")
	}
	di.DNSGuide, di.DKIMPending = buildDNSGuide(di.Name, "mail.example.com", false, "", "", di.DkimSelector, di.DkimTXTRecord)

	var buf bytes.Buffer
	if err := templates.AdminDomainDetail(di, "admin@example.com", "", true).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "p=") {
		t.Fatal("positive control: rendered page lacks the public TXT record")
	}
	assertNoKeyMaterial(t, "domain detail page", body, markers)
}

func TestGenerateDKIMKey_ResponseNoKeyMaterial(t *testing.T) {
	h, _, ps, _, _ := newDKIMTestHandler(t)
	w := generateDKIMRequest(t, h, "example.com")
	rec := loadStoredRecord(t, ps, "example.com")
	markers := keyMarkers(t, rec.DkimPrivateKey)

	loc := w.Header().Get("Location")
	if loc != "/admin/domains/example.com?flash=DKIM+key+generated" {
		t.Fatalf("Location = %q, want the domain page with flash", loc)
	}
	assertNoKeyMaterial(t, "redirect Location", loc, markers)
	for k, vs := range w.Header() {
		for _, v := range vs {
			assertNoKeyMaterial(t, "header "+k, v, markers)
		}
	}
	assertNoKeyMaterial(t, "response body", w.Body.String(), markers)
}

// verifyWithStoredKey signs a message with the registry signer for domain and
// verifies it against the public key derived from pemKey.
func verifyWithStoredKey(t *testing.T, reg *mail.DKIMRegistry, domain, pemKey string) error {
	t.Helper()
	signer := reg.Get(domain)
	if signer == nil {
		t.Fatalf("no signer for %s", domain)
	}
	signed, err := signer.Sign(buildTestMessage(domain))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	txt := mustDKIMTXTRecord(t, domain, "mail", pemKey)
	val := txt[strings.Index(txt, "\"")+1 : strings.LastIndex(txt, "\"")]
	_, err = dkim.VerifyWithOptions(bytes.NewReader(signed), &dkim.VerifyOptions{
		LookupTXT: func(string) ([]string, error) { return []string{val}, nil },
	})
	return err
}

func TestGenerateDKIMKey_MixedCaseRotatesCanonicalKey(t *testing.T) {
	h, _, ps, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, h, "example.com")
	pemA := loadStoredRecord(t, ps, "example.com").DkimPrivateKey

	w := generateDKIMRequest(t, h, "Example.COM")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if _, err := ps.Get([]byte("/system/domain/Example.COM/dkim")); err == nil {
		t.Error("mixed-case DKIM key was created as a separate record")
	}
	pemB := loadStoredRecord(t, ps, "example.com").DkimPrivateKey
	if pemB == pemA {
		t.Error("canonical key was not rotated")
	}

	iter, err := ps.PrefixScan([]byte("/system/domain/"))
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	n := 0
	for iter.Next() {
		if strings.EqualFold(string(iter.Key()), "/system/domain/example.com/dkim") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("found %d case-variants of the DKIM key, want 1", n)
	}

	reg := mail.NewDKIMRegistry()
	LoadStoredDKIMKeys(ps, reg, discardLogger())
	if err := verifyWithStoredKey(t, reg, "example.com", pemB); err != nil {
		t.Errorf("signer after restart does not match rotated key: %v", err)
	}
}

func TestGenerateDKIMKey_RemovesLegacyMixedCaseKey(t *testing.T) {
	h, applier, ps, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, h, "legacy.example.com")
	legacy := loadStoredRecord(t, ps, "legacy.example.com")
	data, _ := proto.Marshal(legacy)
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/Legacy.Example.com/dkim"), Value: data,
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	generateDKIMRequest(t, h, "LEGACY.example.com")
	if _, err := ps.Get([]byte("/system/domain/Legacy.Example.com/dkim")); err == nil {
		t.Error("stale mixed-case key survived rotation")
	}
}

// Y.Example.com sorts before y.example.com, so without the sort-order-free rule
// the mixed-case record would be registered first and the canonical one would
// merely overwrite it. This proves only that the canonical key is the one
// registered; TestDKIMStartupLoad_DisabledCanonicalSuppressesMixedCase proves
// the rule itself.
func TestDKIMStartupLoad_CanonicalKeyIsRegistered(t *testing.T) {
	h, applier, ps, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, h, "y.example.com")
	recY := loadStoredRecord(t, ps, "y.example.com")

	// Seed a different key X under a mixed-case key.
	hx, _, psx, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, hx, "x.example.com")
	recX := loadStoredRecord(t, psx, "x.example.com")
	data, _ := proto.Marshal(recX)
	if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/Y.Example.com/dkim"), Value: data}, time.Second); err != nil {
		t.Fatal(err)
	}
	reg := mail.NewDKIMRegistry()
	LoadStoredDKIMKeys(ps, reg, discardLogger())
	if err := verifyWithStoredKey(t, reg, "y.example.com", recY.DkimPrivateKey); err != nil {
		t.Errorf("canonical key did not win: %v", err)
	}
}

// A canonical record that exists but has DKIM disabled still takes precedence
// over an enabled legacy mixed-case record: the mixed-case one must not be
// registered, because the operator's latest decision lives on the canonical key.
func TestDKIMStartupLoad_DisabledCanonicalSuppressesMixedCase(t *testing.T) {
	h, applier, ps, _, _ := newDKIMTestHandler(t)
	generateDKIMRequest(t, h, "z.example.com")
	rec := loadStoredRecord(t, ps, "z.example.com")

	enabled, err := proto.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	rec.DkimEnabled = false
	disabled, err := proto.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for key, val := range map[string][]byte{
		"/system/domain/z.example.com/dkim": disabled,
		"/system/domain/Z.Example.com/dkim": enabled,
	} {
		if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(key), Value: val}, time.Second); err != nil {
			t.Fatal(err)
		}
	}

	reg := mail.NewDKIMRegistry()
	LoadStoredDKIMKeys(ps, reg, discardLogger())
	if got := reg.Get("z.example.com"); got != nil {
		t.Errorf("mixed-case record was registered despite a disabled canonical one")
	}
}

// A legacy domain stored as /system/domain/Example.COM must still show its
// rotated key: rotation moves the record to the lower-case key and deletes the
// mixed-case one, so only the lower-case read in enrichDomainDKIM finds it.
// The redirect must keep the as-typed name, because loadDomain looks the
// domain up case-sensitively.
func TestGenerateDKIMKey_LegacyMixedCaseDomainStillRenders(t *testing.T) {
	h, applier, ps, _, _ := newDKIMTestHandler(t)
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/Example.COM"), Value: []byte("enabled"),
	}, time.Second); err != nil {
		t.Fatal(err)
	}

	w := generateDKIMRequest(t, h, "Example.COM")
	if loc := w.Header().Get("Location"); loc != "/admin/domains/Example.COM?flash=DKIM+key+generated" {
		t.Errorf("Location = %q, want the as-typed domain page", loc)
	}
	if _, err := ps.Get([]byte("/system/domain/Example.COM/dkim")); err == nil {
		t.Fatal("precondition: mixed-case DKIM key should not exist after rotation")
	}

	rec := loadStoredRecord(t, ps, "example.com")
	di := templates.DomainInfo{Name: "Example.COM", Enabled: true}
	h.enrichDomainDKIM(&di)
	if di.DkimTXTRecord == "" {
		t.Fatal("legacy mixed-case domain page shows no DKIM record after rotation")
	}
	want := mustDKIMTXTRecord(t, "Example.COM", rec.DkimSelector, rec.DkimPrivateKey)
	if di.DkimTXTRecord != want {
		t.Errorf("DkimTXTRecord does not match the rotated key's public record")
	}
}
