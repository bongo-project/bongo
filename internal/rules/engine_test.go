package rules

import (
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestEvaluate_MatchesFromContains(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id:      "r1",
			Name:    "Newsletters",
			Enabled: true,
			Conditions: []*bongov1.Condition{
				{Field: "from", Operator: "contains", Value: "newsletter"},
			},
			Action: &bongov1.FilterAction{Type: "move", Target: "Updates"},
		},
	}
	meta := &bongov1.MailMeta{From: "newsletter@example.com", Subject: "Weekly digest"}

	action := engine.Evaluate(rules, meta, nil, "")
	if action == nil {
		t.Fatal("expected action, got nil")
	}
	if action.Type != "move" || action.Target != "Updates" {
		t.Errorf("got action type=%q target=%q, want move/Updates", action.Type, action.Target)
	}
}

func TestEvaluate_ANDConditions(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id:      "r1",
			Name:    "Urgent from boss",
			Enabled: true,
			Conditions: []*bongov1.Condition{
				{Field: "from", Operator: "contains", Value: "boss@"},
				{Field: "subject", Operator: "contains", Value: "urgent"},
			},
			Action: &bongov1.FilterAction{Type: "move", Target: "Important"},
		},
	}

	// Both match.
	meta := &bongov1.MailMeta{From: "boss@company.com", Subject: "Urgent: review needed"}
	if action := engine.Evaluate(rules, meta, nil, ""); action == nil {
		t.Fatal("expected action when both conditions match")
	}

	// Only one matches.
	meta2 := &bongov1.MailMeta{From: "boss@company.com", Subject: "FYI: updates"}
	if action := engine.Evaluate(rules, meta2, nil, ""); action != nil {
		t.Fatal("expected nil when only one condition matches")
	}
}

func TestEvaluate_DisabledRuleSkipped(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id:      "r1",
			Enabled: false,
			Conditions: []*bongov1.Condition{
				{Field: "from", Operator: "contains", Value: "test"},
			},
			Action: &bongov1.FilterAction{Type: "move", Target: "Trash"},
		},
	}
	meta := &bongov1.MailMeta{From: "test@example.com"}

	if action := engine.Evaluate(rules, meta, nil, ""); action != nil {
		t.Fatal("disabled rule should not match")
	}
}

func TestEvaluate_StopProcessing(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Priority: 1, Enabled: true,
			Conditions:     []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "urgent"}},
			Action:         &bongov1.FilterAction{Type: "move", Target: "Important"},
			StopProcessing: true,
		},
		{
			Id: "r2", Priority: 2, Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "from", Operator: "contains", Value: "boss@"}},
			Action:     &bongov1.FilterAction{Type: "flag", Target: "starred"},
		},
	}

	meta := &bongov1.MailMeta{From: "boss@company.com", Subject: "Urgent task"}
	action := engine.Evaluate(rules, meta, nil, "")
	if action == nil {
		t.Fatal("expected action")
	}
	if action.Target != "Important" {
		t.Errorf("expected first rule to match, got target=%q", action.Target)
	}
}

func TestEvaluate_NoMatchReturnsNil(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "from", Operator: "contains", Value: "newsletter"}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Updates"},
		},
	}
	meta := &bongov1.MailMeta{From: "friend@example.com"}

	if action := engine.Evaluate(rules, meta, nil, ""); action != nil {
		t.Fatal("expected nil when no rules match")
	}
}

func TestEvaluate_RegexMatching(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "subject", Operator: "matches", Value: `\[PATCH \d+/\d+\]`}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Patches"},
		},
	}
	meta := &bongov1.MailMeta{Subject: "[PATCH 1/3] Fix memory leak"}

	if action := engine.Evaluate(rules, meta, nil, ""); action == nil {
		t.Fatal("expected regex to match")
	}
}

func TestEvaluate_CaseInsensitiveContains(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "from", Operator: "contains", Value: "NEWSLETTER"}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Updates"},
		},
	}
	meta := &bongov1.MailMeta{From: "newsletter@example.com"}

	if action := engine.Evaluate(rules, meta, nil, ""); action == nil {
		t.Fatal("expected case-insensitive match")
	}
}

func TestEvaluate_NotContains(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "subject", Operator: "not_contains", Value: "spam"}},
			Action:     &bongov1.FilterAction{Type: "flag", Target: "important"},
		},
	}
	meta := &bongov1.MailMeta{Subject: "Important meeting"}

	if action := engine.Evaluate(rules, meta, nil, ""); action == nil {
		t.Fatal("expected not_contains to match")
	}

	meta2 := &bongov1.MailMeta{Subject: "This is spam"}
	if action := engine.Evaluate(rules, meta2, nil, ""); action != nil {
		t.Fatal("expected not_contains to not match when value present")
	}
}

func TestEvaluate_SizeComparison(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "size", Operator: "greater_than", Value: "1000000"}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Large"},
		},
	}
	meta := &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Size: 2000000},
	}

	if action := engine.Evaluate(rules, meta, nil, ""); action == nil {
		t.Fatal("expected size > 1MB to match")
	}

	meta2 := &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Size: 500},
	}
	if action := engine.Evaluate(rules, meta2, nil, ""); action != nil {
		t.Fatal("expected size 500 to not match > 1MB")
	}
}

func TestEvaluate_HeaderField(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "header:X-List-Id", Operator: "contains", Value: "dev-list"}},
			Action:     &bongov1.FilterAction{Type: "move", Target: "Lists"},
		},
	}
	meta := &bongov1.MailMeta{From: "someone@example.com"}
	headers := map[string]string{"X-List-Id": "<dev-list.example.com>"}

	if action := engine.Evaluate(rules, meta, headers, ""); action == nil {
		t.Fatal("expected header match")
	}
}

func TestEvaluate_HasAttachment(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{{Field: "has_attachment", Operator: "equals", Value: "true"}},
			Action:     &bongov1.FilterAction{Type: "flag", Target: "attachment"},
		},
	}
	meta := &bongov1.MailMeta{
		Attachments: []*bongov1.BlobRef{{Hash: "abc"}},
	}

	if action := engine.Evaluate(rules, meta, nil, ""); action == nil {
		t.Fatal("expected has_attachment match")
	}
}

func TestEvaluate_EmptyConditions(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: nil,
			Action:     &bongov1.FilterAction{Type: "move", Target: "Somewhere"},
		},
	}
	meta := &bongov1.MailMeta{From: "test@example.com"}

	if action := engine.Evaluate(rules, meta, nil, ""); action != nil {
		t.Fatal("rule with empty conditions should not match")
	}
}

func TestEvaluate_TagAction(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{
				{Field: "subject", Operator: "contains", Value: "newsletter"},
			},
			Action: &bongov1.FilterAction{
				Type: "tag",
				Tags: []string{"newsletter", "bulk"},
			},
		},
	}
	meta := &bongov1.MailMeta{Subject: "Your newsletter is here"}

	action := engine.Evaluate(rules, meta, nil, "")
	if action == nil {
		t.Fatal("expected tag action, got nil")
	}
	if action.Type != "tag" {
		t.Errorf("expected type=tag, got %q", action.Type)
	}
	if len(action.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %v", action.Tags)
	}
	if action.Tags[0] != "newsletter" || action.Tags[1] != "bulk" {
		t.Errorf("unexpected tags: %v", action.Tags)
	}
}

func TestEvaluate_TagActionNoMatch(t *testing.T) {
	engine := NewEngine()
	rules := []*bongov1.FilterRule{
		{
			Id: "r1", Enabled: true,
			Conditions: []*bongov1.Condition{
				{Field: "subject", Operator: "contains", Value: "newsletter"},
			},
			Action: &bongov1.FilterAction{
				Type: "tag",
				Tags: []string{"newsletter"},
			},
		},
	}
	meta := &bongov1.MailMeta{Subject: "Hello from a friend"}

	if action := engine.Evaluate(rules, meta, nil, ""); action != nil {
		t.Fatal("expected nil when subject does not match")
	}
}
