package rules_test

import (
	"context"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	rulesprocessor "github.com/bongo-project/bongo/internal/pipeline/rules"
	"github.com/bongo-project/bongo/internal/store"
)

func act(typ, target string) *bongov1.FilterAction {
	return &bongov1.FilterAction{Type: typ, Target: target}
}

// matchSubject is a rule matching the subject "hello", with the given actions
// in the new shape (both fields, as the form writes them).
func matchRule(id string, priority int32, stop bool, actions ...*bongov1.FilterAction) *bongov1.FilterRule {
	r := &bongov1.FilterRule{
		Id: id, Priority: priority, Enabled: true, StopProcessing: stop,
		Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "hello"}},
		Actions:    actions,
	}
	if len(actions) > 0 {
		r.Action = actions[0]
	}
	return r
}

func runRules(t *testing.T, env *bongov1.QueueEnvelope, rules ...*bongov1.FilterRule) *bongov1.StageResult {
	t.Helper()
	p := rulesprocessor.NewRulesProcessor(&mockRuleProvider{rules: map[string][]*bongov1.FilterRule{"alice@example.com": rules}}, nil)
	env.Recipients = []string{"alice@example.com"}
	msg := strings.NewReader(minimalMessage("bob@example.com", "alice@example.com", "hello there", "body"))
	res, err := p.Process(context.Background(), env, msg)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return res
}

func TestRulesStopProcessing(t *testing.T) {
	a := func(stop bool) *bongov1.FilterRule { return matchRule("A", 1, stop, act("copy", "FromA")) }
	b := matchRule("B", 2, false, act("copy", "FromB"))

	t.Run("A without stop lets B run", func(t *testing.T) {
		env := &bongov1.QueueEnvelope{}
		runRules(t, env, a(false), b)
		if got := strings.Join(env.CopyMailboxes, ","); got != "FromA,FromB" {
			t.Errorf("copies = %q, want FromA,FromB", got)
		}
	})
	t.Run("A with stop hides B", func(t *testing.T) {
		env := &bongov1.QueueEnvelope{}
		runRules(t, env, a(true), b)
		if got := strings.Join(env.CopyMailboxes, ","); got != "FromA" {
			t.Errorf("copies = %q, want FromA", got)
		}
	})
	t.Run("disabled rule does not stop", func(t *testing.T) {
		off := a(true)
		off.Enabled = false
		env := &bongov1.QueueEnvelope{}
		runRules(t, env, off, b)
		if got := strings.Join(env.CopyMailboxes, ","); got != "FromB" {
			t.Errorf("copies = %q, want FromB", got)
		}
	})
}

func TestRulesTerminalAndNonTerminal(t *testing.T) {
	tests := []struct {
		name       string
		rules      []*bongov1.FilterRule
		wantAction string
		wantTarget string
		wantCopies string
		wantFlags  uint32
		wantFwds   int
		wantDrop   bool
	}{
		{
			name:       "move wins over a later delete in one rule",
			rules:      []*bongov1.FilterRule{matchRule("A", 1, false, act("move", "X"), act("delete", ""))},
			wantAction: "pass", wantTarget: "X",
		},
		{
			name:       "delete wins over a later move in one rule",
			rules:      []*bongov1.FilterRule{matchRule("A", 1, false, act("delete", ""), act("move", "X"))},
			wantAction: "reject",
		},
		{
			name: "move in A then delete in B: move wins",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("move", "X")),
				matchRule("B", 2, false, act("delete", "")),
			},
			wantAction: "pass", wantTarget: "X",
		},
		{
			name: "delete in A then move in B: delete wins",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("delete", "")),
				matchRule("B", 2, false, act("move", "X")),
			},
			wantAction: "reject",
		},
		{
			name: "first of two moves wins",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("move", "X")),
				matchRule("B", 2, false, act("move", "Y")),
			},
			wantAction: "pass", wantTarget: "X",
		},
		{
			name: "non-terminal actions accumulate across rules",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("flag", ""), act("copy", "C1")),
				matchRule("B", 2, false, act("mark_read", ""), act("copy", "C2"), act("copy", "c1")),
			},
			wantAction: "pass", wantCopies: "C1,C2", wantFlags: store.FlagFlagged | store.FlagSeen,
		},
		{
			name:       "move plus copy",
			rules:      []*bongov1.FilterRule{matchRule("A", 1, false, act("copy", "Backup"), act("move", "Work"))},
			wantAction: "pass", wantTarget: "Work", wantCopies: "Backup",
		},
		{
			name: "delete with a copy passes and discards the normal destination",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("copy", "Backup"), act("delete", "")),
			},
			wantAction: "pass", wantCopies: "Backup", wantDrop: true,
		},
		{
			name: "delete with a forward passes and discards the normal destination",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("forward", "me@elsewhere.example"), act("delete", "")),
			},
			wantAction: "pass", wantFwds: 1, wantDrop: true,
		},
		{
			name: "delete with only flags still rejects",
			rules: []*bongov1.FilterRule{
				matchRule("A", 1, false, act("flag", ""), act("delete", "")),
			},
			wantAction: "reject", wantFlags: store.FlagFlagged,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := &bongov1.QueueEnvelope{}
			res := runRules(t, env, tc.rules...)
			if res.Action != tc.wantAction {
				t.Errorf("action = %q, want %q", res.Action, tc.wantAction)
			}
			if env.TargetMailbox != tc.wantTarget {
				t.Errorf("target = %q, want %q", env.TargetMailbox, tc.wantTarget)
			}
			if got := strings.Join(env.CopyMailboxes, ","); got != tc.wantCopies {
				t.Errorf("copies = %q, want %q", got, tc.wantCopies)
			}
			if env.PendingFlags != tc.wantFlags {
				t.Errorf("flags = %d, want %d", env.PendingFlags, tc.wantFlags)
			}
			if len(env.ForwardRequests) != tc.wantFwds {
				t.Errorf("forward requests = %d, want %d", len(env.ForwardRequests), tc.wantFwds)
			}
			if env.DiscardNormal != tc.wantDrop {
				t.Errorf("discard_normal = %v, want %v", env.DiscardNormal, tc.wantDrop)
			}
		})
	}
}

func TestRulesSpamStaysInJunk(t *testing.T) {
	tests := []struct {
		name       string
		incoming   string
		rule       *bongov1.FilterRule
		wantTarget string
		wantCopies string
	}{
		{"move elsewhere keeps Junk", "Junk", matchRule("A", 1, false, act("move", "Work")), "Junk", ""},
		{"move to Trash is honoured", "Junk", matchRule("A", 1, false, act("move", "trash")), "trash", ""},
		{"move to Junk is honoured", "Junk", matchRule("A", 1, false, act("move", "JUNK")), "JUNK", ""},
		{"move of ham is honoured", "", matchRule("A", 1, false, act("move", "Work")), "Work", ""},
		{"move of INBOX mail (a released message) is honoured", "INBOX", matchRule("A", 1, false, act("move", "Work")), "Work", ""},
		{"copy of spam elsewhere is dropped", "Junk", matchRule("A", 1, false, act("copy", "Backup")), "Junk", ""},
		{"copy of spam to Trash is kept", "Junk", matchRule("A", 1, false, act("copy", "Trash")), "Junk", "Trash"},
		{"copy of ham is kept", "", matchRule("A", 1, false, act("copy", "Backup")), "", "Backup"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := &bongov1.QueueEnvelope{TargetMailbox: tc.incoming}
			runRules(t, env, tc.rule)
			if env.TargetMailbox != tc.wantTarget {
				t.Errorf("target = %q, want %q", env.TargetMailbox, tc.wantTarget)
			}
			if got := strings.Join(env.CopyMailboxes, ","); got != tc.wantCopies {
				t.Errorf("copies = %q, want %q", got, tc.wantCopies)
			}
		})
	}
}

// TestRulesLegacyShapeReadsAction: a rule written before the actions list has
// only field 6 and must behave as a one-action rule.
func TestRulesLegacyShapeReadsAction(t *testing.T) {
	legacy := &bongov1.FilterRule{
		Id: "old", Priority: 1, Enabled: true,
		Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "hello"}},
		Action:     act("move", "Old"),
	}
	env := &bongov1.QueueEnvelope{}
	runRules(t, env, legacy)
	if env.TargetMailbox != "Old" {
		t.Errorf("target = %q, want Old", env.TargetMailbox)
	}
}

// TestRulesActionCapIsEnforced: a rule stored with more than ten actions
// (written by something other than the form) runs only the first ten.
func TestRulesActionCapIsEnforced(t *testing.T) {
	var acts []*bongov1.FilterAction
	for i := 0; i < store.MaxRuleActions; i++ {
		acts = append(acts, act("tag", ""))
	}
	acts = append(acts, act("copy", "TooFar"))
	env := &bongov1.QueueEnvelope{}
	runRules(t, env, matchRule("A", 1, false, acts...))
	if len(env.CopyMailboxes) != 0 {
		t.Errorf("copies = %v, want none (11th action ignored)", env.CopyMailboxes)
	}
}
