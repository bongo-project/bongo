package deliver

import (
	"context"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type bookingCall struct{ userID, sender, ctMethod string }

type fakeBookings struct {
	calls []bookingCall
	// stored is the number of commands applied when each call was made: the
	// answerer runs before the mail is stored.
	stored  []int
	store   *mockApplier
	handled bool
}

func (f *fakeBookings) AnswerBooking(userID, sender string, _ []byte, ctMethod string) bool {
	f.calls = append(f.calls, bookingCall{userID, sender, ctMethod})
	f.stored = append(f.stored, len(f.store.commands))
	return f.handled
}

const requestICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nEND:VCALENDAR\r\n"

func requestMessage(from string) string {
	return "From: " + from + "\r\nTo: room@example.com\r\nSubject: Meeting\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <q@example.com>\r\n" +
		"Content-Type: text/calendar; method=REQUEST; charset=utf-8\r\n\r\n" + requestICS
}

func TestDeliverResourceBookingTrust(t *testing.T) {
	tests := []struct {
		name      string
		from      string
		dmarc     string
		authUser  string
		allow     map[string]bool
		target    string
		noAnswer  bool
		wantCalls int
		handled   bool
		plain     bool
	}{
		{name: "dmarc pass", dmarc: "pass", wantCalls: 1},
		{name: "handled booking mail is not stored", dmarc: "pass", wantCalls: 1, handled: true},
		{name: "plain mail is stored even when the answerer would handle", dmarc: "pass", wantCalls: 0, handled: true, plain: true},
		{name: "dmarc fail", dmarc: "fail", wantCalls: 0},
		{name: "dmarc empty", dmarc: "", wantCalls: 0},
		{name: "authenticated local sender", authUser: "u-bob", allow: map[string]bool{"u-bob|bob@example.org": true}, wantCalls: 1},
		{name: "authenticated sender not allowed", authUser: "u-bob", allow: map[string]bool{}, wantCalls: 0},
		{name: "junk copy", dmarc: "pass", target: "Junk", wantCalls: 0},
		{name: "not wired", dmarc: "pass", noAnswer: true, wantCalls: 0},
		{name: "unparseable from", from: "not an address", dmarc: "pass", wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := &mockApplier{}
			proc := NewDeliverProcessor(ap, &mockResolver{mapping: map[string]string{"room@example.com": "user-room"}}, newTestLogger())
			rec := &fakeBookings{store: ap, handled: tt.handled}
			if !tt.noAnswer {
				proc.SetResourceBookings(rec)
			}
			proc.SetSenderAuthorizer(&fakeAuthorizer{allow: tt.allow})
			env := &bongov1.QueueEnvelope{
				Id: "env-book", Sender: "bob@example.org", Recipients: []string{"room@example.com"},
				ReceivedAt: time.Now().Unix(), BlobHash: "h", AuthUserId: tt.authUser, TargetMailbox: tt.target,
				Stages: []*bongov1.StageResult{{Stage: "antispam", DmarcResult: tt.dmarc}},
			}
			from := tt.from
			if from == "" {
				from = "Bob@Example.org"
			}
			msg := requestMessage(from)
			if tt.plain {
				msg = "From: " + from + "\r\nTo: room@example.com\r\nSubject: Hi\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <p@example.com>\r\nContent-Type: text/plain\r\n\r\nhello\r\n"
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(msg)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(rec.calls) != tt.wantCalls {
				t.Fatalf("answerer calls = %d, want %d", len(rec.calls), tt.wantCalls)
			}
			// Mail is stored unless the answerer was asked and handled it.
			wantStored := !tt.handled || tt.wantCalls == 0
			if stored := len(ap.commands) > 0; stored != wantStored {
				t.Errorf("stored = %v, want %v (handled = %v)", stored, wantStored, tt.handled)
			}
			if tt.wantCalls == 0 {
				return
			}
			c := rec.calls[0]
			if c.userID != "user-room" || c.sender != "bob@example.org" || !strings.EqualFold(c.ctMethod, "REQUEST") {
				t.Errorf("call = %+v", c)
			}
			if c.sender == "" {
				t.Error("answerer called with an empty sender")
			}
			if rec.stored[0] != 0 {
				t.Error("answerer ran after the mail was stored")
			}
		})
	}
}
