package deliver

import (
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestVacationRepliesWhenSendAsIdentityIsOnlyInToCc: the user's send-as
// identity is neither their primary address nor an alias, so only the
// authoriser branch of vacationAddressedTo can recognise it. The cases without
// the grant are refused, which proves the input is otherwise rejected.
func TestVacationRepliesWhenSendAsIdentityIsOnlyInToCc(t *testing.T) {
	const identity = "team@other.test"
	tests := []struct {
		name    string
		headers string
		granted bool
		want    int
	}{
		{"identity in To", "To: " + identity + "\r\n", true, 1},
		{"identity in Cc", "To: x@else.test\r\nCc: " + identity + "\r\n", true, 1},
		{"identity in To without grant", "To: " + identity + "\r\n", false, 0},
		{"identity in Cc without grant", "To: x@else.test\r\nCc: " + identity + "\r\n", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newVacHarness(t)
			if tt.granted {
				h.authz["user-alice|"+identity] = true
			}
			env, raw := extMsg("bob@example.net")
			raw = strings.Replace(raw, "To: alice@example.com\r\n", tt.headers, 1)
			if strings.Contains(raw, "To: alice@example.com") || strings.Contains(raw, "alias@example.com") {
				t.Fatal("test input must not name the account's own addresses in To/Cc")
			}
			if err := h.send(env, raw); err != nil {
				t.Fatal(err)
			}
			replies := h.autoReplies()
			if len(replies) != tt.want {
				t.Fatalf("replies = %d, want %d", len(replies), tt.want)
			}
			if tt.want == 1 && (len(replies[0].Recipients) != 1 || replies[0].Recipients[0] != "bob@example.net") {
				t.Errorf("reply recipients = %v, want [bob@example.net]", replies[0].Recipients)
			}
		})
	}
}

// TestVacationWithRuleForward: a forward requested by the rules stage (the
// attributed ForwardRequests, not the legacy ForwardAddresses the deliver
// stage drops) and the vacation reply both happen. The reply goes to the
// original sender, never the forward target.
func TestVacationWithRuleForward(t *testing.T) {
	const target = "away@elsewhere.test"
	h := newVacHarness(t)
	h.proc.SetForwardTargets(allowAll{})
	env, raw := extMsg("bob@example.net")
	env.ForwardRequests = ruleFwd("user-alice", target)
	if err := h.send(env, raw); err != nil {
		t.Fatal(err)
	}

	var forwards []*bongov1.OutboundMessage
	for _, m := range h.queue.messages {
		if m.Origin == bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD {
			forwards = append(forwards, m)
		}
	}
	if len(forwards) != 1 || len(forwards[0].Recipients) != 1 || forwards[0].Recipients[0] != target {
		t.Fatalf("forwards = %v, want one to %s", forwards, target)
	}
	replies := h.autoReplies()
	if len(replies) != 1 {
		t.Fatalf("replies = %d, want 1", len(replies))
	}
	if len(replies[0].Recipients) != 1 || replies[0].Recipients[0] != "bob@example.net" {
		t.Errorf("reply recipients = %v, want [bob@example.net]", replies[0].Recipients)
	}
	if n := len(decodeDeliveries(t, &mockApplier{commands: h.applier.commands})); n != 1 {
		t.Errorf("local deliveries = %d, want 1", n)
	}
}
