package mail

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeDeliverer captures whatever bytes GenerateAndDeliver hands to Deliver,
// so tests can assert on the exact wire bytes (signed vs unsigned) rather
// than re-deriving a signature locally.
type fakeDeliverer struct {
	from       string
	recipients []string
	data       []byte
}

func (f *fakeDeliverer) Deliver(from string, recipients []string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.from = from
	f.recipients = recipients
	f.data = data
	return nil
}

// TestResolveServerDomain verifies the domain resolution fallback chain:
// DKIM domain -> EffectiveTLDPlusOne(smtpHostname) -> smtpHostname -> os.Hostname() -> "localhost".
func TestResolveServerDomain(t *testing.T) {
	osHost, _ := os.Hostname()
	if osHost == "" {
		osHost = "localhost"
	}
	// ResolveServerDomain lowercases the OS hostname, so the expected
	// value must too — otherwise this test fails on hosts whose name
	// contains uppercase letters (e.g. macOS "Mac.local").
	osHost = strings.ToLower(osHost)

	tests := []struct {
		name         string
		dkimDomain   string
		smtpHostname string
		want         string
	}{
		{
			name:         "DKIM domain takes priority over SMTP hostname",
			dkimDomain:   "example.com",
			smtpHostname: "mail.example.org",
			want:         "example.com",
		},
		{
			name:         "registered domain derived from SMTP hostname",
			dkimDomain:   "",
			smtpHostname: "mail.example.com",
			want:         "example.com",
		},
		{
			name:         "bare SMTP hostname used when EffectiveTLDPlusOne fails",
			dkimDomain:   "",
			smtpHostname: "mailserver",
			want:         "mailserver",
		},
		{
			name:         "falls back to os.Hostname when both config values empty",
			dkimDomain:   "",
			smtpHostname: "",
			want:         osHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveServerDomain(tt.dkimDomain, tt.smtpHostname)
			if got != tt.want {
				t.Errorf("ResolveServerDomain(%q, %q) = %q, want %q",
					tt.dkimDomain, tt.smtpHostname, got, tt.want)
			}
		})
	}
}

// TestBuildBounceDomain checks that all server-generated headers use the configured
// domain rather than hardcoded literals such as @localhost or @bongo.
func TestBuildBounceDomain(t *testing.T) {
	tests := []struct {
		name   string
		domain string
	}{
		{name: "configured public domain", domain: "example.com"},
		{name: "subdomain", domain: "mail.example.org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &bongov1.OutboundMessage{
				SenderEmail: "alice@" + tt.domain,
			}
			out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 test", tt.domain, "")

			raw := string(out)

			// From header must use postmaster@<domain>.
			if !strings.Contains(raw, "postmaster@"+tt.domain) {
				t.Errorf("From header missing postmaster@%s:\n%s", tt.domain, raw)
			}

			// Message-ID must use @<domain>, not @localhost.
			foundMsgID := false
			for _, line := range strings.Split(raw, "\r\n") {
				if strings.HasPrefix(line, "Message-ID:") {
					foundMsgID = true
					if strings.Contains(line, "@localhost") {
						t.Errorf("Message-ID must not contain @localhost: %s", line)
					}
					if !strings.Contains(line, "@"+tt.domain) {
						t.Errorf("Message-ID should contain @%s: %s", tt.domain, line)
					}
				}
			}
			if !foundMsgID {
				t.Error("no Message-ID header found in bounce message")
			}

			// Reporting-MTA must use the configured domain.
			if !strings.Contains(raw, "Reporting-MTA: dns; "+tt.domain) {
				t.Errorf("Reporting-MTA should use %s, got:\n%s", tt.domain, raw)
			}

			// Envelope sender (MAILER-DAEMON) must also use the configured domain.
			// We verify via the From line (the envelope is not in the raw bytes but
			// the header From is the observable signal here).
			if strings.Contains(raw, "@localhost") {
				t.Errorf("bounce message must not contain @localhost anywhere:\n%s", raw)
			}
		})
	}
}

// TestBuildBounceHeaderInjection verifies that user-controlled values in the
// outbound message cannot inject extra header lines into the generated DSN.
// After sanitization the injected text may still appear inside a header value
// (e.g. "To: alice@example.comX-Inject: yes") but it must not appear as a
// standalone line (i.e. it must not start a new CRLF-terminated line).
func TestBuildBounceHeaderInjection(t *testing.T) {
	tests := []struct {
		name             string
		senderEmail      string
		failedRecipients []string
		reason           string
		messageGuid      string
		// forbiddenLines are header-field strings that must not appear as
		// complete standalone lines in the output (i.e. they must not be
		// preceded by a CRLF in an injection position).
		forbiddenLines []string
	}{
		{
			name:             "CRLF in senderEmail cannot inject header line",
			senderEmail:      "alice@example.com\r\nX-Inject: yes",
			failedRecipients: []string{"bob@external.com"},
			reason:           "550 user unknown",
			forbiddenLines:   []string{"X-Inject: yes"},
		},
		{
			name:             "CRLF in reason cannot inject header line",
			senderEmail:      "alice@example.com",
			failedRecipients: []string{"bob@external.com"},
			reason:           "550 ok\r\nX-Evil: injected",
			forbiddenLines:   []string{"X-Evil: injected"},
		},
		{
			name:             "CRLF in recipient cannot inject header line",
			senderEmail:      "alice@example.com",
			failedRecipients: []string{"bob@external.com\r\nX-Inject: rcpt"},
			reason:           "550 user unknown",
			forbiddenLines:   []string{"X-Inject: rcpt"},
		},
		{
			name:             "CRLF in MessageGuid cannot inject header line (echoed into In-Reply-To and References)",
			senderEmail:      "alice@example.com",
			failedRecipients: []string{"bob@external.com"},
			reason:           "550 user unknown",
			messageGuid:      "<orig@bongo>\r\nX-Inject: guid",
			forbiddenLines:   []string{"X-Inject: guid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &bongov1.OutboundMessage{
				SenderEmail: tt.senderEmail,
				MessageGuid: tt.messageGuid,
			}
			out := BuildBounceMessage(original, tt.failedRecipients, tt.reason, "test.local", "")

			// Split on CRLF and check that no line equals a forbidden header.
			lines := bytes.Split(out, []byte("\r\n"))
			for _, bad := range tt.forbiddenLines {
				for _, line := range lines {
					if bytes.Equal(line, []byte(bad)) {
						t.Errorf("line %q equals forbidden value %q — header injection detected\nfull output:\n%s",
							line, bad, out)
					}
				}
			}
		})
	}
}

func TestBuildBounce(t *testing.T) {
	original := &bongov1.OutboundMessage{
		Id:          "msg-001",
		SenderEmail: "alice@example.com",
		MessageGuid: "<orig-123@bongo>",
	}

	failedRecipients := []string{"bob@external.com", "carol@external.com"}
	reason := "550 User unknown"

	bounceMsg := BuildBounceMessage(original, failedRecipients, reason, "example.com", "")

	// Parse with enmime to verify RFC 5322 validity.
	env, err := enmime.ReadEnvelope(bytes.NewReader(bounceMsg))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}

	// Verify headers.
	if from := env.GetHeader("From"); !strings.Contains(from, "MAILER-DAEMON") {
		t.Errorf("From = %q, want MAILER-DAEMON", from)
	}
	if to := env.GetHeader("To"); to != "alice@example.com" {
		t.Errorf("To = %q, want alice@example.com", to)
	}
	if subj := env.GetHeader("Subject"); !strings.Contains(subj, "Undelivered") {
		t.Errorf("Subject = %q, want 'Undelivered' in it", subj)
	}
	if irt := env.GetHeader("In-Reply-To"); irt != "<orig-123@bongo>" {
		t.Errorf("In-Reply-To = %q, want <orig-123@bongo>", irt)
	}
	if refs := env.GetHeader("References"); refs != "<orig-123@bongo>" {
		t.Errorf("References = %q, want <orig-123@bongo>", refs)
	}
	if as := env.GetHeader("Auto-Submitted"); as != "auto-replied" {
		t.Errorf("Auto-Submitted = %q, want auto-replied", as)
	}

	// Verify body mentions failed recipients.
	body := env.Text
	if !strings.Contains(body, "bob@external.com") {
		t.Error("body should mention bob@external.com")
	}
	if !strings.Contains(body, "carol@external.com") {
		t.Error("body should mention carol@external.com")
	}
	if !strings.Contains(body, "550 User unknown") {
		t.Error("body should mention the reason")
	}
}

// TestGenerateAndDeliverSignsBounce verifies that a bounce for a domain with a
// registered DKIM signer is delivered with a valid DKIM-Signature header
// (d=<domain>, h= including from) whose signature cryptographically verifies
// against the signer's own public key. It uses a stubbed LookupTXT so the
// crypto path is exercised end-to-end with zero network access.
func TestGenerateAndDeliverSignsBounce(t *testing.T) {
	_, key := generateTestRSAKey(t)
	registry := NewDKIMRegistry()
	registry.Seed("example.test", NewDKIMSignerFromKey("test", "example.test", key))

	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, nil, slog.Default(), "example.test", registry)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	if deliverer.data == nil {
		t.Fatal("Deliver was not called")
	}
	raw := string(deliverer.data)
	if !strings.Contains(raw, "DKIM-Signature:") {
		t.Fatalf("delivered bytes missing DKIM-Signature header:\n%s", raw)
	}
	// Assert against the DKIM-Signature header itself, not the whole message:
	// a Contains check over `raw` would match "d=example.test" or "from"
	// occurring anywhere in the DSN body and pass vacuously.
	sigHeader := dkimSignatureHeader(t, raw)
	if !strings.Contains(sigHeader, "d=example.test") {
		t.Errorf("DKIM-Signature missing d=example.test, got:\n%s", sigHeader)
	}
	hTag := dkimTag(sigHeader, "h=")
	if hTag == "" {
		t.Fatalf("DKIM-Signature has no h= tag:\n%s", sigHeader)
	}
	if !strings.Contains(strings.ToLower(hTag), "from") {
		t.Errorf("DKIM-Signature h= must cover the From header, got h=%s", hTag)
	}

	// Verify the signature cryptographically with a stubbed LookupTXT instead
	// of a live DNS lookup, so the crypto path runs with zero network access.
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	stubTXT := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pubDER)
	lookupTXT := func(domain string) ([]string, error) {
		if strings.HasPrefix(domain, "test._domainkey.example.test") {
			return []string{stubTXT}, nil
		}
		return nil, nil
	}

	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(deliverer.data), &dkim.VerifyOptions{LookupTXT: lookupTXT})
	if err != nil {
		t.Fatalf("dkim.VerifyWithOptions: %v", err)
	}
	if len(verifications) != 1 {
		t.Fatalf("got %d verifications, want 1", len(verifications))
	}
	if verifications[0].Err != nil {
		t.Errorf("DKIM verification failed: %v", verifications[0].Err)
	}
	if verifications[0].Domain != "example.test" {
		t.Errorf("verified domain = %q, want example.test", verifications[0].Domain)
	}
}

// TestGenerateAndDeliverNilRegistry verifies that a nil DKIM registry does not
// panic and still delivers the bounce, unsigned, and that the no-signer
// warning still fires for a nil registry (the bg.dkim == nil short-circuit
// must not skip the warning, only the Get call).
func TestGenerateAndDeliverNilRegistry(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, nil, logger, "example.test", nil)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	if deliverer.data == nil {
		t.Fatal("Deliver was not called")
	}
	if strings.Contains(string(deliverer.data), "DKIM-Signature:") {
		t.Error("bounce should be unsigned with a nil registry")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected a warning-level log entry for a nil registry, got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "example.test") {
		t.Errorf("warning log should name the domain example.test, got:\n%s", logOutput)
	}
}

// TestGenerateAndDeliverUnregisteredDomainWarns verifies that when no signer
// is registered for the bounce domain, delivery still succeeds (unsigned) and
// a warning naming the domain is logged.
func TestGenerateAndDeliverUnregisteredDomainWarns(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	registry := NewDKIMRegistry() // empty: no signer for any domain
	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, nil, logger, "example.test", registry)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	if deliverer.data == nil {
		t.Fatal("Deliver was not called")
	}
	if strings.Contains(string(deliverer.data), "DKIM-Signature:") {
		t.Error("bounce should be unsigned when no signer is registered")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "example.test") {
		t.Errorf("warning log should name the domain example.test, got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected a warning-level log entry, got:\n%s", logOutput)
	}
}

// TestGenerateAndDeliverUsesNullEnvelopeSender verifies RFC 5321 s6.1: the
// DSN's envelope sender (the `from` argument to Deliver) must be the null
// sender "", not MAILER-DAEMON@<domain> — a non-null return path is a
// bounce-of-a-bounce hazard. The From: header, by contrast, must still
// identify MAILER-DAEMON@<domain>.
func TestGenerateAndDeliverUsesNullEnvelopeSender(t *testing.T) {
	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, nil, slog.Default(), "example.test", nil)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	if deliverer.from != "" {
		t.Errorf("envelope sender (Deliver's from argument) = %q, want empty (null sender)", deliverer.from)
	}
	if deliverer.data == nil {
		t.Fatal("Deliver was not called")
	}
	raw := string(deliverer.data)
	if !strings.Contains(raw, "From: MAILER-DAEMON <postmaster@example.test>") {
		t.Errorf("From header must still be MAILER-DAEMON <postmaster@example.test>, got:\n%s", raw)
	}
}

// TestGenerateAndDeliverWarnDomainMatchesSigner is the divergence test: it
// proves the no-signer warning names the domain that DKIMRegistry.Sign
// actually uses, by constructing bg.domain with a trailing dot
// ("example.test."). DKIMRegistry.Get only lowercases (dkim_registry.go),
// while FromRawMessageDomain -> FromHeaderDomain -> normalizeDomain also
// strips a trailing dot (dmarc.go). A guard that looked up Get(bg.domain) ==
// Get("example.test.") misses a signer seeded at "example.test" and produces a
// spurious warning even though Sign (which re-derives the domain from the From
// header) signs successfully; this test fails against that shape of guard.
//
// A trailing-dot domain is reachable in production: ResolveServerDomain
// returns the SMTP hostname verbatim when publicsuffix parsing fails, and a
// hostname can be configured with a trailing dot.
func TestGenerateAndDeliverWarnDomainMatchesSigner(t *testing.T) {
	_, key := generateTestRSAKey(t)
	registry := NewDKIMRegistry()
	registry.Seed("example.test", NewDKIMSignerFromKey("test", "example.test", key))

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	deliverer := &fakeDeliverer{}
	// bg.domain has a trailing dot; the registry is seeded without one.
	gen := NewBounceGenerator(deliverer, nil, logger, "example.test.", registry)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "level=WARN") {
		t.Errorf("no warning expected: the From-header domain (example.test) has a registered signer, got log:\n%s", logOutput)
	}

	if deliverer.data == nil {
		t.Fatal("Deliver was not called")
	}
	raw := string(deliverer.data)
	if !strings.Contains(raw, "DKIM-Signature:") {
		t.Fatalf("delivered bytes missing DKIM-Signature header:\n%s", raw)
	}
	sigHeader := dkimSignatureHeader(t, raw)
	if !strings.Contains(sigHeader, "d=example.test") {
		t.Errorf("DKIM-Signature missing d=example.test, got:\n%s", sigHeader)
	}
}

// TestGenerateAndDeliverWarnsWhenFromUnparseable verifies that when the built
// bounce message's From header cannot be parsed for a domain, the guard warns
// UNCONDITIONALLY rather than consulting bg.domain. DKIMRegistry.Sign resolves
// its signer with the same helper, so it fails identically and the message is
// guaranteed to go out unsigned; falling back to a bg.domain that happens to
// have a registered signer would suppress the warning on exactly the unsigned
// delivery it exists to report.
//
// bg.domain = "" makes buildBounce emit "From: MAILER-DAEMON <postmaster@>",
// whose domain component is empty, so FromRawMessageDomain errors.
func TestGenerateAndDeliverWarnsWhenFromUnparseable(t *testing.T) {
	_, key := generateTestRSAKey(t)
	registry := NewDKIMRegistry()
	// Seed a signer the fallback path WOULD have found, so a regression to
	// "fall back to bg.domain" (here "") is not what makes this pass. The
	// point is that no bg.domain value can suppress this warning.
	registry.Add("example.test", NewDKIMSignerFromKey("test", "example.test", key))

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, nil, logger, "", registry)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected a warning when the From domain is unparseable, got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "cannot determine From domain") {
		t.Errorf("expected the unparseable-From warning message, got:\n%s", logOutput)
	}
	// The delivery really is unsigned, which is what the warning reports.
	if strings.Contains(string(deliverer.data), "DKIM-Signature:") {
		t.Error("expected an unsigned bounce when the From domain is unparseable")
	}
}

// dkimSignatureHeader extracts the unfolded DKIM-Signature header value from a
// raw message. Assertions about the signature must run against this rather
// than the whole message: a substring check over the full text matches tag
// values that merely appear in the DSN body and passes vacuously.
func dkimSignatureHeader(t *testing.T, raw string) string {
	t.Helper()
	headers, _, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatalf("message has no header/body separator:\n%s", raw)
	}
	// Unfold continuation lines (leading space or tab) before searching.
	unfolded := strings.ReplaceAll(strings.ReplaceAll(headers, "\r\n\t", "\t"), "\r\n ", " ")
	for _, line := range strings.Split(unfolded, "\r\n") {
		if after, ok := strings.CutPrefix(line, "DKIM-Signature:"); ok {
			return strings.TrimSpace(after)
		}
	}
	t.Fatalf("no DKIM-Signature header found in:\n%s", headers)
	return ""
}

// dkimTag returns the value of a DKIM-Signature tag (e.g. "h=") from an
// unfolded signature header value, or "" when the tag is absent.
func dkimTag(sigHeader, tag string) string {
	for _, part := range strings.Split(sigHeader, ";") {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, tag); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// leafParts walks an enmime part tree and returns every leaf part (a part with
// no children), i.e. the actual MIME body parts rather than multipart
// containers. Tests should walk the tree this way rather than assuming
// env.Text alone reflects every part.
func leafParts(root *enmime.Part) []*enmime.Part {
	var out []*enmime.Part
	var walk func(p *enmime.Part)
	walk = func(p *enmime.Part) {
		if p == nil {
			return
		}
		if p.FirstChild == nil {
			out = append(out, p)
			return
		}
		for c := p.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// TestBuildBounceThreeParts verifies that given a non-empty original
// header block, the bounce has exactly three MIME parts (text/plain,
// message/delivery-status, text/rfc822-headers), and the first two declare an
// explicit Content-Transfer-Encoding.
func TestBuildBounceThreeParts(t *testing.T) {
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.com", MessageGuid: "<orig@bongo>"}
	originalHeaders := "From: alice@example.com\r\nTo: bob@external.com\r\nSubject: hi\r\n\r\n"

	out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 user unknown", "example.com", originalHeaders)

	env, err := enmime.ReadEnvelope(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}

	parts := leafParts(env.Root)
	if len(parts) != 3 {
		t.Fatalf("got %d MIME parts, want 3:\n%s", len(parts), out)
	}

	byType := make(map[string]*enmime.Part, 3)
	for _, p := range parts {
		byType[p.ContentType] = p
	}

	textPart, ok := byType["text/plain"]
	if !ok {
		t.Fatalf("missing text/plain part, got types %v", partTypes(parts))
	}
	if cte := textPart.Header.Get("Content-Transfer-Encoding"); cte != "quoted-printable" {
		t.Errorf("text/plain Content-Transfer-Encoding = %q, want quoted-printable", cte)
	}

	dsPart, ok := byType["message/delivery-status"]
	if !ok {
		t.Fatalf("missing message/delivery-status part, got types %v", partTypes(parts))
	}
	if cte := dsPart.Header.Get("Content-Transfer-Encoding"); cte != "7bit" {
		t.Errorf("message/delivery-status Content-Transfer-Encoding = %q, want 7bit", cte)
	}

	if _, ok := byType["text/rfc822-headers"]; !ok {
		t.Fatalf("missing text/rfc822-headers part, got types %v", partTypes(parts))
	}
}

func partTypes(parts []*enmime.Part) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.ContentType
	}
	return out
}

// TestBuildBounceOmitsThirdPartWhenNoHeaders verifies that an empty
// originalHeaders (blob unavailable) yields exactly two parts, not three.
func TestBuildBounceOmitsThirdPartWhenNoHeaders(t *testing.T) {
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.com"}
	out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 user unknown", "example.com", "")

	env, err := enmime.ReadEnvelope(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}
	parts := leafParts(env.Root)
	if len(parts) != 2 {
		t.Fatalf("got %d MIME parts, want 2 (no rfc822-headers part): %v", len(parts), partTypes(parts))
	}
	for _, p := range parts {
		if p.ContentType == "text/rfc822-headers" {
			t.Error("text/rfc822-headers part present despite empty originalHeaders")
		}
	}
}

// TestBuildBounceNonASCIIReasonRoundTrips verifies that a non-ASCII reason
// string round-trips through the human-readable part's declared
// charset=utf-8 / Content-Transfer-Encoding: quoted-printable.
func TestBuildBounceNonASCIIReasonRoundTrips(t *testing.T) {
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.com"}
	reason := "550 föremålet är inte tillgängligt éè中文"

	out := BuildBounceMessage(original, []string{"bob@external.com"}, reason, "example.com", "")

	env, err := enmime.ReadEnvelope(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}
	if !strings.Contains(env.Text, reason) {
		t.Errorf("decoded text/plain body does not contain the non-ASCII reason verbatim.\nwant substring: %q\ngot body: %q", reason, env.Text)
	}
}

// TestBuildBounceMessageGuidEmptyOmitsBothHeaders verifies that when
// MessageGuid is empty, neither In-Reply-To nor References is emitted at all
// (not merely empty-valued).
func TestBuildBounceMessageGuidEmptyOmitsBothHeaders(t *testing.T) {
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.com", MessageGuid: ""}
	out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 user unknown", "example.com", "")

	headerBlock, _, found := strings.Cut(string(out), "\r\n\r\n")
	if !found {
		t.Fatalf("no header/body separator in output:\n%s", out)
	}
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(line, "In-Reply-To:") {
			t.Errorf("unexpected In-Reply-To header with empty MessageGuid: %q", line)
		}
		if strings.HasPrefix(line, "References:") {
			t.Errorf("unexpected References header with empty MessageGuid: %q", line)
		}
	}
}

// TestBuildBounceMessageGuidSetEmitsBothHeaders verifies that a non-empty
// MessageGuid produces both In-Reply-To and References with the same value.
func TestBuildBounceMessageGuidSetEmitsBothHeaders(t *testing.T) {
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.com", MessageGuid: "<abc@example.test>"}
	out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 user unknown", "example.com", "")

	env, err := enmime.ReadEnvelope(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}
	if irt := env.GetHeader("In-Reply-To"); irt != "<abc@example.test>" {
		t.Errorf("In-Reply-To = %q, want <abc@example.test>", irt)
	}
	if refs := env.GetHeader("References"); refs != "<abc@example.test>" {
		t.Errorf("References = %q, want <abc@example.test>", refs)
	}
}

// TestGenerateAndDeliverIncludesOriginalHeaders proves that when the
// original message's blob is available, GenerateAndDeliver's output carries a
// third text/rfc822-headers part with the original's header lines.
func TestGenerateAndDeliverIncludesOriginalHeaders(t *testing.T) {
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	rawOriginal := []byte("From: alice@example.test\r\nTo: bob@external.com\r\nSubject: original subject\r\nDate: Mon, 01 Jan 2024 00:00:00 +0000\r\n\r\nbody text\r\n")
	hash, err := bs.Put(rawOriginal)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, bs, slog.Default(), "example.test", nil)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	env, err := enmime.ReadEnvelope(bytes.NewReader(deliverer.data))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}
	parts := leafParts(env.Root)
	var hdrsPart *enmime.Part
	for _, p := range parts {
		if p.ContentType == "text/rfc822-headers" {
			hdrsPart = p
		}
	}
	if hdrsPart == nil {
		t.Fatalf("no text/rfc822-headers part in delivered bounce, got types %v", partTypes(parts))
	}
	if !bytes.Contains(hdrsPart.Content, []byte("Subject: original subject")) {
		t.Errorf("rfc822-headers part missing original Subject line, got:\n%s", hdrsPart.Content)
	}
	if bytes.Contains(hdrsPart.Content, []byte("body text")) {
		t.Errorf("rfc822-headers part must not include the original body, got:\n%s", hdrsPart.Content)
	}
}

// TestGenerateAndDeliverMissingBlobStillDelivers proves that an
// unreadable/invalid original blob hash never fails the bounce: delivery still
// happens with two parts (no rfc822-headers part), and the omission is logged.
func TestGenerateAndDeliverMissingBlobStillDelivers(t *testing.T) {
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, bs, logger, "example.test", nil)

	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: "not-a-real-hash"}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver returned an error for a missing original blob, want nil (best-effort): %v", err)
	}
	if deliverer.data == nil {
		t.Fatal("Deliver was not called")
	}

	env, err := enmime.ReadEnvelope(bytes.NewReader(deliverer.data))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}
	parts := leafParts(env.Root)
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2 (rfc822-headers omitted): %v", len(parts), partTypes(parts))
	}
	if !strings.Contains(logBuf.String(), "not-a-real-hash") {
		t.Errorf("expected omission to be logged with the blob hash, got:\n%s", logBuf.String())
	}
}

// TestGenerateAndDeliverSplitRecordHeaders proves that for a split
// (attachment-dedup) record, the rfc822-headers part still carries
// the original top-level headers, read from the skeleton blob's prefix rather
// than a full reconstruction.
func TestGenerateAndDeliverSplitRecordHeaders(t *testing.T) {
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60) // ~3 KB base64
	rawOriginal := []byte("From: alice@example.test\r\nTo: bob@external.com\r\nSubject: split record subject\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + att + "\r\n--B--\r\n")

	split, err := store.StoreSplit(bs, rawOriginal, 1024)
	if err != nil {
		t.Fatalf("StoreSplit: %v", err)
	}
	if split == nil {
		t.Fatal("expected a split record, got nil (message too small to split)")
	}

	deliverer := &fakeDeliverer{}
	gen := NewBounceGenerator(deliverer, bs, slog.Default(), "example.test", nil)

	original := &bongov1.OutboundMessage{
		SenderEmail: "alice@example.test",
		BlobHash:    split.SkeletonHash,
		Layout:      split.Layout,
		Attachments: split.Attachments,
	}
	if err := gen.GenerateAndDeliver(original, []string{"bob@external.com"}, "550 user unknown"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}

	env, err := enmime.ReadEnvelope(bytes.NewReader(deliverer.data))
	if err != nil {
		t.Fatalf("enmime parse: %v", err)
	}
	var hdrsPart *enmime.Part
	for _, p := range leafParts(env.Root) {
		if p.ContentType == "text/rfc822-headers" {
			hdrsPart = p
		}
	}
	if hdrsPart == nil {
		t.Fatal("no text/rfc822-headers part for split record")
	}
	if !bytes.Contains(hdrsPart.Content, []byte("Subject: split record subject")) {
		t.Errorf("rfc822-headers part missing original Subject line for split record, got:\n%s", hdrsPart.Content)
	}
	if bytes.Contains(hdrsPart.Content, []byte("application/pdf")) {
		t.Errorf("rfc822-headers part must not include MIME body content for split record, got:\n%s", hdrsPart.Content)
	}
}

// TestParseEnhancedStatus covers extracting a well-formed RFC 3463
// enhanced status code anchored to a preceding 3-digit basic reply code, with
// the real reason-string shapes produced on both queue failure paths
// (internal/queue/worker.go), not just a bare SMTP reply.
func TestParseEnhancedStatus(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{
			// Every code this generator emits sits next to "Action: failed",
			// so a success/transient class must never be reported verbatim
			// (RFC 3464 2.3.3). Falls back to 5.0.0 instead.
			name:   "success-class code is not reported on a failure DSN",
			reason: "250 2.1.5 recipient ok",
			want:   "5.0.0",
		},
		{
			name:   "transient-class code is not reported on a failure DSN",
			reason: "450 4.2.1 mailbox busy",
			want:   "5.0.0",
		},
		{
			// A wrapped error can quote an earlier successful command before
			// the failing one; the class-5 code is the relevant one, not the
			// first code that happens to appear.
			name:   "skips an earlier success code and reports the failure",
			reason: "250 2.0.0 accepted ... then RCPT TO: 550 5.1.1 <bob@x.com>: User unknown",
			want:   "5.1.1",
		},
		{
			name:   "RCPT rejection",
			reason: "550 5.1.1 <bob@x.com>: User unknown",
			want:   "5.1.1",
		},
		{
			name:   "quoted enhanced code after HELO rejection",
			reason: `504 "5.5.2 <localhost>: Helo command rejected: need fully-qualified hostname"`,
			want:   "5.5.2",
		},
		{
			name:   "wrapped Go error from max-retries path embeds the code mid-string",
			reason: "all MX hosts failed for x.com: RCPT TO bob@x.com: 550 5.1.1 <bob@x.com>: User unknown",
			want:   "5.1.1",
		},
		{
			name:   "literal fallback reason from permanent-fail path",
			reason: "delivery permanently failed",
			want:   "5.0.0",
		},
		{
			name:   "stray dotted version number is not a status code",
			reason: "client rejected: server requires version 5.1.1 of the protocol",
			want:   "5.0.0",
		},
		{
			name:   "class mismatch between basic and enhanced code falls back",
			reason: "250 4.1.1 queued for delivery",
			want:   "5.0.0",
		},
		{
			name:   "over-long digit group is not well-formed",
			reason: "421 5.1234.1 too many digits",
			want:   "5.0.0",
		},
		{
			name:   "empty reason",
			reason: "",
			want:   "5.0.0",
		},
		{
			// Superseded contract: this parser previously returned any class
			// verbatim on the theory that the caller would enforce policy.
			// The class filter belongs in the parser, not the caller: the
			// caller writes Status: verbatim next to Action: failed, so
			// returning any class through would emit a non-failure status
			// on a failure DSN. Covered by the class-5 cases above.
			name:   "non-failure class anywhere in a wrapped error still yields 5.0.0",
			reason: "connect ok 220 2.0.0 ready; later 450 4.2.1 mailbox busy",
			want:   "5.0.0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEnhancedStatus(tt.reason)
			if got != tt.want {
				t.Errorf("parseEnhancedStatus(%q) = %q, want %q", tt.reason, got, tt.want)
			}
		})
	}
}

// TestBuildBounceStatusFromReason verifies end-to-end that the parsed
// enhanced status code appears in the message/delivery-status part's Status
// field, and a reason with no parseable code falls back to 5.0.0.
func TestBuildBounceStatusFromReason(t *testing.T) {
	original := &bongov1.OutboundMessage{SenderEmail: "alice@example.com"}

	out := BuildBounceMessage(original, []string{"bob@external.com"}, "550 5.1.1 <bob@external.com>: User unknown", "example.com", "")
	if !strings.Contains(string(out), "Status: 5.1.1\r\n") {
		t.Errorf("expected Status: 5.1.1 in output:\n%s", out)
	}

	out = BuildBounceMessage(original, []string{"bob@external.com"}, "delivery permanently failed", "example.com", "")
	if !strings.Contains(string(out), "Status: 5.0.0\r\n") {
		t.Errorf("expected fallback Status: 5.0.0 in output:\n%s", out)
	}
}
