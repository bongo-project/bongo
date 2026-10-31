package store

import bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

// MaxRuleActions is the most actions one filter rule may hold.
const MaxRuleActions = 10

// RuleActions returns a rule's actions in order. A rule written before
// FilterRule.actions existed has only the single action in field 6, so an
// empty list reads as that one action. It lives here, not in internal/rules,
// because the forward-target scan in this package needs it and rules imports
// store.
func RuleActions(rule *bongov1.FilterRule) []*bongov1.FilterAction {
	if rule == nil {
		return nil
	}
	if len(rule.Actions) > 0 {
		return rule.Actions
	}
	if rule.Action != nil {
		return []*bongov1.FilterAction{rule.Action}
	}
	return nil
}

// SetRuleActions writes a rule's actions in both shapes: the list, and field 6
// holding the first, so a node that predates the list still reads something
// sensible. An empty list clears both.
func SetRuleActions(rule *bongov1.FilterRule, actions []*bongov1.FilterAction) {
	rule.Actions = actions
	if len(actions) == 0 {
		rule.Action = nil
		return
	}
	rule.Action = actions[0]
}
