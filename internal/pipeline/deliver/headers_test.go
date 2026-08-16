package deliver

import (
	"strings"
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestBuildAuthenticationResults(t *testing.T) {
	tests := []struct {
		name       string
		hostname   string
		spf        string
		dkimResult string
		dkimDomain string
		dmarc      string
		want       string
	}{
		{
			"full: AC1 - spf pass, dkim pass with domain, dmarc pass",
			"mail.example.com", "pass", "pass", "example.test", "pass",
			"Authentication-Results: mail.example.com; spf=pass; dkim=pass header.d=example.test; dmarc=pass",
		},
		{
			"empty hostname omits the header entirely (graceful degradation)",
			"", "pass", "pass", "example.test", "pass",
			"",
		},
		{
			"empty dmarc result token is omitted, not emitted blank",
			"mail.example.com", "pass", "pass", "example.test", "",
			"Authentication-Results: mail.example.com; spf=pass; dkim=pass header.d=example.test",
		},
		{
			"dkim none has no domain to report",
			"mail.example.com", "pass", "none", "", "",
			"Authentication-Results: mail.example.com; spf=pass; dkim=none",
		},
		{
			"invalid spf token is dropped rather than interpolated verbatim",
			"mail.example.com", "bogus-value", "pass", "example.test", "",
			"Authentication-Results: mail.example.com; dkim=pass header.d=example.test",
		},
		{
			"spf token is case-normalized",
			"mail.example.com", "PASS", "none", "", "",
			"Authentication-Results: mail.example.com; spf=pass; dkim=none",
		},
		{
			"dkim domain containing an injected ';' is dropped, not truncated in place",
			"mail.example.com", "pass", "pass", "example.test; dmarc=pass", "",
			"Authentication-Results: mail.example.com; spf=pass; dkim=pass",
		},
		{
			"CR/LF in hostname is stripped (header injection defense)",
			"mail.example.com\r\nX-Injected: evil", "pass", "none", "", "",
			"Authentication-Results: mail.example.comX-Injected: evil; spf=pass; dkim=none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAuthenticationResults(tt.hostname, tt.spf, tt.dkimResult, tt.dkimDomain, tt.dmarc)
			if got != tt.want {
				t.Errorf("buildAuthenticationResults() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAuthenticationResultsCRLFStrippedNotJustLF(t *testing.T) {
	// mail.SanitizeHeaderValue strips CR and LF independently, so a hostname
	// carrying "\r\n" loses both characters, not just the CR - confirming no
	// bare CR (which alone can still be interpreted as a line break by some
	// parsers) survives either.
	got := buildAuthenticationResults("evil\r\nSet-Cookie: x", "pass", "none", "", "")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("buildAuthenticationResults() = %q, contains a raw CR or LF", got)
	}
}

func TestBuildSpamHeaders(t *testing.T) {
	t.Run("nil stage means the message was never scanned, so no headers are emitted", func(t *testing.T) {
		got := buildSpamHeaders(nil, 0.5)
		if got != nil {
			t.Errorf("buildSpamHeaders(nil, ...) = %v, want nil", got)
		}
	})

	t.Run("AC2/AC3: score, status, and reasons", func(t *testing.T) {
		stage := &bongov1.StageResult{Score: 0.62, Reasons: []string{"SPF fail", "DKIM missing"}}
		got := buildSpamHeaders(stage, 0.5)
		want := []string{
			"X-Spam-Score: 0.62",
			"X-Spam-Status: Yes",
			"X-Spam-Reasons: SPF fail, DKIM missing",
		}
		if len(got) != len(want) {
			t.Fatalf("buildSpamHeaders() = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("buildSpamHeaders()[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("AC3: below threshold is X-Spam-Status: No", func(t *testing.T) {
		stage := &bongov1.StageResult{Score: 0.3}
		got := buildSpamHeaders(stage, 0.5)
		if got[1] != "X-Spam-Status: No" {
			t.Errorf("status header = %q, want X-Spam-Status: No", got[1])
		}
	})

	t.Run("AC3: exactly at threshold is Yes", func(t *testing.T) {
		stage := &bongov1.StageResult{Score: 0.5}
		got := buildSpamHeaders(stage, 0.5)
		if got[1] != "X-Spam-Status: Yes" {
			t.Errorf("status header = %q, want X-Spam-Status: Yes", got[1])
		}
	})

	t.Run("no reasons omits X-Spam-Reasons entirely", func(t *testing.T) {
		stage := &bongov1.StageResult{Score: 0.1}
		got := buildSpamHeaders(stage, 0.5)
		if len(got) != 2 {
			t.Fatalf("buildSpamHeaders() = %v, want exactly Score and Status", got)
		}
	})

	t.Run("a reason containing CR/LF cannot inject a header", func(t *testing.T) {
		stage := &bongov1.StageResult{
			Score:   0.9,
			Reasons: []string{"evil\r\nX-Injected: forged"},
		}
		got := buildSpamHeaders(stage, 0.5)
		reasonsLine := got[len(got)-1]
		if strings.ContainsAny(reasonsLine, "\r\n") {
			t.Errorf("X-Spam-Reasons line = %q, contains raw CR/LF (header injection)", reasonsLine)
		}
		if !strings.Contains(reasonsLine, "X-Injected: forged") {
			t.Errorf("expected injected text to survive as inert text within the value, got %q", reasonsLine)
		}
	})
}

func TestStripTraceHeaders(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			"removes Authentication-Results and X-Spam-* case-insensitively, leaves everything else",
			"Received: from x\r\nauthentication-results: mail.example.com; spf=pass\r\n" +
				"X-SPAM-STATUS: No\r\nX-Spam-Score: 0.00\r\nFrom: a@b.com\r\nSubject: hi\r\n\r\nbody\r\n",
			"Received: from x\r\nFrom: a@b.com\r\nSubject: hi\r\n\r\nbody\r\n",
		},
		{
			"removes folded continuation lines belonging to a stripped header",
			"X-Spam-Reasons: SPF fail,\r\n DKIM missing,\r\n more reasons\r\n" +
				"From: a@b.com\r\n\r\nbody\r\n",
			"From: a@b.com\r\n\r\nbody\r\n",
		},
		{
			"does not touch a header-looking string in the body",
			"From: a@b.com\r\n\r\nX-Spam-Status: No\r\nAuthentication-Results: forged\r\n",
			"From: a@b.com\r\n\r\nX-Spam-Status: No\r\nAuthentication-Results: forged\r\n",
		},
		{
			"no matching headers leaves the message unchanged",
			"From: a@b.com\r\nSubject: hi\r\n\r\nbody\r\n",
			"From: a@b.com\r\nSubject: hi\r\n\r\nbody\r\n",
		},
		{
			"LF-only message stays LF-only and headers are still stripped",
			"X-Spam-Status: No\nFrom: a@b.com\n\nbody\n",
			"From: a@b.com\n\nbody\n",
		},
		{
			// Superseded contract: a bodyless message used to be returned
			// unchanged, which let a forged X-Spam-Status survive into storage
			// on exactly the message shape an attacker controls. A message
			// with no body is valid RFC 5322 and is all header block, so
			// stripping applies to it too.
			"bodyless message is still stripped (FR-12 has no exemption)",
			"X-Spam-Status: No\r\nFrom: a@b.com",
			"From: a@b.com",
		},
		{
			"bodyless LF-only message is stripped and stays LF-only",
			"X-Spam-Status: No\nFrom: a@b.com",
			"From: a@b.com",
		},
		{
			// The header block is LF-only; the CRLF blank line belongs to the
			// BODY. Detecting CRLF from it would terminate stamped headers
			// with the wrong sequence.
			"LF header block with a CRLF blank line in the body stays LF",
			"X-Spam-Status: No\nFrom: a@b.com\n\npart\r\n\r\nmore\n",
			"From: a@b.com\n\npart\r\n\r\nmore\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(stripTraceHeaders([]byte(tt.raw)))
			if got != tt.want {
				t.Errorf("stripTraceHeaders() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestPrependHeaders(t *testing.T) {
	t.Run("CRLF message uses CRLF for the new headers", func(t *testing.T) {
		raw := "From: a@b.com\r\nSubject: hi\r\n\r\nbody\r\n"
		got := string(prependHeaders([]byte(raw), []string{"X-Spam-Score: 0.10"}))
		want := "X-Spam-Score: 0.10\r\nFrom: a@b.com\r\nSubject: hi\r\n\r\nbody\r\n"
		if got != want {
			t.Errorf("prependHeaders() =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("LF-only message uses LF for the new headers, not hardcoded CRLF", func(t *testing.T) {
		raw := "From: a@b.com\nSubject: hi\n\nbody\n"
		got := string(prependHeaders([]byte(raw), []string{"X-Spam-Score: 0.10"}))
		want := "X-Spam-Score: 0.10\nFrom: a@b.com\nSubject: hi\n\nbody\n"
		if got != want {
			t.Errorf("prependHeaders() =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("headers are prepended above every existing header (FR-13)", func(t *testing.T) {
		raw := "DKIM-Signature: v=1; h=from:subject\r\nFrom: a@b.com\r\n\r\nbody\r\n"
		got := string(prependHeaders([]byte(raw), []string{"Authentication-Results: mail.example.com; spf=pass"}))
		if !strings.HasPrefix(got, "Authentication-Results:") {
			t.Errorf("prependHeaders() did not start with the new header: %q", got)
		}
		if !strings.Contains(got, "DKIM-Signature: v=1; h=from:subject\r\nFrom: a@b.com\r\n\r\nbody\r\n") {
			t.Errorf("prependHeaders() did not preserve the original header block: %q", got)
		}
	})

	t.Run("no header lines to add returns raw unchanged", func(t *testing.T) {
		raw := "From: a@b.com\r\n\r\nbody\r\n"
		got := string(prependHeaders([]byte(raw), nil))
		if got != raw {
			t.Errorf("prependHeaders(nil) = %q, want unchanged %q", got, raw)
		}
	})

	// Superseded contract: a message with no blank-line separator used to be
	// returned unchanged. It is a bodyless message (valid RFC 5322), and
	// skipping it would mean the trace headers a reader relies on are absent
	// precisely where a forged set was just stripped.
	t.Run("bodyless message still gets headers prepended", func(t *testing.T) {
		raw := "From: a@b.com"
		got := string(prependHeaders([]byte(raw), []string{"X-Spam-Score: 0.10"}))
		want := "X-Spam-Score: 0.10\r\nFrom: a@b.com"
		if got != want {
			t.Errorf("prependHeaders() = %q, want %q", got, want)
		}
	})
}

func TestFoldHeaderLine(t *testing.T) {
	short := "X-Spam-Reasons: SPF fail, DKIM missing"
	if got := foldHeaderLine(short, "\r\n"); got != short {
		t.Errorf("short line should not be folded: got %q", got)
	}

	long := "X-Spam-Reasons: " + strings.Repeat("reason, ", 200)
	folded := foldHeaderLine(long, "\r\n")
	for _, physical := range strings.Split(folded, "\r\n") {
		if len(physical) > maxHeaderLineLen {
			t.Errorf("folded physical line exceeds maxHeaderLineLen: %d bytes", len(physical))
		}
	}
	if folded == long {
		t.Fatal("long line was not folded at all")
	}
	// Each fold point replaces exactly one original space with the fold
	// marker (ending + tab); reversing that substitution must reconstruct
	// the original value exactly.
	reconstructed := strings.ReplaceAll(folded, "\r\n\t", " ")
	if reconstructed != long {
		t.Errorf("folded content does not round-trip:\ngot  %q\nwant %q", reconstructed, long)
	}
}

func TestAntispamStageAndSpamScoreFromStages(t *testing.T) {
	stages := []*bongov1.StageResult{
		{Stage: "rules", Score: 0.1},
		{Stage: "antispam", Score: 0.75, Reasons: []string{"x"}},
	}
	s := antispamStage(stages)
	if s == nil || s.Score != 0.75 {
		t.Fatalf("antispamStage() = %+v, want the antispam stage with score 0.75", s)
	}
	if got := spamScoreFromStages(stages); got != 0.75 {
		t.Errorf("spamScoreFromStages() = %v, want 0.75", got)
	}
	if s2 := antispamStage(nil); s2 != nil {
		t.Errorf("antispamStage(nil) = %+v, want nil", s2)
	}
	if got := spamScoreFromStages(nil); got != 0.0 {
		t.Errorf("spamScoreFromStages(nil) = %v, want 0.0", got)
	}
}
