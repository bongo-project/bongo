package templates

import (
	"context"
	"strings"
)

var ruleFieldKeys = map[string]string{
	"from":           "rules.fieldFrom",
	"to":             "rules.fieldTo",
	"cc":             "rules.fieldCc",
	"subject":        "rules.fieldSubject",
	"body":           "rules.fieldBody",
	"has_attachment": "rules.fieldHasAttachment",
	"size":           "rules.fieldSize",
}

var ruleOperatorKeys = map[string]string{
	"contains":     "rules.opContains",
	"not_contains": "rules.opNotContains",
	"equals":       "rules.opEquals",
	"matches":      "rules.opMatches",
	"greater_than": "rules.opGreaterThan",
	"less_than":    "rules.opLessThan",
}

var ruleActionKeys = map[string]string{
	"move":      "rules.actionMove",
	"copy":      "rules.actionCopy",
	"forward":   "rules.actionForward",
	"flag":      "rules.actionFlag",
	"mark_read": "rules.actionMarkRead",
	"delete":    "rules.actionDelete",
	"tag":       "rules.actionTag",
}

// ruleConditionText renders one condition as "Field operator value" in the
// request locale. Unknown fields and operators fall back to their raw text so
// a rule written by a newer node is still readable.
func ruleConditionText(ctx context.Context, c ConditionInfo) string {
	name := strings.ToLower(c.Field)
	var field string
	switch {
	case ruleFieldKeys[name] != "":
		field = t(ctx, ruleFieldKeys[name])
	case len(c.Field) > len("header:") && strings.EqualFold(c.Field[:len("header:")], "header:"):
		field = t(ctx, "rules.fieldHeader") + " " + c.Field[len("header:"):]
	default:
		field = c.Field
	}
	op := c.Operator
	if k := ruleOperatorKeys[strings.ToLower(c.Operator)]; k != "" {
		op = t(ctx, k)
	}
	value := c.Value
	if name == "has_attachment" {
		if strings.EqualFold(c.Value, "true") {
			value = t(ctx, "common.yes")
		} else {
			value = t(ctx, "common.no")
		}
	}
	return strings.TrimSpace(strings.Join(nonEmpty(field, op, value), " "))
}

// ruleActionText renders a rule action as its label, followed by the target
// folder, address or tag list when the action has one.
func ruleActionText(ctx context.Context, a ActionInfo) string {
	if a.Type == "" {
		return t(ctx, "rules.actionNone")
	}
	kind := strings.ToLower(a.Type)
	label := a.Type
	if k := ruleActionKeys[kind]; k != "" {
		label = t(ctx, k)
	}
	var detail string
	switch kind {
	case "move", "copy", "forward":
		detail = a.Target
	case "tag":
		detail = strings.Join(a.Tags, ", ")
	case "flag", "mark_read", "delete":
	default:
		detail = a.Target
	}
	if detail == "" {
		return label
	}
	return label + ": " + detail
}

func nonEmpty(parts ...string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
