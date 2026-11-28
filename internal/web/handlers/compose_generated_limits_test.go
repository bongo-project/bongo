package handlers

import (
	"errors"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Generated mail draws on the same per-user limits as a composed send: the
// per-user messages-per-minute value overrides the global one, and a limits
// read error refuses the external recipients.
func TestDeliverGeneratedUsesPerUserLimits(t *testing.T) {
	tests := []struct {
		name       string
		reader     func(string) (*bongov1.SendLimits, error)
		global     int
		wantFirst  int // external recipients not reached on the first call
		wantSecond int
	}{
		{"global allows one", nil, 1, 0, 1},
		{"per user allows two", limitsReader(&bongov1.SendLimits{MessagesPerMinute: 2}), 1, 0, 0},
		{"per user stricter", limitsReader(&bongov1.SendLimits{MessagesPerMinute: 1}), 5, 0, 1},
		{"unreadable limits fail closed", func(string) (*bongov1.SendLimits, error) { return nil, errors.New("down") }, 5, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, uid, email := newPipelineEnv(t)
			env.handler.SetMessageLimiter(mail.NewRateLimiterWindow(tt.global, time.Minute))
			if tt.reader != nil {
				env.handler.SetSendLimitsReader(tt.reader)
			}
			for i, want := range []int{tt.wantFirst, tt.wantSecond} {
				notReached, _ := env.handler.DeliverGenerated(uid, email, email, []string{"far@remote.example"}, []byte(generatedRaw))
				if len(notReached) != want {
					t.Fatalf("call %d: not reached = %v, want %d", i+1, notReached, want)
				}
			}
		})
	}
}
