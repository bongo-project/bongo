package rules_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	rulesprocessor "github.com/bongo-project/bongo/internal/pipeline/rules"
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

func TestRulesTagAction(t *testing.T) {
	// A "tag" rule should accumulate tag names in env.PendingTags and record
	// each tag in StageResult.Reasons as "tag:<name>".
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "tag newsletters",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Newsletter"},
					},
					Action: &bongov1.FilterAction{
						Type: "tag",
						Tags: []string{"newsletter", "bulk"},
					},
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
		"Your digest.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	// PendingTags should contain both tags.
	if len(env.PendingTags) != 2 {
		t.Fatalf("expected 2 pending tags, got %v", env.PendingTags)
	}
	if env.PendingTags[0] != "newsletter" || env.PendingTags[1] != "bulk" {
		t.Errorf("unexpected pending tags: %v", env.PendingTags)
	}
	// Reasons should include "tag:newsletter" and "tag:bulk".
	wantReasons := map[string]bool{"tag:newsletter": false, "tag:bulk": false}
	for _, r := range result.Reasons {
		if _, ok := wantReasons[r]; ok {
			wantReasons[r] = true
		}
	}
	for reason, found := range wantReasons {
		if !found {
			t.Errorf("expected reason %q in %v", reason, result.Reasons)
		}
	}
}

func TestRulesTagDeduplication(t *testing.T) {
	// When two recipients both have matching tag rules, PendingTags should
	// deduplicate across both rule results.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Update"},
					},
					Action: &bongov1.FilterAction{Type: "tag", Tags: []string{"updates", "shared"}},
				},
			},
			"bob@example.com": {
				{
					Id:      "r2",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Update"},
					},
					// "shared" is a duplicate; "extra" is new.
					Action: &bongov1.FilterAction{Type: "tag", Tags: []string{"shared", "extra"}},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com", "bob@example.com"},
	}
	msg := strings.NewReader(minimalMessage(
		"sender@example.com",
		"alice@example.com, bob@example.com",
		"Software Update",
		"Details inside.",
	))

	_, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect ["updates", "shared", "extra"] — no duplicate "shared".
	want := []string{"updates", "shared", "extra"}
	if len(env.PendingTags) != len(want) {
		t.Fatalf("expected pending tags %v, got %v", want, env.PendingTags)
	}
	for i, v := range want {
		if env.PendingTags[i] != v {
			t.Errorf("pending tags[%d]: want %q, got %q", i, v, env.PendingTags[i])
		}
	}
}

func TestRulesForwardAction(t *testing.T) {
	// A "forward" rule should populate env.ForwardRequests and record it in reasons.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Name:    "forward newsletters",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "subject", Operator: "contains", Value: "Newsletter"},
					},
					Action: &bongov1.FilterAction{
						Type:   "forward",
						Target: "external@other.com",
					},
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
		"Your digest.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	// ForwardRequests should carry the target, attributed to the rule's owner
	// (the recipient itself here: this processor has no resolver).
	if len(env.ForwardRequests) != 1 {
		t.Fatalf("expected 1 forward request, got %v", env.ForwardRequests)
	}
	if got := env.ForwardRequests[0]; got.UserId != "alice@example.com" || got.Target != "external@other.com" {
		t.Errorf("forward request = %v, want alice@example.com -> external@other.com", got)
	}
	if len(env.ForwardAddresses) != 0 {
		t.Errorf("legacy unattributed ForwardAddresses = %v, want none", env.ForwardAddresses)
	}
	// Reasons should include "forward:external@other.com".
	found := false
	for _, r := range result.Reasons {
		if r == "forward:external@other.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reason 'forward:external@other.com' in %v", result.Reasons)
	}
	// TargetMailbox should not be changed.
	if env.TargetMailbox != "" {
		t.Errorf("forward action must not set TargetMailbox, got %q", env.TargetMailbox)
	}
}

type mapResolver map[string]string

func (m mapResolver) ResolveRecipient(email string) (string, error) {
	if id, ok := m[email]; ok {
		return id, nil
	}
	return "", errors.New("not local")
}

// Each forward request carries its own rule owner's resolved user ID, not the
// first recipient's or the recipient address.
func TestRulesForwardRequestsAttributedPerOwner(t *testing.T) {
	fwd := func(target string) []*bongov1.FilterRule {
		return []*bongov1.FilterRule{{
			Id: "r", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "Hello"}},
			Action:     &bongov1.FilterAction{Type: "forward", Target: target},
		}}
	}
	provider := &mockRuleProvider{rules: map[string][]*bongov1.FilterRule{
		"user-alice": fwd("a@other.com"),
		"user-bob":   fwd("b@other.com"),
	}}
	p := rulesprocessor.NewRulesProcessor(provider, nil)
	p.SetResolver(mapResolver{"alice@example.com": "user-alice", "bob@example.com": "user-bob"})
	env := &bongov1.QueueEnvelope{Recipients: []string{"alice@example.com", "bob@example.com"}}
	if _, err := p.Process(context.Background(), env, strings.NewReader(minimalMessage("x@y.com", "alice@example.com", "Hello", "hi"))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := map[string]string{}
	for _, r := range env.ForwardRequests {
		got[r.Target] = r.UserId
	}
	if len(env.ForwardRequests) != 2 || got["a@other.com"] != "user-alice" || got["b@other.com"] != "user-bob" {
		t.Fatalf("forward requests = %v, want a->user-alice and b->user-bob", env.ForwardRequests)
	}
}

func TestRulesTagNoMailboxChange(t *testing.T) {
	// A "tag" action must not change env.TargetMailbox.
	provider := &mockRuleProvider{
		rules: map[string][]*bongov1.FilterRule{
			"alice@example.com": {
				{
					Id:      "r1",
					Enabled: true,
					Conditions: []*bongov1.Condition{
						{Field: "from", Operator: "contains", Value: "promo@"},
					},
					Action: &bongov1.FilterAction{Type: "tag", Tags: []string{"promo"}},
				},
			},
		},
	}

	p := rulesprocessor.NewRulesProcessor(provider, nil)
	env := &bongov1.QueueEnvelope{
		Recipients: []string{"alice@example.com"},
	}
	msg := strings.NewReader(minimalMessage(
		"promo@store.example",
		"alice@example.com",
		"Big sale",
		"Check it out.",
	))

	result, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q", result.Action)
	}
	if env.TargetMailbox != "" {
		t.Errorf("tag action must not set TargetMailbox, got %q", env.TargetMailbox)
	}
	if len(env.PendingTags) != 1 || env.PendingTags[0] != "promo" {
		t.Errorf("expected pending tags [promo], got %v", env.PendingTags)
	}
}
