package rules_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	rulesprocessor "github.com/bongo-project/bongo/internal/pipeline/rules"
)

// A bounce to the reserved no-relay sender must not run the catch-all owner's
// rules: a forward rule would otherwise send another user's bounce, headers
// included, to the owner's target.
func TestRulesSkipNoRelayAddress(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	if err := ps.Set(store.CatchAllKey("hosted.example"), []byte("owner")); err != nil {
		t.Fatal(err)
	}
	profile, err := proto.Marshal(&bongov1.UserProfile{Email: "owner@hosted.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/owner/profile"), profile); err != nil {
		t.Fatal(err)
	}
	resolver := store.NewRecipientResolver(nil, ps, nil)

	provider := &mockRuleProvider{rules: map[string][]*bongov1.FilterRule{"owner": {{
		Id: "r1", Name: "forward all", Enabled: true,
		Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "Undelivered"}},
		Action:     &bongov1.FilterAction{Type: "forward", Target: "owner@elsewhere.example"},
	}}}}
	p := rulesprocessor.NewRulesProcessor(provider, nil)
	p.SetResolver(resolver)

	for _, tt := range []struct {
		rcpt        string
		wantForward bool
	}{
		{"bongo-bounce-sink@hosted.example", false},
		{"anything@hosted.example", true}, // control: the rule does fire for a catch-all hit
	} {
		env := &bongov1.QueueEnvelope{Recipients: []string{tt.rcpt}}
		raw := minimalMessage("MAILER-DAEMON@dest.example", tt.rcpt, "Undelivered", "bounce")
		if _, err := p.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
			t.Fatalf("Process: %v", err)
		}
		if got := len(env.ForwardRequests) > 0; got != tt.wantForward {
			t.Errorf("%s: forward requested = %v, want %v", tt.rcpt, got, tt.wantForward)
		}
	}
}
