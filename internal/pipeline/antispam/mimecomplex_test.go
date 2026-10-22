package antispam

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestBuiltinDeepMIMEIsFast feeds a message that costs enmime seconds to parse
// (24 unclosed multipart levels) and requires the stage to finish quickly.
func TestBuiltinDeepMIMEIsFast(t *testing.T) {
	proc := NewBuiltinProcessor(antispam.NewScorer())
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
	}

	start := time.Now()
	result, err := proc.Process(context.Background(), env, bytes.NewReader(testutil.DeepUnclosedMultipart(24)))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Process took %v on a deeply nested message, want < 1s", elapsed)
	}
	if result == nil || result.Stage != "antispam" {
		t.Errorf("unexpected result %+v", result)
	}
}
