package rules

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// Engine evaluates filter rules against inbound messages.
type Engine struct{}

// NewEngine creates a new rule evaluation engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Evaluate runs filter rules against a message and returns the first matching action.
// Returns nil if no rules match.
func (e *Engine) Evaluate(rules []*bongov1.FilterRule, meta *bongov1.MailMeta, headers map[string]string, bodyText string) *bongov1.FilterAction {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matchAllConditions(rule.Conditions, meta, headers, bodyText) {
			return rule.Action
		}
		// stop_processing only applies when a rule matches
	}
	return nil
}

// EvaluateWithStop is like Evaluate but also returns whether stop_processing was set.
func (e *Engine) EvaluateWithStop(rules []*bongov1.FilterRule, meta *bongov1.MailMeta, headers map[string]string, bodyText string) (*bongov1.FilterAction, bool) {
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if matchAllConditions(rule.Conditions, meta, headers, bodyText) {
			return rule.Action, rule.StopProcessing
		}
	}
	return nil, false
}

func matchAllConditions(conditions []*bongov1.Condition, meta *bongov1.MailMeta, headers map[string]string, bodyText string) bool {
	if len(conditions) == 0 {
		return false
	}
	for _, cond := range conditions {
		if !matchCondition(cond, meta, headers, bodyText) {
			return false
		}
	}
	return true
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
