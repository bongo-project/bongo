package rules

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestMatchModes(t *testing.T) {
	two := []*bongov1.Condition{
		{Field: "from", Operator: "contains", Value: "a@"},
		{Field: "subject", Operator: "contains", Value: "b"},
	}
	bothMeta := &bongov1.MailMeta{From: "a@x.com", Subject: "b"}
	fromOnly := &bongov1.MailMeta{From: "a@x.com", Subject: "zzz"}
	subjOnly := &bongov1.MailMeta{From: "q@x.com", Subject: "b"}
	neither := &bongov1.MailMeta{From: "q@x.com", Subject: "zzz"}
	tests := []struct {
		name  string
		match string
		conds []*bongov1.Condition
		meta  *bongov1.MailMeta
		want  bool
	}{
		{"any: both", "any", two, bothMeta, true},
		{"any: from only", "any", two, fromOnly, true},
		{"any: subject only", "any", two, subjOnly, true},
		{"any: neither", "any", two, neither, false},
		{"all: both", "all", two, bothMeta, true},
		{"all: from only", "all", two, fromOnly, false},
		{"all: subject only", "all", two, subjOnly, false},
		{"empty reads as all: both", "", two, bothMeta, true},
		{"empty reads as all: one", "", two, fromOnly, false},
		{"unknown value fails closed (as all)", "ANY", two, fromOnly, false},
		{"unknown value fails closed (as all) 2", "or", two, subjOnly, false},
		{"any: no conditions", "any", nil, bothMeta, false},
		{"all: no conditions", "all", nil, bothMeta, false},
		{"empty: no conditions", "", nil, bothMeta, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := &bongov1.FilterRule{
				Id: "r", Enabled: true, Match: tc.match, Conditions: tc.conds,
				Action: &bongov1.FilterAction{Type: "flag"},
			}
			e := NewEngine()
			if got := e.Evaluate([]*bongov1.FilterRule{rule}, tc.meta, nil, "") != nil; got != tc.want {
				t.Errorf("Evaluate = %v, want %v", got, tc.want)
			}
			if got := len(e.MatchingRules([]*bongov1.FilterRule{rule}, tc.meta, nil, "")) == 1; got != tc.want {
				t.Errorf("MatchingRules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchingRulesAnyStopProcessing(t *testing.T) {
	conds := []*bongov1.Condition{
		{Field: "from", Operator: "contains", Value: "nobody"},
		{Field: "subject", Operator: "contains", Value: "hi"},
	}
	rules := []*bongov1.FilterRule{
		{Id: "A", Enabled: true, Match: "any", StopProcessing: true, Conditions: conds},
		{Id: "B", Enabled: true, Match: "any", Conditions: conds},
	}
	got := NewEngine().MatchingRules(rules, &bongov1.MailMeta{From: "x@y", Subject: "hi"}, nil, "")
	if len(got) != 1 || got[0].Id != "A" {
		t.Errorf("want only A, got %v", got)
	}
}
