package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mimesafe"
)

// splittableWithParts is bigAttachmentMessage with n extra filler parts, so the
// large attachment would normally be split out.
func splittableWithParts(n int) []byte {
	msg := bigAttachmentMessage("many parts")
	filler := strings.Repeat("--B\r\nContent-Type: text/plain\r\n\r\nx\r\n", n)
	return []byte(strings.Replace(msg, "--B\r\nContent-Type: application/pdf", filler+"--B\r\nContent-Type: application/pdf", 1))
}

// TestPrepareSplitSkipsOverLimitMIME pins that a message over the MIME limits
// is stored whole instead of being walked by attsplit, with a control showing
// the same shape splits when under the limit.
func TestPrepareSplitSkipsOverLimitMIME(t *testing.T) {
	tests := []struct {
		name      string
		extra     int
		wantSplit bool
	}{
		{"under limit splits", 10, true},
		{"just under part cap splits", mimesafe.MaxParts - 3, true},
		{"over part cap stored whole", mimesafe.MaxParts + 7, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrepareSplit(splittableWithParts(tt.extra), 1024) != nil
			if got != tt.wantSplit {
				t.Errorf("split = %v, want %v", got, tt.wantSplit)
			}
		})
	}
}

// zChain builds a message whose level L delimiter is "--q<L>Z" for boundary
// q<L>. attsplit accepts any line starting with "--boundary" as a delimiter, so
// it descends one level per line, while a strict MIME parser (and
// mimesafe.Check) sees no nesting at all.
func zChain(levels int) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nSubject: z\r\nContent-Type: multipart/mixed; boundary=q00000\r\n\r\n")
	b.WriteString("--q00000\r\nX: y\r\n\r\nfoo\r\n")
	b.WriteString("--q00000Z\r\nContent-Type: multipart/mixed; boundary=q00001\r\n\r\n")
	for i := 1; i < levels; i++ {
		fmt.Fprintf(&b, "--q%05dZ\r\nContent-Type: multipart/mixed; boundary=q%05d\r\n\r\n", i, i+1)
	}
	for i := levels - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "\r\n--q%05dZ--\r\n", i)
	}
	return []byte(b.String())
}

// TestPrepareSplitBoundedOnDelimiterChain pins that attsplit's own looser
// delimiter matching cannot be driven quadratic: the chain is stored whole and
// quickly, even though mimesafe.Check sees nothing wrong with it.
func TestPrepareSplitBoundedOnDelimiterChain(t *testing.T) {
	raw := zChain(32000) // over 2 MB
	if len(raw) < 2<<20 {
		t.Fatalf("fixture too small: %d bytes", len(raw))
	}
	start := time.Now()
	got := PrepareSplit(raw, 1024)
	if el := time.Since(start); el > time.Second {
		t.Errorf("PrepareSplit took %v on a delimiter chain, want < 1s", el)
	}
	if got != nil {
		t.Errorf("delimiter chain was split, want stored whole")
	}
}

// TestPrepareSplitOverLimitWithAttachmentStoredWhole pins that an attachment
// collected before attsplit hits its depth cap is not split out: the message is
// over the limit, so nothing from the partial walk may be used. mimesafe.Check
// passes this message, so only attsplit's own rule can reject it.
func TestPrepareSplitOverLimitWithAttachmentStoredWhole(t *testing.T) {
	build := func(levels int) []byte {
		var b strings.Builder
		b.WriteString("From: a@example.com\r\nSubject: z\r\nContent-Type: multipart/mixed; boundary=q00000\r\n\r\n")
		b.WriteString("--q00000\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n")
		b.WriteString(strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", 100) + "\r\n")
		b.WriteString("--q00000Z\r\nContent-Type: multipart/mixed; boundary=q00001\r\n\r\n")
		for i := 1; i < levels; i++ {
			fmt.Fprintf(&b, "--q%05dZ\r\nContent-Type: multipart/mixed; boundary=q%05d\r\n\r\n", i, i+1)
		}
		for i := levels - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "\r\n--q%05dZ--\r\n", i)
		}
		return []byte(b.String())
	}
	if PrepareSplit(build(1), 1024) == nil {
		t.Fatal("control: shallow message with a large attachment was not split")
	}
	over := build(mimesafe.MaxDepth + 8)
	if _, _, err := mimesafe.Check(over); err != nil {
		t.Fatalf("fixture must pass mimesafe.Check, got %v", err)
	}
	if sm := PrepareSplit(over, 1024); sm != nil {
		t.Errorf("over-limit message was split into %d attachments, want stored whole", len(sm.Attachments))
	}
}
