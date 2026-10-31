package rules

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// Engine evaluates filter rules against inbound messages.
type Engine struct{}

// NewEngine creates a new rule evaluation engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Actions returns a rule's actions in order; an empty list reads as the single
// legacy action. See store.RuleActions.
func Actions(rule *bongov1.FilterRule) []*bongov1.FilterAction {
	return store.RuleActions(rule)
}

// SetActions writes a rule's actions in both shapes. See store.SetRuleActions.
func SetActions(rule *bongov1.FilterRule, actions []*bongov1.FilterAction) {
	store.SetRuleActions(rule, actions)
}

// Evaluate returns the first action of the first enabled rule that matches, or
// nil. It answers "does this rule match this message" for the rule tester; the
// delivery pipeline uses MatchingRules, which honours stop_processing.
func (e *Engine) Evaluate(rules []*bongov1.FilterRule, msg *Message) *bongov1.FilterAction {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matchConditions(rule, msg) {
			if acts := Actions(rule); len(acts) > 0 {
				return acts[0]
			}
			return nil
		}
	}
	return nil
}

// MatchingRules returns the enabled rules that match, in the order given (the
// caller sorts by priority). It stops after the first matching rule with
// stop_processing set, which is included.
func (e *Engine) MatchingRules(rules []*bongov1.FilterRule, msg *Message) []*bongov1.FilterRule {
	var matched []*bongov1.FilterRule
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if !matchConditions(rule, msg) {
			continue
		}
		matched = append(matched, rule)
		if rule.StopProcessing {
			break
		}
	}
	return matched
}

// MatchAny is the FilterRule.Match value for "any condition". Every other
// stored value, including "", means all conditions: an unknown value must
// never widen what a rule fires on. Only the save path refuses unknown values.
const MatchAny = "any"

// matchConditions reports whether a rule's conditions hold for a message. Both
// Evaluate (the rule tester) and MatchingRules (delivery) use it. A rule with
// no conditions never matches, in either mode.
func matchConditions(rule *bongov1.FilterRule, msg *Message) bool {
	if len(rule.Conditions) == 0 {
		return false
	}
	anyOf := rule.Match == MatchAny
	for _, cond := range rule.Conditions {
		ok := matchCondition(cond, msg)
		if anyOf && ok {
			return true
		}
		if !anyOf && !ok {
			return false
		}
	}
	return !anyOf
}

func matchCondition(cond *bongov1.Condition, msg *Message) bool {
	if strings.EqualFold(cond.Field, FieldMIMEType) {
		return matchMIMEType(cond, msg.MIMETypes)
	}
	return matchOperator(cond.Operator, resolveField(cond.Field, msg), cond.Value)
}

// FieldMIMEType is the condition field matched against the content type of
// every leaf part of a message. A node from before this field existed resolves
// it to "", so upgrade every node before anyone creates a mime_type rule:
// otherwise "matches" with a pattern that accepts "" would fire there.
const FieldMIMEType = "mime_type"

// HeaderPrefix starts a header condition field: header:Name.
const HeaderPrefix = "header:"

// matchMIMEType holds when any part type satisfies the condition. Only equals,
// contains and matches are honoured: under any-part semantics not_contains is
// almost always true, and an unknown operator must never widen a rule. A
// matches pattern is compiled once, case-insensitively, and tried against
// every part.
func matchMIMEType(cond *bongov1.Condition, types []string) bool {
	var match func(string) bool
	switch strings.ToLower(cond.Operator) {
	case "equals":
		match = func(t string) bool { return strings.EqualFold(t, cond.Value) }
	case "contains":
		want := strings.ToLower(cond.Value)
		match = func(t string) bool { return strings.Contains(t, want) }
	case "matches":
		// Part types are lowercase; ignore case so a pattern with capitals
		// behaves like equals and contains.
		re := compileWithTimeout("(?i)" + cond.Value)
		if re == nil {
			return false
		}
		match = re.MatchString
	default:
		return false
	}
	return slices.ContainsFunc(types, match)
}

func resolveField(field string, msg *Message) string {
	meta := msg.Meta
	switch strings.ToLower(field) {
	case "from":
		return meta.From
	case "to":
		return strings.Join(meta.To, ", ")
	case "cc":
		return strings.Join(meta.Cc, ", ")
	case "subject":
		return meta.Subject
	case "body":
		return msg.BodyText
	case "has_attachment":
		return strconv.FormatBool(len(meta.Attachments) > 0)
	case "size":
		if meta.RawMessage != nil {
			return strconv.FormatInt(meta.RawMessage.Size, 10)
		}
		return "0"
	default:
		// header:X-Foo. The name is matched case-insensitively:
		// Message.Headers keys are lowercased.
		if len(field) > len(HeaderPrefix) && strings.EqualFold(field[:len(HeaderPrefix)], HeaderPrefix) {
			return msg.Headers[strings.ToLower(strings.TrimSpace(field[len(HeaderPrefix):]))]
		}
		return ""
	}
}

func matchOperator(operator, fieldValue, condValue string) bool {
	switch strings.ToLower(operator) {
	case "contains":
		return strings.Contains(strings.ToLower(fieldValue), strings.ToLower(condValue))
	case "not_contains":
		return !strings.Contains(strings.ToLower(fieldValue), strings.ToLower(condValue))
	case "equals":
		return strings.EqualFold(fieldValue, condValue)
	case "matches":
		return regexpMatch(condValue, fieldValue)
	case "greater_than":
		return numericCompare(fieldValue, condValue, func(a, b int64) bool { return a > b })
	case "less_than":
		return numericCompare(fieldValue, condValue, func(a, b int64) bool { return a < b })
	default:
		return false
	}
}

func regexpMatch(pattern, value string) bool {
	re := compileWithTimeout(pattern)
	return re != nil && re.MatchString(value)
}

// compileWithTimeout compiles pattern, giving up after 100ms to prevent ReDoS,
// and returns nil on a bad pattern or a timeout. Callers that test several
// values compile once and reuse the result.
func compileWithTimeout(pattern string) *regexp.Regexp {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan *regexp.Regexp, 1)
	go func() {
		re, err := regexp.Compile(pattern)
		if err != nil {
			done <- nil
			return
		}
		done <- re
	}()

	select {
	case re := <-done:
		return re
	case <-ctx.Done():
		return nil
	}
}

func numericCompare(fieldStr, condStr string, cmp func(int64, int64) bool) bool {
	fieldVal, err := strconv.ParseInt(fieldStr, 10, 64)
	if err != nil {
		return false
	}
	condVal, err := strconv.ParseInt(condStr, 10, 64)
	if err != nil {
		return false
	}
	return cmp(fieldVal, condVal)
}
