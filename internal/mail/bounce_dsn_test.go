package mail

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// messageOfLen returns a message of exactly n bytes: a short header block, the
// marker, then padding lines far below the 998-octet limit.
func messageOfLen(n int) string {
	head := "From: alice@example.test\r\nTo: bob@external.com\r\nSubject: sized\r\n\r\n" + copyBodyMarker + "\r\n"
	rest := n - len(head)
	var b strings.Builder
	b.WriteString(head)
	for rest > 0 {
		l := min(rest, 76)
		if rest-l == 1 { // never leave one byte, which cannot hold a CRLF
			l--
		}
		b.WriteString(strings.Repeat("x", l-2) + "\r\n")
		rest -= l
	}
	return b.String()
}

func putBlob(t *testing.T, raw string) (*store.BlobStore, string) {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return bs, h
}

// reportFor runs one report through a generator and returns the first delivery.
func reportFor(t *testing.T, bs *store.BlobStore, msg *bongov1.OutboundMessage, kind reportKind, reason string) (string, *recordingDeliverer) {
	t.Helper()
	d := &recordingDeliverer{}
	gen := NewBounceGenerator(d, bs, quietLogger(), "example.test", nil)
	gen.SetPostmasterCopy(true, func(string) bool { return true }, "")
	var err error
	switch kind {
	case reportDelayed:
		err = gen.GenerateDelayReport(msg, msg.Recipients, reason, time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	case reportDelivered:
		err = gen.GenerateDeliveredReport(msg, msg.Recipients)
	default:
		err = gen.GenerateAndDeliver(msg, msg.Recipients, reason)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(d.calls) == 0 {
		t.Fatal("nothing delivered")
	}
	return string(d.calls[0].data), d
}

func TestReturnContentByRET(t *testing.T) {
	const limit = 1 << 20
	tests := []struct {
		name     string
		ret      string
		size     int
		wantFull bool
	}{
		{"FULL small", "FULL", 600, true},
		{"FULL at limit", "FULL", limit, true},
		{"FULL one over limit", "FULL", limit + 1, false},
		{"FULL well over", "FULL", limit + 4099, false},
		{"HDRS", "HDRS", 600, false},
		{"no RET", "", 600, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, hash := putBlob(t, messageOfLen(tt.size))
			msg := &bongov1.OutboundMessage{
				SenderEmail: "alice@example.test", BlobHash: hash, DsnRet: tt.ret,
				Recipients: []string{"bob@external.com"},
			}
			out, d := reportFor(t, bs, msg, reportFailed, "550 5.1.1 user unknown")
			full := strings.Contains(out, "Content-Type: message/rfc822")
			if full != tt.wantFull {
				t.Errorf("message/rfc822 present = %v, want %v", full, tt.wantFull)
			}
			if got := strings.Contains(out, copyBodyMarker); got != tt.wantFull {
				t.Errorf("original body present = %v, want %v", got, tt.wantFull)
			}
			// Either way the headers are returned, as text/rfc822-headers or
			// inside the full message.
			if !strings.Contains(out, "Subject: sized") {
				t.Error("original headers missing")
			}
			if tt.wantFull && strings.Contains(out, "text/rfc822-headers") {
				t.Error("full return must replace the header part, not add to it")
			}
			if !tt.wantFull && !strings.Contains(out, "text/rfc822-headers") {
				t.Error("headers part missing")
			}
			// The postmaster copy is a second delivery and never has the body.
			if len(d.calls) != 2 {
				t.Fatalf("deliveries = %d, want sender report + postmaster copy", len(d.calls))
			}
			cp := string(d.calls[1].data)
			if strings.Contains(cp, "message/rfc822") || strings.Contains(cp, copyBodyMarker) {
				t.Error("postmaster copy carries the original message")
			}
		})
	}
}

func TestReturnFullFallsBackToHeaders(t *testing.T) {
	head := "From: alice@example.test\r\nTo: bob@external.com\r\nSubject: sized\r\n\r\n"
	tests := []struct {
		name string
		body string
	}{
		{"NUL byte", copyBodyMarker + "\x00tail\r\n"},
		{"line of 999 octets", strings.Repeat("y", 999) + "\r\n" + copyBodyMarker + "\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, hash := putBlob(t, head+tt.body)
			msg := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash, DsnRet: "FULL", Recipients: []string{"bob@external.com"}}
			out, _ := reportFor(t, bs, msg, reportFailed, "550 5.1.1 no")
			if strings.Contains(out, "message/rfc822") || strings.Contains(out, copyBodyMarker) {
				t.Error("unsafe original was attached")
			}
			if !strings.Contains(out, "text/rfc822-headers") {
				t.Error("no header fallback")
			}
		})
	}

	// A line of exactly 998 octets is fine.
	bs, hash := putBlob(t, head+strings.Repeat("y", 998)+"\r\n"+copyBodyMarker+"\r\n")
	msg := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash, DsnRet: "FULL", Recipients: []string{"bob@external.com"}}
	if out, _ := reportFor(t, bs, msg, reportFailed, "550 5.1.1 no"); !strings.Contains(out, "message/rfc822") {
		t.Error("998-octet line should still be attached")
	}
}

func TestAttachableOriginalRefusesBoundaryLine(t *testing.T) {
	if _, ok := attachableOriginal([]byte("Subject: x\r\n\r\n--b123\r\nbody\r\n"), "b123"); ok {
		t.Error("boundary line accepted")
	}
	if _, ok := attachableOriginal([]byte("Subject: x\r\n\r\n--b123--\r\n"), "b123"); ok {
		t.Error("closing boundary line accepted")
	}
	if _, ok := attachableOriginal([]byte("Subject: x\r\n\r\n--b1234\r\nbody\r\n"), "b123"); !ok {
		t.Error("different boundary refused")
	}
}

func TestReturnFullNonASCIIUses8bit(t *testing.T) {
	bs, hash := putBlob(t, "From: a@example.test\r\nSubject: s\r\n\r\ncafé "+copyBodyMarker+"\r\n")
	msg := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash, DsnRet: "FULL", Recipients: []string{"bob@external.com"}}
	out, _ := reportFor(t, bs, msg, reportFailed, "550 5.1.1 no")
	i := strings.Index(out, "Content-Type: message/rfc822")
	if i < 0 {
		t.Fatal("not attached")
	}
	if rest := out[i:]; !strings.HasPrefix(rest, "Content-Type: message/rfc822\r\nContent-Transfer-Encoding: 8bit\r\n") {
		t.Errorf("CTE = %q", rest[:min(len(rest), 90)])
	}
}

// An oversize split record is refused from its layout alone, before any blob
// is read: the skeleton here is tiny, so a rebuild would fail with a read
// error (logged), while the size check says nothing.
func TestReturnFullOversizeSplitIsNotRebuilt(t *testing.T) {
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skel, err := bs.Put([]byte("Subject: x\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	msg := &bongov1.OutboundMessage{
		BlobHash: skel, DsnRet: "FULL",
		Layout: []*bongov1.MessageSegment{{Kind: bongov1.MessageSegment_LITERAL, Length: 2 << 20}},
	}
	var logs bytes.Buffer
	bg := NewBounceGenerator(&recordingDeliverer{}, bs, slog.New(slog.NewTextHandler(&logs, nil)), "example.test", nil)
	if got := bg.originalFullMessage(msg); got != nil {
		t.Fatal("oversize message attached")
	}
	if strings.Contains(logs.String(), "unreadable") {
		t.Errorf("message was rebuilt before the size check:\n%s", logs.String())
	}
}

// A split record's size is the sum of its literal pieces and attachments, and
// the check runs on blob sizes before anything is rebuilt.
func TestReturnFullSplitRecordSize(t *testing.T) {
	const limit = 1 << 20
	head := "From: alice@example.test\r\nSubject: sized\r\nContent-Type: text/plain\r\n\r\n" + copyBodyMarker + "\r\n"
	tests := []struct {
		name    string
		attSize int
		want    bool
	}{
		{"at limit", limit - len(head), true},
		{"one over limit", limit - len(head) + 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, err := store.NewBlobStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			skel, err := bs.Put([]byte(head))
			if err != nil {
				t.Fatal(err)
			}
			att := strings.Repeat("a", tt.attSize-2) + "\r\n"
			attHash, err := bs.Put([]byte(att))
			if err != nil {
				t.Fatal(err)
			}
			msg := &bongov1.OutboundMessage{
				SenderEmail: "alice@example.test", BlobHash: skel, DsnRet: "FULL", Recipients: []string{"bob@external.com"},
				Attachments: []*bongov1.BlobRef{{Hash: attHash, Size: int64(len(att))}},
				Layout: []*bongov1.MessageSegment{
					{Kind: bongov1.MessageSegment_LITERAL, Length: int64(len(head))},
					{Kind: bongov1.MessageSegment_ATTACHMENT, AttachmentIndex: 0},
				},
			}
			gen := NewBounceGenerator(&recordingDeliverer{}, bs, quietLogger(), "example.test", nil)
			got := gen.originalFullMessage(msg)
			if (got != nil) != tt.want {
				t.Fatalf("attached = %v, want %v", got != nil, tt.want)
			}
			if tt.want && len(got) != limit {
				t.Errorf("rebuilt %d bytes, want %d", len(got), limit)
			}
		})
	}
}

func TestReportOriginalFields(t *testing.T) {
	bs, hash := putBlob(t, messageOfLen(600))
	msg := &bongov1.OutboundMessage{
		SenderEmail: "alice@example.test", BlobHash: hash,
		Recipients: []string{"bob@external.com", "Carol@External.com", "dave@external.com"},
		DsnEnvid:   "envelope id+1",
		RecipientDsn: []*bongov1.RecipientDSN{
			{Address: "bob@external.com", OrcptType: "rfc822", Orcpt: "bob.original@else.example"},
			{Address: "carol@external.com", OrcptType: "utf-8", Orcpt: "jürgen+x@else.example"},
		},
	}
	out, _ := reportFor(t, bs, msg, reportFailed, "550 5.1.1 no")
	for _, want := range []string{
		"Reporting-MTA: dns; example.test\r\nOriginal-Envelope-Id: envelope id+1\r\n\r\n",
		"Original-Recipient: rfc822; bob.original@else.example\r\nFinal-Recipient: rfc822; bob@external.com\r\n",
		// The ASCII letters of the UTF-8 address are written literally; only
		// the non-ASCII character and the reserved '+' are escaped. Carol's
		// record is found although the recipient list has a different case.
		"Original-Recipient: utf-8; j\\x{FC}rgen\\x{2B}x@else.example\r\nFinal-Recipient: rfc822; Carol@External.com\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// dave has no ORCPT, so no Original-Recipient directly before his field.
	if strings.Contains(out, "Original-Recipient: rfc822; dave") || strings.Contains(out, "\r\nOriginal-Recipient: \r\n") {
		t.Error("spurious Original-Recipient")
	}
	if n := strings.Count(out, "Original-Recipient:"); n != 2 {
		t.Errorf("Original-Recipient count = %d, want 2", n)
	}
	// The 7bit delivery-status part stays ASCII.
	i := strings.Index(out, "message/delivery-status")
	if !isASCII(out[i : i+strings.Index(out[i:], "text/rfc822-headers")]) {
		t.Error("non-ASCII in the delivery-status part")
	}

	// No parameters: neither field appears (the pre-DSN report).
	plain := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash, Recipients: []string{"bob@external.com"}}
	out, _ = reportFor(t, bs, plain, reportFailed, "550 5.1.1 no")
	if strings.Contains(out, "Original-Envelope-Id") || strings.Contains(out, "Original-Recipient") {
		t.Error("Original-* fields without DSN parameters")
	}
}

func TestReportKinds(t *testing.T) {
	bs, hash := putBlob(t, messageOfLen(600))
	base := func() *bongov1.OutboundMessage {
		return &bongov1.OutboundMessage{SenderEmail: "alice@example.test", BlobHash: hash, Recipients: []string{"bob@external.com"}}
	}
	tests := []struct {
		name         string
		kind         reportKind
		reason       string
		wantSubject  string
		wantAction   string
		wantStatus   string
		wantDiag     bool
		wantRetry    bool
		wantDeliverN int // sender report + postmaster copies
	}{
		{"failed", reportFailed, "550 5.1.1 no", "Undelivered Mail Returned to Sender", "failed", "5.1.1", true, false, 2},
		{"delayed with class 4 code", reportDelayed, "451 4.7.1 greylisted", "Delayed Mail (still being retried)", "delayed", "4.7.1", true, true, 1},
		{"delayed without code", reportDelayed, "connection refused", "Delayed Mail (still being retried)", "delayed", "4.0.0", true, true, 1},
		{"delayed ignores class 5 code", reportDelayed, "550 5.1.1 no", "Delayed Mail (still being retried)", "delayed", "4.0.0", true, true, 1},
		{"delivered", reportDelivered, "", "Successful Mail Delivery Report", "delivered", "2.0.0", false, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, d := reportFor(t, bs, base(), tt.kind, tt.reason)
			for _, want := range []string{
				"Subject: " + tt.wantSubject + "\r\n",
				"Action: " + tt.wantAction + "\r\n",
				"Status: " + tt.wantStatus + "\r\n",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q", want)
				}
			}
			if got := strings.Contains(out, "Diagnostic-Code:"); got != tt.wantDiag {
				t.Errorf("Diagnostic-Code present = %v, want %v", got, tt.wantDiag)
			}
			if got := strings.Contains(out, "Will-Retry-Until: Thu, 05 Nov 2026 12:00:00 +0000\r\n"); got != tt.wantRetry {
				t.Errorf("Will-Retry-Until present = %v, want %v", got, tt.wantRetry)
			}
			if len(d.calls) != tt.wantDeliverN {
				t.Errorf("deliveries = %d, want %d (postmaster copy is for bounces only)", len(d.calls), tt.wantDeliverN)
			}
			if d.calls[0].from != "" {
				t.Errorf("envelope sender = %q, want null", d.calls[0].from)
			}
			if !bytes.Contains(d.calls[0].data, []byte("Auto-Submitted: auto-replied")) {
				t.Error("missing Auto-Submitted")
			}
		})
	}
}

func TestNotifyWants(t *testing.T) {
	tests := []struct {
		name                     string
		mask                     uint32
		failure, delay, delivery bool
	}{
		{"not given", 0, true, false, false},
		{"NEVER", NotifyNever, false, false, false},
		{"SUCCESS only", NotifySuccess, false, false, true},
		{"DELAY only", NotifyDelay, false, true, false},
		{"FAILURE only", NotifyFailure, true, false, false},
		{"all three", NotifySuccess | NotifyFailure | NotifyDelay, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NotifyWantsFailure(tt.mask); got != tt.failure {
				t.Errorf("failure = %v, want %v", got, tt.failure)
			}
			if got := NotifyWantsDelay(tt.mask); got != tt.delay {
				t.Errorf("delay = %v, want %v", got, tt.delay)
			}
			if got := NotifyWantsSuccess(tt.mask); got != tt.delivery {
				t.Errorf("success = %v, want %v", got, tt.delivery)
			}
		})
	}
}

// The 1 MiB limit holds after line endings are converted: an original with
// bare LFs just under the limit grows past it as CRLF and is not attached.
func TestAttachableOriginalLimitAfterCRLF(t *testing.T) {
	line := strings.Repeat("a", 99) + "\n" // 100 bytes, 101 as CRLF
	raw := []byte("Subject: x\n\n" + strings.Repeat(line, (maxFullReturn-12)/100))
	if len(raw) > maxFullReturn {
		t.Fatalf("fixture is %d bytes, want at most %d", len(raw), maxFullReturn)
	}
	if _, ok := attachableOriginal(raw, "b"); ok {
		t.Error("an original over the limit after CRLF conversion was attached")
	}
	small := []byte("Subject: x\n\nhello\n")
	if _, ok := attachableOriginal(small, "b"); !ok {
		t.Error("a small original was refused")
	}
}
