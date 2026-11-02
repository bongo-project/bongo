package deliver

import (
	"context"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type placedItem struct{ userID, id, ics string }

type fakePlacer struct {
	items []placedItem
	err   error
}

func (f *fakePlacer) Place(userID, id string, ics []byte) error {
	f.items = append(f.items, placedItem{userID, id, string(ics)})
	return f.err
}

func itipMessage(from, method, attendees string) string {
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//X//EN\r\nMETHOD:" + method + "\r\n" +
		"BEGIN:VEVENT\r\nUID:meet-1@example.org\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\n" +
		"ORGANIZER:mailto:olive@example.org\r\n" + attendees +
		"BEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:x\r\nTRIGGER:-PT5M\r\nEND:VALARM\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	return "From: " + from + "\r\nTo: alice@example.com\r\nSubject: Meeting\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <m@example.com>\r\n" +
		"Content-Type: text/calendar; method=" + method + "; charset=utf-8\r\n\r\n" + body
}

const (
	bobOnly   = "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.org\r\n"
	bobCarol  = "ATTENDEE;PARTSTAT=ACCEPTED:mailto:bob@example.org\r\nATTENDEE;PARTSTAT=DECLINED:mailto:carol@example.org\r\n"
	carolOnly = "ATTENDEE;PARTSTAT=ACCEPTED:mailto:carol@example.org\r\n"
)

func TestDeliverScheduleInboxTrust(t *testing.T) {
	tests := []struct {
		name      string
		from      string
		method    string
		attendees string
		dmarc     string
		target    string
		noPlacer  bool
		want      int
		wantNot   []string
		wantHas   []string
	}{
		{name: "request from an unverified sender is placed", from: "olive@example.org", method: "REQUEST", attendees: bobOnly, dmarc: "fail", want: 1,
			wantHas: []string{"METHOD:REQUEST"}, wantNot: []string{"VALARM"}},
		{name: "request, no dmarc verdict at all", from: "olive@example.org", method: "REQUEST", attendees: bobOnly, want: 1},
		{name: "request into junk is not placed", from: "olive@example.org", method: "REQUEST", attendees: bobOnly, dmarc: "pass", target: "Junk", want: 0},
		{name: "reply vouched for by dmarc", from: "bob@example.org", method: "REPLY", attendees: bobCarol, dmarc: "pass", want: 1,
			wantHas: []string{"bob@example.org"}, wantNot: []string{"carol@example.org"}},
		{name: "reply unverified", from: "bob@example.org", method: "REPLY", attendees: bobOnly, dmarc: "fail", want: 0},
		{name: "reply verified for another address than the attendee", from: "bob@example.org", method: "REPLY", attendees: carolOnly, dmarc: "pass", want: 0},
		{name: "cancel from the organiser, verified", from: "olive@example.org", method: "CANCEL", attendees: bobOnly, dmarc: "pass", want: 1},
		{name: "cancel from the organiser, unverified", from: "olive@example.org", method: "CANCEL", attendees: bobOnly, dmarc: "fail", want: 0},
		{name: "cancel verified but not from the organiser", from: "mallory@example.org", method: "CANCEL", attendees: bobOnly, dmarc: "pass", want: 0},
		{name: "other methods are ordinary mail", from: "olive@example.org", method: "PUBLISH", attendees: bobOnly, dmarc: "pass", want: 0},
		{name: "no placer wired", from: "olive@example.org", method: "REQUEST", attendees: bobOnly, noPlacer: true, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ap := &mockApplier{}
			proc := NewDeliverProcessor(ap, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, newTestLogger())
			fp := &fakePlacer{}
			if !tt.noPlacer {
				proc.SetScheduleInbox(fp)
			}
			env := &bongov1.QueueEnvelope{
				Id: "env-itip", Sender: tt.from, Recipients: []string{"alice@example.com"},
				ReceivedAt: time.Now().Unix(), BlobHash: "h", TargetMailbox: tt.target,
				Stages: []*bongov1.StageResult{{Stage: "antispam", DmarcResult: tt.dmarc}},
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(itipMessage(tt.from, tt.method, tt.attendees))); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(fp.items) != tt.want {
				t.Fatalf("placed %d items, want %d", len(fp.items), tt.want)
			}
			for _, it := range fp.items {
				if it.userID != "user-alice" {
					t.Errorf("placed for %q", it.userID)
				}
				for _, w := range tt.wantHas {
					if !strings.Contains(it.ics, w) {
						t.Errorf("item lacks %q:\n%s", w, it.ics)
					}
				}
				for _, w := range tt.wantNot {
					if strings.Contains(it.ics, w) {
						t.Errorf("item holds %q:\n%s", w, it.ics)
					}
				}
			}
		})
	}
}

// A stage that runs twice for one envelope (recovery before the delivered
// flag was set) must present the same item id, so the store keeps one.
func TestDeliverScheduleInboxIDIsStable(t *testing.T) {
	run := func(envID string) string {
		ap := &mockApplier{}
		proc := NewDeliverProcessor(ap, &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}, newTestLogger())
		fp := &fakePlacer{}
		proc.SetScheduleInbox(fp)
		env := &bongov1.QueueEnvelope{Id: envID, Sender: "olive@example.org", Recipients: []string{"alice@example.com"}, ReceivedAt: time.Now().Unix(), BlobHash: "h"}
		if _, err := proc.Process(context.Background(), env, strings.NewReader(itipMessage("olive@example.org", "REQUEST", bobOnly))); err != nil {
			t.Fatal(err)
		}
		if len(fp.items) != 1 {
			t.Fatalf("placed %d", len(fp.items))
		}
		return fp.items[0].id
	}
	if a, b := run("env-1"), run("env-1"); a != b {
		t.Errorf("same delivery, ids %s and %s", a, b)
	}
	if run("env-1") == run("env-2") {
		t.Error("two deliveries share an id")
	}
}
