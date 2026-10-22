package rules_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	rulesprocessor "github.com/bongo-project/bongo/internal/pipeline/rules"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestRulesDeepMIMEIsFast requires the rules stage to survive a message that
// costs enmime seconds to parse, and still match on the intact headers.
func TestRulesDeepMIMEIsFast(t *testing.T) {
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {{
				Id:      "r1",
				Enabled: true,
				Conditions: []*bongov1.Condition{
					{Field: "subject", Operator: "contains", Value: "deep nesting"},
				},
				Action: &bongov1.FilterAction{Type: "move", Target: "Archive"},
			}},
		},
	}
	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{Recipients: []string{"alice@example.com"}}

	start := time.Now()
	if _, err := p.Process(context.Background(), env, bytes.NewReader(testutil.DeepUnclosedMultipart(24))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Process took %v on a deeply nested message, want < 1s", elapsed)
	}
	if env.TargetMailbox != "Archive" {
		t.Errorf("TargetMailbox = %q, want Archive (subject header must survive degradation)", env.TargetMailbox)
	}
}
