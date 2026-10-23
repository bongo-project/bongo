package deliver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeAuthorizer maps "user|addr" to a send-as answer and counts calls.
type fakeAuthorizer struct {
	allow map[string]bool
	err   error
	calls int
}

func (f *fakeAuthorizer) CanSendAs(userID, addr string) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.allow[userID+"|"+addr], nil
}

// TestDeliverForwardOriginSigning pins when a forward is LOCAL (DKIM-signed by
// the queue worker) and when it stays FORWARD. One shared dataset: user B
// (alice) forwards everything to an external address.
func TestDeliverForwardOriginSigning(t *testing.T) {
	const (
		local = bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL
		fwd   = bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD
	)
	hdr := func(from string) string {
		return minimalMessage(from, "alice@example.com", "Hello", "Hi!\r\n")
	}
	tests := []struct {
		name       string
		raw        string
		authUser   string
		noAuthz    bool
		authzErr   error
		allow      map[string]bool
		want       bongov1.OutboundOrigin
		wantCalled bool
	}{
		{"a authenticated author allowed", hdr("a@hosted.example"), "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true}, local, true},
		{"a mixed-case From is lowered", hdr("A@Hosted.Example"), "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true}, local, true},
		{"b external spoof of hosted From", hdr("a@hosted.example"), "", false, nil, map[string]bool{"user-a|a@hosted.example": true}, fwd, false},
		{"c authenticated but not allowed to send as From", hdr("b@hosted.example"), "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true}, fwd, true},
		{"d lookup error fails closed", hdr("a@hosted.example"), "user-a", false, errors.New("boom"), nil, fwd, true},
		{"e ordinary inbound, no authorizer", hdr("bob@other.example"), "", true, nil, nil, fwd, false},
		{"f auth user but nil authorizer", hdr("a@hosted.example"), "user-a", true, nil, nil, fwd, false},
		{"g two From headers", "From: a@hosted.example\r\n" + hdr("b@hosted.example"), "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true, "user-a|b@hosted.example": true}, fwd, false},
		{"g address list", hdr("a@hosted.example, b@other.example"), "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true}, fwd, false},
		{"g comment smuggling", hdr("b@other.example (a@hosted.example)"), "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true}, fwd, true},
		{"h no From header", "To: alice@example.com\r\nSubject: x\r\n\r\nbody\r\n", "user-a", false, nil, map[string]bool{"user-a|a@hosted.example": true}, fwd, false},
	}
	for _, tt := range tests {
		for _, path := range []string{"forward address", "rule forward"} {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				queuer := &mockQueuer{}
				proc := NewDeliverProcessor(&mockApplier{}, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, newTestLogger())
				proc.SetQueuer(queuer)
				proc.SetForwardTargets(allowAllTargets{})
				authz := &fakeAuthorizer{allow: tt.allow, err: tt.authzErr}
				if !tt.noAuthz {
					proc.SetSenderAuthorizer(authz)
				}
				env := &bongov1.QueueEnvelope{
					Id: "env-local-origin", Sender: "a@hosted.example", Recipients: []string{"alice@example.com"},
					ReceivedAt: time.Now().Unix(), BlobHash: "h", AuthUserId: tt.authUser,
				}
				if path == "forward address" {
					proc.SetPreferences(&mockPrefs{prefs: map[string]*bongov1.UserPreferences{"user-alice": {ForwardAddress: "external@other.com"}}})
				} else {
					env.ForwardRequests = []*bongov1.ForwardRequest{{UserId: "user-alice", Target: "external@other.com"}}
				}
				if _, err := proc.Process(context.Background(), env, strings.NewReader(tt.raw)); err != nil {
					t.Fatalf("Process: %v", err)
				}
				if len(queuer.messages) != 1 {
					t.Fatalf("forwarded messages = %d, want 1", len(queuer.messages))
				}
				if got := queuer.messages[0].GetOrigin(); got != tt.want {
					t.Errorf("origin = %v, want %v", got, tt.want)
				}
				if (authz.calls > 0) != tt.wantCalled || (tt.noAuthz && authz.calls != 0) {
					t.Errorf("authorizer calls = %d, wantCalled %v", authz.calls, tt.wantCalled)
				}
			})
		}
	}
}
