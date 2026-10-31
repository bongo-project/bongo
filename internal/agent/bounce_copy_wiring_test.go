package agent

import "testing"

// The bounce copy goes to postmaster@<domain> only for a hosted, enabled
// domain. A disabled or unknown domain falls back to smtp.postmaster_account.
func TestBounceCopyHostedDomain(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	for k, v := range map[string]string{
		"/system/domain/on.bcopy.test":  "enabled",
		"/system/domain/off.bcopy.test": "disabled",
	} {
		if err := a.Store.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		domain string
		want   bool
	}{
		{"on.bcopy.test", true},
		{"off.bcopy.test", false},
		{"unknown.bcopy.test", false},
	} {
		if got := a.bounceCopyHostedDomain(tc.domain); got != tc.want {
			t.Errorf("bounceCopyHostedDomain(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}

	var nilStore Agent
	if nilStore.bounceCopyHostedDomain("on.bcopy.test") {
		t.Error("an agent without a store reports a hosted domain")
	}
}
