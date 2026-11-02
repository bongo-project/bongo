package deliver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type replyCall struct{ userID, sender, ctMethod, part string }

// fakeReplies records calls and how many commands were stored at call time.
type fakeReplies struct {
	calls  []replyCall
	stored []int
	store  *mockApplier
}

func (f *fakeReplies) ApplyReply(userID, sender string, part []byte, ctMethod string) {
	f.calls = append(f.calls, replyCall{userID, sender, ctMethod, string(part)})
	f.stored = append(f.stored, len(f.store.commands))
}

const replyICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REPLY\r\nEND:VCALENDAR\r\n"

func replyMessage(from string) string {
	return "From: " + from + "\r\nTo: alice@example.com\r\nSubject: Accepted\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <r@example.com>\r\n" +
		"Content-Type: text/calendar; method=REPLY; charset=utf-8\r\n\r\n" + replyICS
}

func TestDeliverCalendarReplyTrust(t *testing.T) {
	const from = "Bob@Example.org"
	const want = "bob@example.org"
	tests := []struct {
		name      string
		raw       string
		dmarc     string
		authUser  string
		noAuthz   bool
		allow     map[string]bool
		authzErr  error
		target    string
		discard   bool
		skipLocal bool
		noApplier bool
		wantCalls int
	}{
		{name: "dmarc pass", dmarc: "pass", wantCalls: 1},
		{name: "dmarc fail", dmarc: "fail", wantCalls: 0},
		{name: "dmarc none", dmarc: "none", wantCalls: 0},
		{name: "dmarc empty", dmarc: "", wantCalls: 0},
		{name: "authenticated author may send as", authUser: "u-bob", allow: map[string]bool{"u-bob|bob@example.org": true}, wantCalls: 1},
		{name: "authenticated author may not", authUser: "u-bob", allow: map[string]bool{}, wantCalls: 0},
		{name: "send-as lookup error", authUser: "u-bob", authzErr: errors.New("boom"), wantCalls: 0},
		{name: "authenticated without authorizer", authUser: "u-bob", noAuthz: true, wantCalls: 0},
		{name: "trusted network, no auth user", allow: map[string]bool{"|bob@example.org": true}, wantCalls: 0},
		{name: "junk target", dmarc: "pass", target: "Junk", wantCalls: 0},
		{name: "discard normal", dmarc: "pass", discard: true, wantCalls: 0},
		{name: "skip local copy", dmarc: "pass", skipLocal: true, wantCalls: 0},
		{name: "two from headers", raw: "From: a@example.org\r\n" + replyMessage(from), dmarc: "pass", wantCalls: 0},
		{name: "no applier wired", dmarc: "pass", noApplier: true, wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := &mockApplier{}
			proc := NewDeliverProcessor(ap, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, newTestLogger())
			rec := &fakeReplies{store: ap}
			if !tt.noApplier {
				proc.SetCalendarReplies(rec)
			}
			if !tt.noAuthz {
				proc.SetSenderAuthorizer(&fakeAuthorizer{allow: tt.allow, err: tt.authzErr})
			}
			if tt.skipLocal {
				proc.SetQueuer(&mockQueuer{})
				proc.SetForwardTargets(allowAllTargets{})
				proc.SetPreferences(&mockPrefs{prefs: map[string]*bongov1.UserPreferences{"user-alice": {ForwardAddress: "x@other.com"}}})
			}
			env := &bongov1.QueueEnvelope{
				Id: "env-reply", Sender: "bob@example.org", Recipients: []string{"alice@example.com"},
				ReceivedAt: time.Now().Unix(), BlobHash: "h", AuthUserId: tt.authUser,
				TargetMailbox: tt.target, DiscardNormal: tt.discard,
				Stages: []*bongov1.StageResult{{Stage: "antispam", DmarcResult: tt.dmarc}},
			}
			raw := tt.raw
			if raw == "" {
				raw = replyMessage(from)
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(rec.calls) != tt.wantCalls {
				t.Fatalf("applier calls = %d, want %d", len(rec.calls), tt.wantCalls)
			}
			if tt.wantCalls == 0 {
				return
			}
			c := rec.calls[0]
			if c.userID != "user-alice" || c.sender != want || !strings.EqualFold(c.ctMethod, "REPLY") || c.part != replyICS {
				t.Errorf("call = %+v", c)
			}
			if rec.stored[0] == 0 {
				t.Error("applier ran before the mail was stored")
			}
		})
	}
}

func TestDeliverCalendarReplyOncePerUser(t *testing.T) {
	ap := &mockApplier{}
	proc := NewDeliverProcessor(ap, &mockResolver{mapping: map[string]string{
		"alice@example.com": "user-alice", "ali@example.com": "user-alice", "carl@example.com": "user-carl",
	}}, newTestLogger())
	rec := &fakeReplies{store: ap}
	proc.SetCalendarReplies(rec)
	env := &bongov1.QueueEnvelope{
		Id: "env-reply2", Sender: "bob@example.org",
		Recipients: []string{"alice@example.com", "ali@example.com", "carl@example.com"},
		ReceivedAt: time.Now().Unix(), BlobHash: "h",
		Stages: []*bongov1.StageResult{{Stage: "antispam", DmarcResult: "pass"}},
	}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(replyMessage("bob@example.org"))); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 2 || rec.calls[0].userID != "user-alice" || rec.calls[1].userID != "user-carl" {
		t.Errorf("calls = %+v, want alice then carl once each", rec.calls)
	}
}

func TestDeliverOrdinaryMailNeverReachesCalendar(t *testing.T) {
	ap := &mockApplier{}
	proc := NewDeliverProcessor(ap, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, newTestLogger())
	rec := &fakeReplies{store: ap}
	proc.SetCalendarReplies(rec)
	env := &bongov1.QueueEnvelope{
		Id: "env-plain", Sender: "bob@example.org", Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(), BlobHash: "h",
		Stages: []*bongov1.StageResult{{Stage: "antispam", DmarcResult: "pass"}},
	}
	raw := minimalMessage("bob@example.org", "alice@example.com", "Hi", "text\r\n")
	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("applier called for a message without a calendar part")
	}
}
