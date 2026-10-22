package imap

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestFetchPathsDeepMIMEAreFast drives the IMAP FETCH helpers (ENVELOPE and
// BODYSTRUCTURE) with a message that costs enmime seconds to parse.
func TestFetchPathsDeepMIMEAreFast(t *testing.T) {
	raw := testutil.DeepUnclosedMultipart(24)
	s := &session{}

	start := time.Now()
	envelope := s.buildEnvelope(&bongov1.MailMeta{Subject: "meta"}, raw)
	if envelope.Subject != "deep nesting" {
		t.Errorf("envelope subject = %q, want header from the degraded parse", envelope.Subject)
	}
	if bs := s.buildBodyStructure(raw); bs == nil {
		t.Error("buildBodyStructure returned nil")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("FETCH helpers took %v on a deeply nested message, want < 1s", elapsed)
	}
}
