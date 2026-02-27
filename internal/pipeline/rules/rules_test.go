package rules_test

import (
	"context"
	"strings"
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	rulesprocessor "github.com/bongo-go/bongo-go/internal/pipeline/rules"
)

// mockRuleProvider returns predefined rules keyed by userID.
type mockRuleProvider struct {
	rules map[string][]*bongov1.FilterRule
	err   error
}

func (m *mockRuleProvider) List(userID string) ([]*bongov1.FilterRule, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.rules[userID], nil
}

// minimalMessage constructs a minimal RFC 5322 message with the given headers and body.
func minimalMessage(from, to, subject, body string) string {
	return "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body
}

func TestRulesPass(t *testing.T) {
	// Rule exists but condition does not match — action should be "pass".
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "no match",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "NEVER_MATCHES_XYZ"},
					},
					Action: &bongov1.FilterAction{Type: "move", Target: "Archive"},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com"},
	}
	msg := strings.NewReader(minimalMessage("bob@example.com", "alice@example.com", "Hello", "World"))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	if env.TargetMailbox != "" {
		t.Errorf("expected empty TargetMailbox, got %q", env.TargetMailbox)
	}
}

func TestRulesMoveToFolder(t *testing.T) {
	// Rule matches on subject — action should set TargetMailbox.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "archive newsletters",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Newsletter"},
					},
					Action: &bongov1.FilterAction{Type: "move", Target: "Newsletters"},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com"},
	}
	msg := strings.NewReader(minimalMessage(
		"news@example.com",
		"alice@example.com",
		"Weekly Newsletter",
		"Here is your weekly digest.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	if env.TargetMailbox != "Newsletters" {
		t.Errorf("expected TargetMailbox=Newsletters, got %q", env.TargetMailbox)
	}
}

func TestRulesNoRules(t *testing.T) {
	// User has no rules at all — should return action=pass cleanly.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"bob@example.com"},
	}
	msg := strings.NewReader(minimalMessage("alice@example.com", "bob@example.com", "Hi", "Hey there"))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	if env.TargetMailbox != "" {
		t.Errorf("expected empty TargetMailbox, got %q", env.TargetMailbox)
	}
}

func TestRulesDeleteAction(t *testing.T) {
	// Delete rule should set action=reject in the StageResult.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "block spam sender",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "from", Operator: "contains", Value: "spammer@"},
					},
					Action: &bongov1.FilterAction{Type: "delete"},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com"},
	}
	msg := strings.NewReader(minimalMessage(
		"spammer@evil.example",
		"alice@example.com",
		"Buy now!",
		"Click here.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "reject" {
		t.Errorf("expected action=reject, got %q", result.Action)
	}
}

func TestRulesMultipleRecipients(t *testing.T) {
	// Each recipient's rules are evaluated independently.
	// alice has a move rule matching; bob has no rules.
	// The first matching move should set env.TargetMailbox.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "move promos",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Promo"},
					},
					Action: &bongov1.FilterAction{Type: "move", Target: "Promotions"},
				},
			},
			// bob has no rules.
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com", "bob@example.com"},
	}
	msg := strings.NewReader(minimalMessage(
		"shop@store.example",
		"alice@example.com, bob@example.com",
		"Big Promo Sale",
		"20% off everything.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stage != "rules" {
		t.Errorf("expected stage=rules, got %q", result.Stage)
	}
	if env.TargetMailbox != "Promotions" {
		t.Errorf("expected TargetMailbox=Promotions, got %q", env.TargetMailbox)
	}
}

func TestRulesFlagAndMarkRead(t *testing.T) {
	// Flag and mark_read actions are recorded in Reasons, not as target mailbox changes.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "flag boss emails",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "from", Operator: "contains", Value: "boss@"},
					},
					Action: &bongov1.FilterAction{Type: "flag", Target: "starred"},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com"},
	}
	msg := strings.NewReader(minimalMessage(
		"boss@company.example",
		"alice@example.com",
		"Important meeting",
		"See you at 9.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	if env.TargetMailbox != "" {
		t.Errorf("expected empty TargetMailbox for flag action, got %q", env.TargetMailbox)
	}
	found := false
	for _, r := range result.Reasons {
		if strings.HasPrefix(r, "flag:") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reasons to contain flag entry, got %v", result.Reasons)
	}
}

func TestRulesDisabledRuleIgnored(t *testing.T) {
	// Disabled rules must not fire even if conditions would match.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "disabled mover",
					Enabled: false, // disabled
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Hello"},
					},
					Action: &bongov1.FilterAction{Type: "move", Target: "Archive"},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com"},
	}
	msg := strings.NewReader(minimalMessage("bob@example.com", "alice@example.com", "Hello World", "body"))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass for disabled rule, got %q", result.Action)
	}
	if env.TargetMailbox != "" {
		t.Errorf("expected empty TargetMailbox, got %q", env.TargetMailbox)
	}
}

func TestRulesName(t *testing.T) {
	p := rulesprocessor.NewRulesProcessor(&mockRuleProvider{}, nil)
	if p.Name() != "rules" {
		t.Errorf("expected Name()=rules, got %q", p.Name())
	}
}
