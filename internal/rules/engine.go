package rules

import (
	"context"
	"regexp"
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
func (e *Engine) Evaluate(rules []*bongov1.FilterRule, meta *bongov1.MailMeta, headers map[string]string, bodyText string) *bongov1.FilterAction {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matchConditions(rule, meta, headers, bodyText) {
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
func (e *Engine) MatchingRules(rules []*bongov1.FilterRule, meta *bongov1.MailMeta, headers map[string]string, bodyText string) []*bongov1.FilterRule {
	var matched []*bongov1.FilterRule
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if !matchConditions(rule, meta, headers, bodyText) {
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
func matchConditions(rule *bongov1.FilterRule, meta *bongov1.MailMeta, headers map[string]string, bodyText string) bool {
	if len(rule.Conditions) == 0 {
		return false
	}
	anyOf := rule.Match == MatchAny
	for _, cond := range rule.Conditions {
		ok := matchCondition(cond, meta, headers, bodyText)
		if anyOf && ok {
			return true
		}
		if !anyOf && !ok {
			return false
		}
	}
	return !anyOf
}

func matchCondition(cond *bongov1.Condition, meta *bongov1.MailMeta, headers map[string]string, bodyText string) bool {
	fieldValue := resolveField(cond.Field, meta, headers, bodyText)
	return matchOperator(cond.Operator, fieldValue, cond.Value)
}

func resolveField(field string, meta *bongov1.MailMeta, headers map[string]string, bodyText string) string {
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
		return bodyText
	case "has_attachment":
		return strconv.FormatBool(len(meta.Attachments) > 0)
	case "size":
		if meta.RawMessage != nil {
			return strconv.FormatInt(meta.RawMessage.Size, 10)
		}
		return "0"
	default:
		// header:X-Foo
		if strings.HasPrefix(strings.ToLower(field), "header:") {
			headerName := field[7:]
			return headers[headerName]
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
	// Compile with a timeout to prevent ReDoS.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		re, err := regexp.Compile(pattern)
		if err != nil {
			done <- false
			return
		}
		done <- re.MatchString(value)
	}()

	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		return false
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
