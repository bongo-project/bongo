package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/pipeline/deliver"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// deliveryApplier counts the Raft commands the delivery stage issues; a
// stored message is at least one command.
type deliveryApplier struct{ commands []*bongov1.RaftCommand }

func (r *deliveryApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	r.commands = append(r.commands, cmd)
	return nil
}

type mapResolver map[string]string

func (m mapResolver) ResolveRecipient(addr string) (string, error) {
	if id, ok := m[addr]; ok {
		return id, nil
	}
	return "", fmt.Errorf("unknown recipient %s", addr)
}

func bookingMail(ics, method string) string {
	ct, body := "text/plain; charset=utf-8", "Can we meet?\r\n"
	if ics != "" {
		ct, body = "text/calendar; method="+method+"; charset=utf-8", ics
	}
	return "From: " + aliceAdr + "\r\nTo: " + resMail + "\r\nSubject: Planning\r\n" +
		"Date: Mon, 01 Jan 2030 12:00:00 +0000\r\nMessage-Id: <m@example.org>\r\n" +
		"Content-Type: " + ct + "\r\n\r\n" + body
}

// Booking mail the resource has answered is not kept, however many arrive, and
// plain mail to the same resource still is. Domain usage counts mail records
// and event records: a handled booking adds no mail record (the recording
// applier saw no command at all, so no /user/res-1/mail/ key can exist), only
// the event record the booking itself is.
func TestDeliveredBookingMailIsAnsweredNotStored(t *testing.T) {
	e := newResourceEnv(t)
	ap := &deliveryApplier{}
	proc := deliver.NewDeliverProcessor(ap, mapResolver{resMail: resID}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	proc.SetResourceBookings(e.h)

	process := func(t *testing.T, id, msg, dmarc string) {
		t.Helper()
		env := &bongov1.QueueEnvelope{
			Id: id, Sender: aliceAdr, Recipients: []string{resMail}, ReceivedAt: time.Now().Unix(), BlobHash: "h",
			Stages: []*bongov1.StageResult{{Stage: "antispam", DmarcResult: dmarc}},
		}
		if _, err := proc.Process(context.Background(), env, strings.NewReader(msg)); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}

	for i := range 10 {
		start := time.Date(2030, 5, 6+i, 9, 0, 0, 0, time.UTC)
		ics := bookOpts{
			uid:   fmt.Sprintf("book-%d@example.org", i),
			start: start.Format("20060102T150405Z"), end: start.Add(time.Hour).Format("20060102T150405Z"),
		}.ics()
		process(t, fmt.Sprintf("env-%d", i), bookingMail(ics, "REQUEST"), "pass")
	}
	if got := e.answers(t); len(got) != 10 {
		t.Fatalf("replies = %v, want 10", got)
	}
	if e.count(t) != 10 {
		t.Errorf("bookings = %d, want 10", e.count(t))
	}
	if len(ap.commands) != 0 {
		t.Fatalf("%d commands stored booking mail in the resource's INBOX", len(ap.commands))
	}

	// Not verified: never answered, so stored as ordinary mail.
	process(t, "env-unverified", bookingMail(bookOpts{uid: "forged@example.org"}.ics(), "REQUEST"), "fail")
	if len(e.answers(t)) != 10 || len(ap.commands) == 0 {
		t.Errorf("unverified booking mail: replies %d, commands %d; want 10 and some", len(e.answers(t)), len(ap.commands))
	}

	// Plain mail is stored for the resource.
	before := len(ap.commands)
	process(t, "env-plain", bookingMail("", ""), "pass")
	if len(ap.commands) == before {
		t.Error("plain mail to the resource was not stored")
	}
	if len(e.answers(t)) != 10 {
		t.Errorf("plain mail was answered: %v", e.answers(t))
	}
}
