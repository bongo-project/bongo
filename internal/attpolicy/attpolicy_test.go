package attpolicy

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mimesafe"
)

func defaultPolicy() *Policy {
	return NewPolicy(DefaultExtensions, DefaultMimeTypes)
}

// multi builds a CRLF multipart/mixed message from raw part blocks (headers,
// blank line, body).
func multi(parts ...string) string {
	var b strings.Builder
	b.WriteString("From: a@example.org\r\nTo: b@example.net\r\nSubject: hi\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"BND\"\r\n\r\n")
	for _, p := range parts {
		b.WriteString("--BND\r\n" + p + "\r\n")
	}
	b.WriteString("--BND--\r\n")
	return b.String()
}

const textPart = "Content-Type: text/plain\r\n\r\nhello"

func attach(headers string) string {
	return headers + "\r\n\r\nQUJD"
}

func TestEvaluateDetection(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string // matched rules; nil means the message passes
	}{
		{"plain exe", multi(textPart, attach("Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"setup.exe\"")), []string{".exe"}},
		{"upper case", multi(textPart, attach("Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"SETUP.EXE\"")), []string{".exe"}},
		{"double extension", multi(textPart, attach("Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"invoice.pdf.exe\"")), []string{".exe"}},
		{"pdf passes", multi(textPart, attach("Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"invoice.pdf\"")), nil},
		{"exe in middle passes", multi(textPart, attach("Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.exe.pdf\"")), nil},
		{"trailing dot", multi(textPart, attach("Content-Disposition: attachment; filename=\"evil.EXE.\"")), []string{".exe"}},
		{"trailing space", multi(textPart, attach("Content-Disposition: attachment; filename=\"evil.exe  \"")), []string{".exe"}},
		{"rfc2231 single", multi(textPart, attach("Content-Disposition: attachment; filename*=UTF-8''evil%2Eexe")), []string{".exe"}},
		{"rfc2231 continued", multi(textPart, attach("Content-Disposition: attachment;\r\n filename*0*=UTF-8''my%20in;\r\n filename*1*=voice.p;\r\n filename*2*=s1")), []string{".ps1"}},
		{"rfc2231 continued plain", multi(textPart, attach("Content-Disposition: attachment; filename*0=\"evil.\"; filename*1=\"scr\"")), []string{".scr"}},
		{"rfc2047 name", multi(textPart, attach("Content-Disposition: attachment; filename=\"=?UTF-8?B?ZXZpbC5leGU=?=\"")), []string{".exe"}},
		{"name only in content type", multi(textPart, attach("Content-Type: application/octet-stream; name=\"run.bat\"")), []string{".bat"}},
		{"windows path", multi(textPart, attach("Content-Disposition: attachment; filename=\"C:\\Users\\x\\run.cmd\"")), []string{".cmd"}},
		{"unix path", multi(textPart, attach("Content-Disposition: attachment; filename=\"../../run.com\"")), []string{".com"}},
		{"mime type only", multi(textPart, attach("Content-Type: application/x-msdownload")), []string{"application/x-msdownload"}},
		{"mime type params mixed case", multi(textPart, attach("Content-Type: Application/X-MSDownload; foo=bar")), []string{"application/x-msdownload"}},
		{"nul truncation", multi(textPart, attach("Content-Disposition: attachment; filename=\"evil.exe%00.txt\"")), nil}, // literal text, not a NUL
		{"zip passes", multi(textPart, attach("Content-Type: application/zip\r\nContent-Disposition: attachment; filename=\"a.zip\"")), nil},
		{"nested mixed alternative", multi("Content-Type: multipart/alternative; boundary=\"ALT\"\r\n\r\n--ALT\r\nContent-Type: text/plain\r\n\r\nx\r\n--ALT\r\nContent-Type: multipart/related; boundary=\"REL\"\r\n\r\n--REL\r\nContent-Type: text/html\r\n\r\n<p>\r\n--REL\r\nContent-Disposition: inline; filename=\"x.js\"\r\n\r\nQUJD\r\n--REL--\r\n\r\n--ALT--"), []string{".js"}},
		{"inline part", multi(textPart, "Content-Type: text/plain; name=\"x.vbs\"\r\nContent-Disposition: inline\r\n\r\nbody"), []string{".vbs"}},
		{"no disposition", multi(textPart, "Content-Type: application/octet-stream; name=\"x.msi\"\r\n\r\nQUJD"), []string{".msi"}},
		{"bad header line before disposition", multi(textPart, "X bad line without colon\r\nContent-Disposition: attachment; filename=\"a.exe\"\r\n\r\nQUJD"), []string{".exe"}},
		{"spaced header name", multi(textPart, "Content-Disposition : attachment; filename=\"a.exe\"\r\n\r\nQUJD"), []string{".exe"}},
		{"duplicate filename params", multi(textPart, "Content-Disposition: attachment; filename=\"a.txt\"; filename=\"a.exe\"\r\n\r\nQUJD"), []string{".exe"}},
		{"malformed params fail closed", multi(textPart, "Content-Disposition: attachment; filename=\"a.jar\r\n\r\nQUJD"), []string{".jar"}},
		{"unencoded rfc822", multi(textPart, "Content-Type: message/rfc822\r\n\r\nFrom: x@y\r\nContent-Type: multipart/mixed; boundary=\"IN\"\r\n\r\n--IN\r\nContent-Type: text/plain\r\n\r\nx\r\n--IN\r\nContent-Disposition: attachment; filename=\"in.scr\"\r\n\r\nQUJD\r\n--IN--"), []string{".scr"}},
		{"top-level single part", "From: a@b\r\nContent-Type: application/octet-stream; name=\"x.exe\"\r\nContent-Transfer-Encoding: base64\r\n\r\nQUJD\r\n", []string{".exe"}},
		{"lf only", strings.ReplaceAll(multi(textPart, attach("Content-Disposition: attachment; filename=\"a.exe\"")), "\r\n", "\n"), []string{".exe"}},
		{"plain text mail", "From: a@b\r\nContent-Type: text/plain\r\n\r\nhello\r\n", nil},
		{"no mime at all", "From: a@b\r\n\r\nhello\r\n", nil},
		{"quoted-printable rfc822 not opened", multi(textPart, "Content-Type: message/rfc822\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nFrom: x@y\r\nContent-Disposition: attachment; filename=3D\"in.exe\"\r\n\r\nbody"), nil},
		{"boundary lookalike prefix", multi(textPart, attach("Content-Disposition: attachment; filename=\"ok.txt\"")) + "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The "nul truncation" case writes a literal %00 which is plain text.
			res := Evaluate([]byte(tt.raw), defaultPolicy(), false, nil)
			if tt.want == nil {
				if res.Quarantine {
					t.Fatalf("expected pass, got %+v", res)
				}
				return
			}
			if !res.Quarantine || res.Reason != ReasonBlocked {
				t.Fatalf("expected blocked, got %+v", res)
			}
			if !slices.Equal(res.Rules, tt.want) {
				t.Fatalf("rules = %v, want %v", res.Rules, tt.want)
			}
		})
	}
}

func TestEvaluateNulAndControlInName(t *testing.T) {
	raw := multi(textPart, attach("Content-Disposition: attachment; filename=\"evil.exe\x00.txt\""))
	res := Evaluate([]byte(raw), defaultPolicy(), false, nil)
	if !res.Quarantine {
		t.Fatalf("name truncated at NUL must be caught: %+v", res)
	}
	raw = multi(textPart, attach("Content-Disposition: attachment; filename=\"evil.e\u202exe\""))
	res = Evaluate([]byte(raw), defaultPolicy(), false, nil)
	if !res.Quarantine {
		t.Fatalf("bidi control in name must not hide the extension: %+v", res)
	}
}

func TestEvaluateZipContentsNotOpened(t *testing.T) {
	// A zip whose central directory names an exe passes: archives are not opened.
	raw := multi(textPart, attach("Content-Type: application/zip\r\nContent-Disposition: attachment; filename=\"a.zip\"")+"evil.exe")
	if res := Evaluate([]byte(raw), defaultPolicy(), false, nil); res.Quarantine {
		t.Fatalf("archive must pass: %+v", res)
	}
}

func TestEvaluateEmptyPolicyPasses(t *testing.T) {
	raw := multi(textPart, attach("Content-Disposition: attachment; filename=\"a.exe\""))
	for _, p := range []*Policy{nil, NewPolicy(nil, nil)} {
		if res := Evaluate([]byte(raw), p, true, nil); res.Quarantine || res.Out != nil {
			t.Fatalf("empty policy must pass untouched: %+v", res)
		}
	}
}

func TestEvaluateTooComplexQuarantines(t *testing.T) {
	var b strings.Builder
	b.WriteString("Content-Type: multipart/mixed; boundary=\"b0\"\r\n\r\n")
	for i := 0; i < mimesafe.MaxDepth+3; i++ {
		fmt.Fprintf(&b, "--b%d\r\nContent-Type: multipart/mixed; boundary=\"b%d\"\r\n\r\n", i, i+1)
	}
	res := Evaluate([]byte(b.String()), defaultPolicy(), true, nil)
	if !res.Quarantine || res.Reason != ReasonComplex {
		t.Fatalf("got %+v", res)
	}
	// Too many parts.
	b.Reset()
	b.WriteString("Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n")
	for i := 0; i < mimesafe.MaxParts+5; i++ {
		b.WriteString("--b\r\nContent-Type: text/plain\r\n\r\nx\r\n")
	}
	b.WriteString("--b--\r\n")
	res = Evaluate([]byte(b.String()), defaultPolicy(), true, nil)
	if !res.Quarantine || res.Reason != ReasonComplex {
		t.Fatalf("got %+v", res)
	}
}

func testNotice(name, rule string) string {
	return fmt.Sprintf("removed %q (%s)", name, rule)
}

func decodeNotices(t *testing.T, out []byte) []string {
	t.Helper()
	env, degraded, err := mimesafe.ReadEnvelope(out)
	if err != nil || degraded {
		t.Fatalf("parse: degraded=%v err=%v", degraded, err)
	}
	var notices []string
	for p := env.Root; p != nil; {
		if strings.HasPrefix(p.ContentType, "text/plain") && strings.Contains(string(p.Content), "removed") {
			notices = append(notices, string(p.Content))
		}
		switch {
		case p.FirstChild != nil:
			p = p.FirstChild
		default:
			for p != nil && p.NextSibling == nil {
				p = p.Parent
			}
			if p != nil {
				p = p.NextSibling
			}
		}
	}
	return notices
}

func TestStripKeepsHeadersAndRemovesPart(t *testing.T) {
	signed := "DKIM-Signature: v=1; a=rsa-sha256; d=example.org; s=x; h=from:to; bh=AAAA; b=BBBB\r\n" +
		"ARC-Seal: i=1; a=rsa-sha256; d=example.org; s=x; cv=none; b=CCCC\r\n" +
		"ARC-Message-Signature: i=1; a=rsa-sha256; d=example.org; s=x; h=from; bh=DDDD; b=EEEE\r\n" +
		"ARC-Authentication-Results: i=1; example.org; dkim=pass\r\n"
	raw := signed + multi(textPart, attach("Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"invoice.pdf.exe\""), "Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"ok.pdf\"\r\n\r\nUERG")
	res := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if res.Quarantine || res.Out == nil {
		t.Fatalf("got %+v", res)
	}
	if len(res.Rules) != 1 || res.Rules[0] != ".exe" || res.Parts != 1 {
		t.Fatalf("rules=%v parts=%d", res.Rules, res.Parts)
	}
	// Header block is byte-identical up to the blank line.
	hi := bytes.Index([]byte(raw), []byte("\r\n\r\n"))
	if !bytes.HasPrefix(res.Out, []byte(raw[:hi+4])) {
		t.Fatalf("header block changed:\n%s", res.Out)
	}
	if bytes.Contains(res.Out, []byte("setup")) || bytes.Contains(bytes.ToLower(res.Out), []byte(".exe")) {
		t.Fatalf("blocked name left in output:\n%s", res.Out)
	}
	if !bytes.Contains(res.Out, []byte("ok.pdf")) || !bytes.Contains(res.Out, []byte("UERG")) {
		t.Fatalf("clean part not preserved verbatim:\n%s", res.Out)
	}
	notices := decodeNotices(t, res.Out)
	if len(notices) != 1 || !strings.Contains(notices[0], "invoice.pdf.exe") || !strings.Contains(notices[0], ".exe") {
		t.Fatalf("notices = %q", notices)
	}
	// Second run is a no-op.
	again := Evaluate(res.Out, defaultPolicy(), true, testNotice)
	if again.Quarantine || again.Out != nil || again.Parts != 0 {
		t.Fatalf("second run must be a no-op: %+v", again)
	}
	// Deterministic.
	res2 := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if !bytes.Equal(res.Out, res2.Out) {
		t.Fatal("output is not deterministic")
	}
}

func TestStripLFOnly(t *testing.T) {
	raw := strings.ReplaceAll(multi(textPart, attach("Content-Disposition: attachment; filename=\"a.exe\"")), "\r\n", "\n")
	res := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if res.Quarantine || res.Out == nil {
		t.Fatalf("got %+v", res)
	}
	if bytes.Contains(res.Out, []byte("\r")) {
		t.Fatal("LF mail must stay LF")
	}
	if n := decodeNotices(t, res.Out); len(n) != 1 {
		t.Fatalf("notices = %q", n)
	}
}

func TestStripTopLevelSinglePart(t *testing.T) {
	raw := "From: a@b\r\nSubject: s\r\nDKIM-Signature: v=1; d=x\r\nContent-Type: application/octet-stream; name=\"x.exe\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment\r\n\r\nQUJD\r\n"
	res := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if res.Quarantine || res.Out == nil {
		t.Fatalf("got %+v", res)
	}
	out := string(res.Out)
	for _, keep := range []string{"From: a@b\r\n", "Subject: s\r\n", "DKIM-Signature: v=1; d=x\r\n"} {
		if !strings.Contains(out, keep) {
			t.Errorf("header %q lost:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "x.exe") || strings.Contains(out, "octet-stream") || strings.Contains(out, "QUJD") {
		t.Fatalf("blocked content left:\n%s", out)
	}
	if !strings.Contains(out, "MIME-Version: 1.0") {
		t.Fatalf("MIME-Version missing:\n%s", out)
	}
	if n := decodeNotices(t, res.Out); len(n) != 1 {
		t.Fatalf("notices = %q (out=%s)", n, out)
	}
}

func TestStripInsideRFC822(t *testing.T) {
	raw := multi(textPart, "Content-Type: message/rfc822\r\n\r\nFrom: x@y\r\nContent-Type: application/octet-stream; name=\"in.exe\"\r\n\r\nQUJD")
	res := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if res.Quarantine || res.Out == nil {
		t.Fatalf("got %+v", res)
	}
	if bytes.Contains(res.Out, []byte("in.exe\"")) || bytes.Contains(res.Out, []byte("octet-stream")) {
		t.Fatalf("inner exe left:\n%s", res.Out)
	}
	if !bytes.Contains(res.Out, []byte("From: x@y")) {
		t.Fatalf("inner headers lost:\n%s", res.Out)
	}
}

func TestStripHostileFilename(t *testing.T) {
	long := strings.Repeat("é", 10000) + ".exe"
	names := []string{
		"a\r\n--BND\r\nContent-Type: text/html\r\n\r\n<script>.exe",
		"x\u202e\u2066fdp.exe",
		long,
	}
	for i, n := range names {
		enc := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(n)) + "?="
		raw := multi(textPart, attach("Content-Disposition: attachment; filename=\""+enc+"\""))
		var seen string
		res := Evaluate([]byte(raw), defaultPolicy(), true, func(name, rule string) string {
			seen = name
			return testNotice(name, rule)
		})
		if res.Quarantine || res.Out == nil {
			t.Fatalf("case %d: got %+v", i, res)
		}
		if strings.ContainsAny(seen, "\r\n\u202e\u2066") {
			t.Errorf("case %d: unsanitised name %q", i, seen)
		}
		if len([]rune(seen)) > 255 {
			t.Errorf("case %d: name not capped: %d runes", i, len([]rune(seen)))
		}
		if bytes.Contains(res.Out, []byte("<script>")) {
			t.Errorf("case %d: hostile name reached output", i)
		}
		if n := decodeNotices(t, res.Out); len(n) != 1 {
			t.Errorf("case %d: notices = %d", i, len(n))
		}
	}
}

func TestStripNoNameUsesMimeType(t *testing.T) {
	raw := multi(textPart, attach("Content-Type: application/x-msdownload"))
	var seen string
	res := Evaluate([]byte(raw), defaultPolicy(), true, func(name, rule string) string {
		seen = name
		return testNotice(name, rule)
	})
	if res.Quarantine || seen != "application/x-msdownload" {
		t.Fatalf("seen=%q res=%+v", seen, res)
	}
}

func TestQuarantineActionKeepsOriginal(t *testing.T) {
	raw := multi(textPart, attach("Content-Disposition: attachment; filename=\"a.exe\""))
	res := Evaluate([]byte(raw), defaultPolicy(), false, testNotice)
	if !res.Quarantine || res.Reason != ReasonBlocked || res.Out != nil || res.Parts != 1 {
		t.Fatalf("got %+v", res)
	}
}

// A message whose structure only a lenient reader sees must not slip through
// when the walker found nothing: the cross-check with the production parser
// catches it.
func TestParserMismatchQuarantines(t *testing.T) {
	// The boundary parameter is unquoted with a trailing garbage token, which
	// the walker and enmime may read differently; whichever way, an exe must
	// never pass unnoticed.
	raw := "From: a@b\r\nContent-Type: multipart/mixed; boundary=BND garbage\r\n\r\n--BND\r\nContent-Type: text/plain\r\n\r\nx\r\n--BND\r\nContent-Disposition: attachment; filename=\"a.exe\"\r\n\r\nQUJD\r\n--BND--\r\n"
	res := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if !res.Quarantine && (res.Out == nil || bytes.Contains(bytes.ToLower(res.Out), []byte("a.exe"))) {
		t.Fatalf("exe slipped through: %+v", res)
	}
}

func FuzzEvaluate(f *testing.F) {
	f.Add([]byte(multi(textPart, attach("Content-Disposition: attachment; filename=\"a.exe\""))))
	f.Add([]byte("From: a\n\nx"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=\"aa\"; boundary=\"bb\"\n\n--bb\nContent-Disposition: attachment; filename=a.exe\n\nx\n--bb--"))
	f.Add([]byte("Content-Type: text/plain\nContent-Type: multipart/mixed; boundary=bb\n\n--bb\nContent-Type: application/x-msdownload (x)\n\nx\n--bb--"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: message/rfc822\n\nContent-Type: x/y; name=a.js\n\n--b--"))
	pol := defaultPolicy()
	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) > 1<<16 {
			t.Skip()
		}
		res := Evaluate(in, pol, true, testNotice)
		if res.Quarantine {
			return
		}
		out := res.Out
		if out == nil {
			out = in
		}
		// Whatever is let through parses without a blocked part.
		if pol.parserFinds(out, false) {
			t.Fatalf("blocked part survived:\nin=%q\nout=%q", in, out)
		}
	})
}

// Cases where parsers can disagree about which statement wins must not
// pass: the message is held, or the blocked part is found.
func TestEvaluateAmbiguityFailsClosed(t *testing.T) {
	exe := "Content-Disposition: attachment; filename=\"a.exe\"\r\n\r\nQUJD"
	body := func(b string) string {
		return "--" + b + "\r\nContent-Type: text/plain\r\n\r\nhi\r\n--" + b + "\r\n" + exe + "\r\n--" + b + "--\r\n"
	}
	tests := []struct {
		name, raw string
	}{
		{"repeated boundary parameter", "From: a@b\r\nContent-Type: multipart/mixed; boundary=\"aa\"; boundary=\"bb\"\r\n\r\n--aa\r\nContent-Type: text/plain\r\n\r\nx\r\n--aa--\r\n" + body("bb")},
		{"second content type", "From: a@b\r\nContent-Type: text/plain\r\nContent-Type: multipart/mixed; boundary=bb\r\n\r\n" + body("bb")},
		{"second disposition", multi("Content-Disposition: inline\r\nContent-Disposition: attachment\r\nContent-Type: text/plain\r\n\r\nx")},
		{"second transfer encoding", multi("Content-Transfer-Encoding: 7bit\r\nContent-Transfer-Encoding: base64\r\nContent-Type: text/plain\r\n\r\nx")},
		{"unparsable multipart type", "From: a@b\r\nContent-Type: multipart/mixed; boundary=\"bb\r\n\r\n" + body("bb")},
		{"multipart without boundary", "From: a@b\r\nContent-Type: multipart/mixed\r\n\r\n" + body("bb")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Evaluate([]byte(tt.raw), defaultPolicy(), true, testNotice)
			if !res.Quarantine {
				t.Fatalf("passed: %+v", res)
			}
		})
	}
}

func TestEvaluateBoundaryFromParameter(t *testing.T) {
	// "xboundary" is another parameter; the real boundary is BND.
	raw := strings.Replace(multi(textPart, attach("Content-Disposition: attachment; filename=\"a.exe\"")),
		`boundary="BND"`, `xboundary="decoy"; boundary="BND"`, 1)
	res := Evaluate([]byte(raw), defaultPolicy(), false, nil)
	if !res.Quarantine || !slices.Equal(res.Rules, []string{".exe"}) {
		t.Fatalf("got %+v", res)
	}
}

func TestEvaluateMediaTypeComment(t *testing.T) {
	tests := []struct {
		name, ct string
		blocked  bool
	}{
		{"comment after type", "application/x-msdownload (x)", true},
		{"comment inside type", "application/(c)x-msdownload", true},
		{"comment in name part", "application/octet-stream; name=\"a(1).exe\"", true},
		{"unterminated comment keeps name", "application/pdf (; name=a.exe", true},
		// enmime cannot read a commented media type, so even a harmless one
		// is held rather than passed on the walker's verdict alone.
		{"harmless but unreadable", "application/pdf (x)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Evaluate([]byte(multi(textPart, "Content-Type: "+tt.ct+"\r\n\r\nQUJD")), defaultPolicy(), false, nil)
			if res.Quarantine != tt.blocked {
				t.Fatalf("got %+v", res)
			}
		})
	}
}

// A header with a million continuation lines must be refused quickly, and a
// large one under the cap must be read in linear time.
func TestEvaluateHeaderCostBounded(t *testing.T) {
	huge := "From: a@b\r\nX-Long: a" + strings.Repeat("\r\n a", 1_000_000) + "\r\nContent-Type: text/plain\r\n\r\nx"
	start := time.Now()
	res := Evaluate([]byte(huge), defaultPolicy(), true, testNotice)
	if !res.Quarantine || res.Reason != ReasonComplex {
		t.Fatalf("got %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}

	big := "From: a@b\r\nX-Long: a" + strings.Repeat("\r\n a", 200_000) + "\r\nContent-Type: text/plain\r\n\r\nx"
	start = time.Now()
	res = Evaluate([]byte(big), defaultPolicy(), true, testNotice)
	if res.Quarantine {
		t.Fatalf("got %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}
}

// A parameter list of many valueless segments must be split in linear time.
// Searching the whole remaining value for '=' at every segment made this
// quadratic: a million segments took many seconds.
func TestSplitParamsLinear(t *testing.T) {
	// Compare growth instead of a wall-clock bound, which a loaded CI
	// runner with -race and -cover breaks: four times the input costs about
	// four times as long when linear, sixteen when quadratic.
	elapsed := func(n int) time.Duration {
		v := "inline" + strings.Repeat(";x", n) + "; filename=\"a.exe\""
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			mt, params := splitParams(v)
			if d := time.Since(start); d < best {
				best = d
			}
			if mt != "inline" || len(params) != 1 || params[0].key != "filename" || params[0].val != "a.exe" {
				t.Fatalf("mt=%q params=%+v", mt, params)
			}
		}
		return best
	}
	small, large := elapsed(50_000), elapsed(200_000)
	if small < time.Millisecond {
		small = time.Millisecond
	}
	if ratio := float64(large) / float64(small); ratio > 9 {
		t.Fatalf("splitParams grew %.1fx for 4x the input (%s, %s): not linear", ratio, small, large)
	}
}

// The same shape folded over many short header lines reaches the walker
// unauthenticated. It must be refused quickly as too complex.
func TestEvaluateManyParametersQuarantines(t *testing.T) {
	cd := "Content-Disposition: inline" + strings.Repeat("\r\n "+strings.Repeat(";x", 38), 12_000)
	raw := multi(textPart, cd+"\r\nContent-Type: text/plain\r\n\r\nhello")
	start := time.Now()
	res := Evaluate([]byte(raw), defaultPolicy(), true, testNotice)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Evaluate took %s", d)
	}
	if !res.Quarantine || res.Reason != ReasonComplex {
		t.Fatalf("got %+v", res)
	}
}

func TestSplitParamsShapes(t *testing.T) {
	tests := []struct {
		in   string
		mt   string
		want []param
	}{
		{"text/plain", "text/plain", nil},
		{"Text/Plain ; Charset = utf-8", "text/plain", []param{{key: "charset", idx: -1, val: "utf-8"}}},
		{"attachment; filename=\"a;b.exe\"; size=3", "attachment", []param{{key: "filename", idx: -1, val: "a;b.exe"}, {key: "size", idx: -1, val: "3"}}},
		{"attachment; junk; filename=a.exe", "attachment", []param{{key: "filename", idx: -1, val: "a.exe"}}},
		{"attachment; filename=\"a\\\"b.exe\"", "attachment", []param{{key: "filename", idx: -1, val: "a\"b.exe"}}},
		{"attachment; filename=\"unterminated.exe", "attachment", []param{{key: "filename", idx: -1, val: "unterminated.exe"}}},
		{"attachment; filename*0*=UTF-8''a; filename*1=.exe", "attachment", []param{{key: "filename", idx: 0, enc: true, val: "UTF-8''a"}, {key: "filename", idx: 1, val: ".exe"}}},
		{"attachment;;; trailing", "attachment", nil},
		{"attachment; =v; k=", "attachment", []param{{key: "", idx: -1, val: "v"}, {key: "k", idx: -1, val: ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			mt, params := splitParams(tt.in)
			if mt != tt.mt || !slices.Equal(params, tt.want) {
				t.Fatalf("got %q %+v, want %q %+v", mt, params, tt.mt, tt.want)
			}
		})
	}
}

// A message/rfc822 part whose transfer encoding carries a comment is still an
// unencoded message and must be searched; an encoding nobody knows is held,
// because the walker and the production parser may read it differently.
func TestEvaluateRFC822TransferEncoding(t *testing.T) {
	inner := "From: x@y\r\nContent-Type: application/octet-stream; name=\"in.exe\"\r\n\r\nQUJD"
	part := func(cte string) string {
		return "Content-Type: message/rfc822\r\nContent-Transfer-Encoding: " + cte + "\r\n\r\n" + inner
	}
	tests := []struct {
		name, cte, reason string
		rules             []string
	}{
		{"comment after 7bit", "7bit (forwarded)", ReasonBlocked, []string{".exe"}},
		{"comment before 8bit", "(c) 8BIT", ReasonBlocked, []string{".exe"}},
		{"unknown token", "x-custom", ReasonMismatch, nil},
		{"two tokens", "7bit 8bit", ReasonMismatch, nil},
		{"unterminated comment", "7bit (x", ReasonMismatch, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Evaluate([]byte(multi(textPart, part(tt.cte))), defaultPolicy(), false, nil)
			if !res.Quarantine || res.Reason != tt.reason || !slices.Equal(res.Rules, tt.rules) {
				t.Fatalf("got %+v", res)
			}
		})
	}
	// Stripping inside the commented-encoding message works as for 7bit.
	res := Evaluate([]byte(multi(textPart, part("7bit (forwarded)"))), defaultPolicy(), true, testNotice)
	if res.Quarantine || res.Out == nil || bytes.Contains(res.Out, []byte("in.exe\"")) {
		t.Fatalf("got %+v\n%s", res, res.Out)
	}
}
