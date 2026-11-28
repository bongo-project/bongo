package rules_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	rulesprocessor "github.com/bongo-project/bongo/internal/pipeline/rules"
)

type featureStub struct {
	off bool
	err error
}

func (f featureStub) FeatureDisabled(string, string) (bool, error) {
	return f.off || f.err != nil, f.err
}

// With rules switched off for the user none of their rules apply, a lookup
// failure counts as off, and the mail is left for normal delivery.
func TestRulesSkippedWhenFeatureDisabled(t *testing.T) {
	provider := &mockRuleProvider{rules: map[string][]*bongov1.FilterRule{
		"alice@example.com": {{
			Id: "r1", Name: "move", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "Hello"}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Archive"},
		}},
	}}
	tests := []struct {
		name       string
		features   *featureStub
		wantTarget string
		wantAction string
	}{
		{"no lookup wired", nil, "Archive", "pass"},
		{"rules on", &featureStub{}, "Archive", "pass"},
		{"rules off", &featureStub{off: true}, "", "pass"},
		{"lookup error fails closed", &featureStub{err: errors.New("pebble down")}, "", "pass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := rulesprocessor.NewRulesProcessor(provider, nil)
			if tt.features != nil {
				p.SetFeatures(tt.features)
			}
			env := &bongov1.QueueEnvelope{Recipients: []string{"alice@example.com"}}
			msg := strings.NewReader(minimalMessage("bob@example.com", "alice@example.com", "Hello", "World"))
			res, err := p.Process(context.Background(), env, msg)
			if err != nil {
				t.Fatal(err)
			}
			if env.TargetMailbox != tt.wantTarget || res.Action != tt.wantAction {
				t.Fatalf("target=%q action=%q, want %q %q", env.TargetMailbox, res.Action, tt.wantTarget, tt.wantAction)
			}
		})
	}
}
