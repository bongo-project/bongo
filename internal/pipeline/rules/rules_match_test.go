package rules_test

import (
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestRulesMatchAnyFiresOnDelivery(t *testing.T) {
	mk := func(match string) *bongov1.FilterRule {
		r := matchRule("A", 1, false, act("copy", "Hit"))
		// The subject condition matches ("hello"); the sender one does not.
		r.Conditions = append(r.Conditions, &bongov1.Condition{Field: "from", Operator: "contains", Value: "nobody@"})
		r.Match = match
		return r
	}
	tests := []struct{ match, want string }{
		{"any", "Hit"},
		{"all", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run("match="+tc.match, func(t *testing.T) {
			env := &bongov1.QueueEnvelope{}
			runRules(t, env, mk(tc.match))
			if got := strings.Join(env.CopyMailboxes, ","); got != tc.want {
				t.Errorf("copies = %q, want %q", got, tc.want)
			}
		})
	}
}
