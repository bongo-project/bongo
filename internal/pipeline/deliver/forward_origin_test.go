package deliver

import (
	"context"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestDeliverForwardMarkedForwardOrigin pins the origin on forwarded copies so
// the queue worker never DKIM-signs relayed inbound mail with a hosted-domain
// key, even when the inbound header From spoofs a hosted domain.
func TestDeliverForwardMarkedForwardOrigin(t *testing.T) {
	tests := []struct {
		name  string
		env   func() *bongov1.QueueEnvelope
		prefs map[string]*bongov1.UserPreferences
	}{
		{
			name: "account forward address",
			env: func() *bongov1.QueueEnvelope {
				return &bongov1.QueueEnvelope{Id: "env-origin-1", Sender: "ceo@hosted.example", Recipients: []string{"alice@example.com"}, ReceivedAt: time.Now().Unix(), BlobHash: "h1"}
			},
			prefs: map[string]*bongov1.UserPreferences{"user-alice": {ForwardAddress: "external@other.com"}},
		},
		{
			name: "rule forward addresses",
			env: func() *bongov1.QueueEnvelope {
				return &bongov1.QueueEnvelope{Id: "env-origin-2", Sender: "ceo@hosted.example", Recipients: []string{"alice@example.com"}, ReceivedAt: time.Now().Unix(), BlobHash: "h2", ForwardRequests: []*bongov1.ForwardRequest{{UserId: "user-alice", Target: "external@other.com"}}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queuer := &mockQueuer{}
			proc := NewDeliverProcessor(&mockApplier{}, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, newTestLogger())
			proc.SetQueuer(queuer)
			proc.SetForwardTargets(allowAllTargets{})
			if tt.prefs != nil {
				proc.SetPreferences(&mockPrefs{prefs: tt.prefs})
			}
			rawMsg := minimalMessage("ceo@hosted.example", "alice@example.com", "Hello", "Hi!\r\n")
			if _, err := proc.Process(context.Background(), tt.env(), strings.NewReader(rawMsg)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(queuer.messages) != 1 {
				t.Fatalf("forwarded messages = %d, want 1", len(queuer.messages))
			}
			if got := queuer.messages[0].GetOrigin(); got != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD {
				t.Errorf("origin = %v, want FORWARD", got)
			}
		})
	}
}
