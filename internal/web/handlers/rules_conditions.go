package handlers

import (
	"net/url"
	"slices"
	"strings"

	"github.com/bongo-project/bongo/internal/rules"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	// maxRuleConditions bounds the condition rows of one rule.
	maxRuleConditions = 20
	// maxConditionValue bounds one condition value in bytes.
	maxConditionValue = 1024
	// maxHeaderName bounds a header condition's name. RFC 5322 recommends
	// at most 78 characters per line, name and colon included.
	maxHeaderName = 76
)

var (
	textOperators = []string{"contains", "not_contains", "equals", "matches"}
	// fieldOperators lists the operators the engine honours per field, so the
	// stored data matches what delivery does. A header:Name field takes the
	// text operators.
	fieldOperators = map[string][]string{
		"from":              textOperators,
		"to":                textOperators,
		"cc":                textOperators,
		"subject":           textOperators,
		"body":              textOperators,
		"has_attachment":    {"equals"},
		"size":              {"greater_than", "less_than"},
		rules.FieldMIMEType: {"equals", "contains", "matches"},
	}
)

// parseRuleConditions reads the editor's positional condition fields. Each row
// posts cond_field, cond_operator and cond_value exactly once, so the three
// lists must be the same length. A header row posts "header:" and the name in
// cond_field. It returns the i18n key of the error to show, or "". Every row
// is validated before the caller writes anything.
func parseRuleConditions(form url.Values) ([]*bongov1.Condition, string) {
	fields, operators, values := form["cond_field"], form["cond_operator"], form["cond_value"]
	if len(operators) != len(fields) || len(values) != len(fields) {
		return nil, "rules.errConditionField"
	}
	if len(fields) > maxRuleConditions {
		return nil, "rules.errConditionLimit"
	}

	conds := make([]*bongov1.Condition, 0, len(fields))
	for i, field := range fields {
		ops, ok := operatorsForField(field)
		if !ok {
			if strings.HasPrefix(field, rules.HeaderPrefix) {
				return nil, "rules.errHeaderName"
			}
			return nil, "rules.errConditionField"
		}
		if !slices.Contains(ops, operators[i]) {
			return nil, "rules.errConditionField"
		}
		if len(values[i]) > maxConditionValue {
			return nil, "rules.errConditionLimit"
		}
		if field == "has_attachment" && values[i] != "true" && values[i] != "false" {
			return nil, "rules.errConditionField"
		}
		conds = append(conds, &bongov1.Condition{Field: field, Operator: operators[i], Value: values[i]})
	}
	return conds, ""
}

// operatorsForField returns the operators a field accepts, and false for a
// field outside the known set or a header whose name is not a valid field name.
func operatorsForField(field string) ([]string, bool) {
	if name, ok := strings.CutPrefix(field, rules.HeaderPrefix); ok {
		return textOperators, validHeaderName(name)
	}
	ops, ok := fieldOperators[field]
	return ops, ok
}

// validHeaderName reports whether name is an RFC 5322 field name: one or more
// printable US-ASCII characters (33 to 126) other than colon. That excludes
// whitespace, control characters (CR and LF among them) and non-ASCII bytes.
// The name is stored and rendered in rule summaries, and must never carry
// anything that could be read as a header line break.
func validHeaderName(name string) bool {
	if name == "" || len(name) > maxHeaderName {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}
