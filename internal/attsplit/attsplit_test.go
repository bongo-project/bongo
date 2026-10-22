package attsplit

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mimesafe"
)

// b64block returns a base64-like body of n characters, wrapped at 76 cols with
// CRLF (as real MIME encoders produce). Content is irrelevant: the spike
// extracts the raw bytes verbatim, it never decodes.
func b64block(n int) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var b strings.Builder
	col := 0
	for i := range n {
		if col == 76 {
			b.WriteString("\r\n")
			col = 0
		}
		b.WriteByte(alpha[i%64])
		col++
	}
	return b.String()
}

func mustRoundTrip(t *testing.T, raw string, minSize int) *Skeleton {
	t.Helper()
	sk := Split([]byte(raw), minSize)
	got := sk.Reconstruct()
	if !bytes.Equal(got, []byte(raw)) {
		t.Fatalf("reconstruction is NOT byte-exact\n--- want (%d bytes) ---\n%q\n--- got (%d bytes) ---\n%q", len(raw), raw, len(got), got)
	}
	return sk
}

func TestRoundTripSimpleBase64(t *testing.T) {
	att := b64block(1200)
	raw := "From: a@ext.test\r\n" +
		"To: b@example.com\r\n" +
		"Subject: hi\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"BOUND\"\r\n\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain\r\n\r\nSee attached.\r\n" +
		"--BOUND\r\n" +
		"Content-Type: application/zip; name=\"a.zip\"\r\n" +
		"Content-Disposition: attachment; filename=\"a.zip\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		att + "\r\n" +
		"--BOUND--\r\n"

	sk := mustRoundTrip(t, raw, 200)
	if len(sk.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(sk.Parts))
	}
	p := sk.Parts[0]
	if p.Filename != "a.zip" {
		t.Errorf("filename = %q, want a.zip", p.Filename)
	}
	if p.ContentType != "application/zip" {
		t.Errorf("content type = %q, want application/zip", p.ContentType)
	}
	if len(p.Hash) != 64 {
		t.Errorf("hash = %q, want 64 hex chars", p.Hash)
	}
	if string(p.Encoded) != att {
		t.Errorf("extracted bytes are not the exact encoded body")
	}
	// The skeleton must be materially smaller than the raw (attachment removed).
	if sk.SkeletonSize() >= len(raw)-len(att)+16 {
		t.Errorf("skeleton not reduced: skeleton=%d raw=%d att=%d", sk.SkeletonSize(), len(raw), len(att))
	}
}

func TestBelowThresholdNotSplit(t *testing.T) {
	att := b64block(120) // small
	raw := "From: a@ext.test\r\nTo: b@example.com\r\nSubject: hi\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: application/octet-stream; name=\"tiny.bin\"\r\n" +
		"Content-Disposition: attachment; filename=\"tiny.bin\"\r\n\r\n" +
		att + "\r\n--B--\r\n"

	sk := mustRoundTrip(t, raw, 4096)
	if len(sk.Parts) != 0 {
		t.Fatalf("parts = %d, want 0 (below threshold)", len(sk.Parts))
	}
}

func TestMultipleAttachments(t *testing.T) {
	a1, a2 := b64block(800), b64block(900)
	raw := "Subject: two\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"one.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"one.pdf\"\r\n\r\n" + a1 + "\r\n" +
		"--B\r\nContent-Type: application/zip; name=\"two.zip\"\r\n" +
		"Content-Disposition: attachment; filename=\"two.zip\"\r\n\r\n" + a2 + "\r\n" +
		"--B--\r\n"

	sk := mustRoundTrip(t, raw, 200)
	if len(sk.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(sk.Parts))
	}
	if sk.Parts[0].Filename != "one.pdf" || sk.Parts[1].Filename != "two.zip" {
		t.Errorf("filenames = %q,%q", sk.Parts[0].Filename, sk.Parts[1].Filename)
	}
}

func TestNestedMultipart(t *testing.T) {
	att := b64block(1000)
	raw := "Subject: nested\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"OUT\"\r\n\r\n" +
		"--OUT\r\n" +
		"Content-Type: multipart/alternative; boundary=\"IN\"\r\n\r\n" +
		"--IN\r\nContent-Type: text/plain\r\n\r\nplain\r\n" +
		"--IN\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n" +
		"--IN--\r\n" +
		"--OUT\r\n" +
		"Content-Type: image/png; name=\"pic.png\"\r\n" +
		"Content-Disposition: attachment; filename=\"pic.png\"\r\n\r\n" + att + "\r\n" +
		"--OUT--\r\n"

	sk := mustRoundTrip(t, raw, 200)
	if len(sk.Parts) != 1 {
		t.Fatalf("parts = %d, want 1 (only the attachment; nested alt parts stay inline)", len(sk.Parts))
	}
	if sk.Parts[0].Filename != "pic.png" {
		t.Errorf("filename = %q, want pic.png", sk.Parts[0].Filename)
	}
}

func TestDedupSameAttachmentDifferentMessages(t *testing.T) {
	att := b64block(1500)
	build := func(subject, from string) string {
		return "Subject: " + subject + "\r\nFrom: " + from + "\r\n" +
			"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
			"--B\r\nContent-Type: text/plain\r\n\r\n" + subject + "\r\n" +
			"--B\r\nContent-Type: application/pdf; name=\"report.pdf\"\r\n" +
			"Content-Disposition: attachment; filename=\"report.pdf\"\r\n\r\n" + att + "\r\n--B--\r\n"
	}
	s1 := mustRoundTrip(t, build("first", "x@a.test"), 200)
	s2 := mustRoundTrip(t, build("second entirely different", "y@b.test"), 200)

	if len(s1.Parts) != 1 || len(s2.Parts) != 1 {
		t.Fatalf("expected one part each")
	}
	if s1.Parts[0].Hash != s2.Parts[0].Hash {
		t.Fatalf("same attachment in two different messages must hash equal (dedup):\n%s\n%s", s1.Parts[0].Hash, s2.Parts[0].Hash)
	}
}

// TestDKIMSignedByteExact: a DKIM-signed message must reconstruct byte-for-byte,
// so the signed bytes (headers + body hash) are preserved and verification would
// still pass. Byte-equality is the proof; no DNS/verify needed.
func TestDKIMSignedByteExact(t *testing.T) {
	att := b64block(2000)
	raw := "DKIM-Signature: v=1; a=rsa-sha256; d=ext.test; s=mail; c=relaxed/simple;\r\n" +
		"\tbh=abcdEFGHij0123456789klmnoPQRSTUVWXyz+/=; b=SIGVALUE0123456789/+\r\n" +
		"From: a@ext.test\r\nTo: b@example.com\r\nSubject: signed\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody text\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + att + "\r\n--B--\r\n"

	sk := mustRoundTrip(t, raw, 200)
	if len(sk.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(sk.Parts))
	}
}

func TestPlainMessagePassthrough(t *testing.T) {
	raw := "From: a@ext.test\r\nSubject: plain\r\n\r\nJust a plain text body, no MIME parts.\r\n"
	sk := mustRoundTrip(t, raw, 200)
	if len(sk.Parts) != 0 {
		t.Fatalf("parts = %d, want 0 (plain message)", len(sk.Parts))
	}
}

func TestQuotedPrintableAttachment(t *testing.T) {
	// A quoted-printable-ish body; extraction is encoding-agnostic (raw bytes).
	body := strings.Repeat("Some=20text=20with=3Dequals=0D=0A", 40)
	raw := "Subject: qp\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: text/plain; name=\"note.txt\"\r\n" +
		"Content-Disposition: attachment; filename=\"note.txt\"\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" + body + "\r\n--B--\r\n"

	sk := mustRoundTrip(t, raw, 200)
	if len(sk.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(sk.Parts))
	}
}

// overLimitWithAttachment is a multipart whose first part is a large
// attachment, followed by a delimiter chain that trips attsplit's depth cap.
// The attachment is collected before the cap is hit, so only the "stored whole
// when over" rule keeps it out of the result. mimesafe.Check sees no nesting.
func overLimitWithAttachment(levels int) string {
	var b strings.Builder
	b.WriteString("From: a@ext.test\r\nSubject: z\r\nContent-Type: multipart/mixed; boundary=q00000\r\n\r\n")
	b.WriteString("--q00000\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n")
	b.WriteString(b64block(4000) + "\r\n")
	b.WriteString("--q00000Z\r\nContent-Type: multipart/mixed; boundary=q00001\r\n\r\n")
	for i := 1; i < levels; i++ {
		fmt.Fprintf(&b, "--q%05dZ\r\nContent-Type: multipart/mixed; boundary=q%05d\r\n\r\n", i, i+1)
	}
	for i := levels - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "\r\n--q%05dZ--\r\n", i)
	}
	return b.String()
}

func TestSplitOverLimitStoredWhole(t *testing.T) {
	// Control: without the chain the same attachment is extracted.
	control := overLimitWithAttachment(1)
	if sk := Split([]byte(control), 1024); len(sk.Parts) != 1 {
		t.Fatalf("control: parts = %d, want 1", len(sk.Parts))
	}
	raw := overLimitWithAttachment(mimesafe.MaxDepth + 8)
	sk := Split([]byte(raw), 1024)
	if len(sk.Parts) != 0 {
		t.Errorf("over-limit message yielded %d attachment parts, want 0 (stored whole)", len(sk.Parts))
	}
	if got := sk.Reconstruct(); !bytes.Equal(got, []byte(raw)) {
		t.Error("over-limit message does not reconstruct byte-exact")
	}
}
