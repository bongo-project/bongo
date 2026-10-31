package deliver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type fakeReporter struct {
	originals  []*bongov1.OutboundMessage
	recipients [][]string
	err        error
}

func (f *fakeReporter) GenerateDeliveredReport(original *bongov1.OutboundMessage, recipients []string) error {
	f.originals = append(f.originals, original)
	f.recipients = append(f.recipients, recipients)
	return f.err
}

func reportEnv() *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id: "env-rpt", Sender: "alice@remote.example", Recipients: []string{"bob@hosted.example"},
		ReceivedAt: time.Now().Unix(), BlobHash: "blob-rpt", SpfResult: "pass",
		DsnNotify: mail.NotifySuccess,
	}
}

func reportProc(t *testing.T, rep DeliveryReporter) (*DeliverProcessor, *mockApplier) {
	t.Helper()
	applier := &mockApplier{}
	proc := NewDeliverProcessor(applier, &mockResolver{mapping: map[string]string{"bob@hosted.example": "user-bob"}}, newTestLogger())
	proc.SetDeliveryReporter(rep)
	return proc, applier
}

func reportRaw() string {
	return minimalMessage("alice@remote.example", "bob@hosted.example", "Hello", "Hi!\r\n")
}

// Exactly the recipients that asked for SUCCESS, from senders who would get a
// bounce today, get a report, once, after the copy was written.
func TestDeliveryReport(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*bongov1.QueueEnvelope)
		want   int
	}{
		{"SUCCESS, SPF pass", func(e *bongov1.QueueEnvelope) {}, 1},
		{"SUCCESS with others", func(e *bongov1.QueueEnvelope) {
			e.DsnNotify = mail.NotifySuccess | mail.NotifyFailure | mail.NotifyDelay
		}, 1},
		{"authenticated local author, no SPF", func(e *bongov1.QueueEnvelope) { e.SpfResult = ""; e.AuthUserId = "user-alice" }, 1},
		{"no NOTIFY", func(e *bongov1.QueueEnvelope) { e.DsnNotify = 0 }, 0},
		{"FAILURE only", func(e *bongov1.QueueEnvelope) { e.DsnNotify = mail.NotifyFailure }, 0},
		{"NEVER", func(e *bongov1.QueueEnvelope) { e.DsnNotify = mail.NotifyNever }, 0},
		{"DELAY only", func(e *bongov1.QueueEnvelope) { e.DsnNotify = mail.NotifyDelay }, 0},
		// Backscatter gate, as for bounces.
		{"null sender", func(e *bongov1.QueueEnvelope) { e.Sender = "" }, 0},
		{"blank sender", func(e *bongov1.QueueEnvelope) { e.Sender = "  " }, 0},
		{"MX sender whose SPF failed", func(e *bongov1.QueueEnvelope) { e.SpfResult = "fail" }, 0},
		{"MX sender with softfail", func(e *bongov1.QueueEnvelope) { e.SpfResult = "softfail" }, 0},
		{"MX sender with no SPF result", func(e *bongov1.QueueEnvelope) { e.SpfResult = "" }, 0},
		// Not delivered to a mailbox the recipient reads.
		{"filed as Junk", func(e *bongov1.QueueEnvelope) { e.TargetMailbox = "Junk" }, 0},
		{"quarantined", func(e *bongov1.QueueEnvelope) { e.TargetMailbox = "Quarantine" }, 0},
		{"delete rule", func(e *bongov1.QueueEnvelope) { e.DiscardNormal = true }, 0},
		{"rule files to a folder (control)", func(e *bongov1.QueueEnvelope) { e.TargetMailbox = "Receipts" }, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &fakeReporter{}
			proc, _ := reportProc(t, rep)
			env := reportEnv()
			tt.mutate(env)
			if _, err := proc.Process(context.Background(), env, strings.NewReader(reportRaw())); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(rep.originals) != tt.want {
				t.Fatalf("reports = %d, want %d", len(rep.originals), tt.want)
			}
		})
	}
}

func TestDeliveryReportCarriesDSNParameters(t *testing.T) {
	rep := &fakeReporter{}
	proc, _ := reportProc(t, rep)
	env := reportEnv()
	env.DsnRet, env.DsnEnvid = "FULL", "env-7"
	env.DsnOrcptType, env.DsnOrcpt = "rfc822", "orig@else.example"
	if _, err := proc.Process(context.Background(), env, strings.NewReader(reportRaw())); err != nil {
		t.Fatal(err)
	}
	if len(rep.originals) != 1 {
		t.Fatalf("reports = %d", len(rep.originals))
	}
	o := rep.originals[0]
	if o.SenderEmail != "alice@remote.example" || o.BlobHash != "blob-rpt" || o.DsnRet != "FULL" || o.DsnEnvid != "env-7" {
		t.Errorf("original = %v", o)
	}
	if len(o.RecipientDsn) != 1 || o.RecipientDsn[0].Address != "bob@hosted.example" ||
		o.RecipientDsn[0].Orcpt != "orig@else.example" || o.RecipientDsn[0].OrcptType != "rfc822" ||
		o.RecipientDsn[0].Notify != mail.NotifySuccess {
		t.Errorf("recipient_dsn = %v", o.RecipientDsn)
	}
	if got := rep.recipients[0]; len(got) != 1 || got[0] != "bob@hosted.example" {
		t.Errorf("recipients = %v", got)
	}
}

// The mail is delivered whether or not the report can be; a reporter error never
// fails the stage, and a nil reporter (an edge node) sends nothing.
func TestDeliveryReportIsBestEffort(t *testing.T) {
	rep := &fakeReporter{err: errors.New("spool full")}
	proc, applier := reportProc(t, rep)
	if _, err := proc.Process(context.Background(), reportEnv(), strings.NewReader(reportRaw())); err != nil {
		t.Fatalf("Process failed on a report error: %v", err)
	}
	if len(applier.commands) == 0 {
		t.Error("mail was not delivered")
	}

	proc, _ = reportProc(t, nil)
	if _, err := proc.Process(context.Background(), reportEnv(), strings.NewReader(reportRaw())); err != nil {
		t.Fatalf("Process with no reporter: %v", err)
	}
}

// A recipient that cannot be resolved is not delivered to, so it is not
// reported as delivered.
func TestDeliveryReportNotSentForUnknownRecipient(t *testing.T) {
	rep := &fakeReporter{}
	proc, _ := reportProc(t, rep)
	env := reportEnv()
	env.Recipients = []string{"nobody@hosted.example"}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(reportRaw())); err != nil {
		t.Fatal(err)
	}
	if len(rep.originals) != 0 {
		t.Errorf("reports = %d, want 0", len(rep.originals))
	}
}
