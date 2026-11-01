package handlers

import (
	"sort"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mail"
)

const generatedRaw = "From: alice@example.com\r\nTo: x\r\nSubject: s\r\nMessage-ID: <gen-1@example.com>\r\n\r\nbody\r\n"

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestDeliverGeneratedSplitsLocalAndExternal(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	notReached, err := env.handler.DeliverGenerated(uid, email, email,
		[]string{"bob@example.com", "Carol <carol@example.com>", "BOB@example.com", "far@remote.example", "far2@remote.example"},
		[]byte(generatedRaw))
	if err != nil || len(notReached) != 0 {
		t.Fatalf("notReached=%v err=%v", notReached, err)
	}

	envs := env.spool.envelopes()
	var got []string
	for _, e := range envs {
		if !e.Trusted || e.AuthUserId != uid || e.Sender != email || e.BlobHash == "" || e.Id == "" {
			t.Errorf("envelope = %+v", e)
		}
		got = append(got, e.Recipients...)
	}
	if strings.Join(sorted(got), ",") != "bob@example.com,carol@example.com" {
		t.Errorf("spooled for %v", got)
	}

	pending, err := env.qs.ListPending(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("queue entries = %d, want 1", len(pending))
	}
	out := pending[0]
	if out.SenderId != uid || out.SenderEmail != email || out.MessageGuid != "<gen-1@example.com>" ||
		strings.Join(sorted(out.Recipients), ",") != "far2@remote.example,far@remote.example" {
		t.Errorf("outbound = %+v", out)
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 0 {
		t.Errorf("Sent copy written: %d", len(sent))
	}
}

func TestDeliverGeneratedNotReached(t *testing.T) {
	rcpts := []string{"bob@example.com", "carol@example.com", "far@remote.example", "far2@remote.example"}
	for _, tc := range []struct {
		name      string
		setup     func(env *composeSendAsEnv)
		want      []string
		wantLocal int
		wantQueue int
	}{
		{"recipient budget refuses the externals", func(env *composeSendAsEnv) {
			env.handler.SetRecipientLimiter(mail.NewRateLimiter(1)) // 2 externals need 2
		}, []string{"far2@remote.example", "far@remote.example"}, 2, 0},
		{"message limiter refuses the externals", func(env *composeSendAsEnv) {
			rl := mail.NewRateLimiter(1)
			rl.Allow("alice@example.com") // the hour's one message is spent
			env.handler.SetRateLimiter(rl)
		}, []string{"far2@remote.example", "far@remote.example"}, 2, 0},
		{"spool failure names only that recipient", func(env *composeSendAsEnv) {
			env.spool.failOn = 2
		}, nil, 1, 1},
		{"no spool", func(env *composeSendAsEnv) {
			env.handler.spool = nil
		}, []string{"bob@example.com", "carol@example.com"}, 0, 1},
		{"no queue", func(env *composeSendAsEnv) {
			env.handler.queueStore = nil
		}, []string{"far2@remote.example", "far@remote.example"}, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, uid, email := newPipelineEnv(t)
			tc.setup(env)
			notReached, err := env.handler.DeliverGenerated(uid, email, email, rcpts, []byte(generatedRaw))
			if tc.name == "spool failure names only that recipient" {
				if len(notReached) != 1 || (notReached[0] != "bob@example.com" && notReached[0] != "carol@example.com") {
					t.Fatalf("notReached = %v", notReached)
				}
			} else if strings.Join(sorted(notReached), ",") != strings.Join(tc.want, ",") {
				t.Fatalf("notReached = %v, want %v", notReached, tc.want)
			}
			if len(notReached) > 0 && err == nil {
				t.Error("err is nil although recipients were not reached")
			}
			if got := len(env.spool.envelopes()); got != tc.wantLocal {
				t.Errorf("spooled = %d, want %d", got, tc.wantLocal)
			}
			pending, _ := env.qs.ListPending(10)
			if len(pending) != tc.wantQueue {
				t.Errorf("queued = %d, want %d", len(pending), tc.wantQueue)
			}
		})
	}
}

func TestDeliverGeneratedNoRecipients(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	notReached, err := env.handler.DeliverGenerated(uid, email, email, []string{"", "  "}, []byte(generatedRaw))
	if notReached != nil || err != nil || len(env.spool.envelopes()) != 0 {
		t.Errorf("notReached=%v err=%v", notReached, err)
	}
}
