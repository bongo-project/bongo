package queue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// drain returns every report delivered so far.
func drain(d *mockDeliverer) []deliveredMsg {
	var out []deliveredMsg
	for {
		select {
		case m := <-d.messages:
			out = append(out, m)
		default:
			return out
		}
	}
}

func rcptDSNs(notify uint32, addrs ...string) []*bongov1.RecipientDSN {
	var out []*bongov1.RecipientDSN
	for _, a := range addrs {
		out = append(out, &bongov1.RecipientDSN{Address: a, Notify: notify})
	}
	return out
}

// A permanently failed recipient is reported only when its NOTIFY allows a
// failure report: absent, or including FAILURE. NEVER, SUCCESS alone and DELAY
// alone all mean none, and nothing at all (not even a postmaster copy) is sent
// when nobody is left to report.
func TestFailureReportHonoursNotify(t *testing.T) {
	tests := []struct {
		name       string
		notify     uint32
		wantReport bool
	}{
		{"no NOTIFY keeps the report", 0, true},
		{"FAILURE", mail.NotifyFailure, true},
		{"SUCCESS,FAILURE,DELAY", mail.NotifySuccess | mail.NotifyFailure | mail.NotifyDelay, true},
		{"NEVER", mail.NotifyNever, false},
		{"SUCCESS alone", mail.NotifySuccess, false},
		{"DELAY alone", mail.NotifyDelay, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)
			worker.bouncer.SetPostmasterCopy(true, func(string) bool { return true }, "admin@ops.example")
			worker.relay = &fakeRelay{permFails: []string{"dead@example.net"}}
			hash, _ := blobs.Put([]byte("From: alice@test.local\r\nSubject: hi\r\n\r\nbody"))
			msg := &bongov1.OutboundMessage{
				Id: "n1", SenderEmail: "alice@test.local", Recipients: []string{"dead@example.net"},
				BlobHash: hash, RecipientDsn: rcptDSNs(tt.notify, "Dead@Example.net"),
			}
			if tt.notify == 0 {
				msg.RecipientDsn = nil
			}
			if err := qs.Enqueue(msg); err != nil {
				t.Fatal(err)
			}
			worker.processMessage(context.Background(), msg)

			got := drain(deliverer)
			want := 0
			if tt.wantReport {
				want = 2 // the sender's bounce and the postmaster copy
			}
			if len(got) != want {
				t.Fatalf("deliveries = %d, want %d", len(got), want)
			}
		})
	}
}

// With two failed recipients only those that want a failure report are named.
func TestFailureReportNamesOnlyWantingRecipients(t *testing.T) {
	worker, qs, blobs, deliverer := newWorkerTestEnvWithDeliverer(t, true)
	worker.relay = &fakeRelay{permFails: []string{"quiet@example.net", "loud@example.net"}}
	hash, _ := blobs.Put([]byte("From: alice@test.local\r\nSubject: hi\r\n\r\nbody"))
	msg := &bongov1.OutboundMessage{
		Id: "n2", SenderEmail: "alice@test.local", Recipients: []string{"quiet@example.net", "loud@example.net"},
		BlobHash: hash, RecipientDsn: rcptDSNs(mail.NotifyNever, "quiet@example.net"),
	}
	if err := qs.Enqueue(msg); err != nil {
		t.Fatal(err)
	}
	worker.processMessage(context.Background(), msg)
	got := drain(deliverer)
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	body := string(got[0].data)
	if !strings.Contains(body, "Final-Recipient: rfc822; loud@example.net") || strings.Contains(body, "quiet@example.net") {
		t.Errorf("report names the wrong recipients:\n%s", body)
	}
}

func delayEnv(t *testing.T, clock *time.Time) (*Worker, *QueueStore, *mockDeliverer, string) {
	t.Helper()
	worker, qs, relay, deliverer, hash := newLifetimeEnv(t, clock)
	_ = relay
	worker.delayWarning = 4 * time.Hour
	return worker, qs, deliverer, hash
}

func delayMsg(id, hash string, created time.Time, notify uint32) *bongov1.OutboundMessage {
	m := &bongov1.OutboundMessage{
		Id: id, SenderEmail: "alice@example.com", Recipients: []string{"bob@external.com"},
		BlobHash: hash, CreatedAt: created.Unix(), NextRetryAt: created.Unix(),
	}
	if notify != 0 {
		m.RecipientDsn = rcptDSNs(notify, "bob@external.com")
	}
	return m
}

// One delay report, on the first failed attempt at least delay_warning_after
// after creation, and never another across later attempts. The flag is
// persisted, not just in memory.
func TestDelayReportSentOnce(t *testing.T) {
	created := time.Unix(1_800_000_000, 0)
	clock := created
	worker, qs, deliverer, hash := delayEnv(t, &clock)
	msg := delayMsg("d1", hash, created, mail.NotifyDelay)
	if err := qs.Enqueue(msg); err != nil {
		t.Fatal(err)
	}

	// One second short of the threshold: nothing yet.
	clock = created.Add(4*time.Hour - time.Second)
	worker.handleFailure(msg, "connection refused")
	if n := len(drain(deliverer)); n != 0 {
		t.Fatalf("delay report before the threshold: %d", n)
	}
	if stored, _ := qs.Get("d1"); stored.DelayReportSent {
		t.Fatal("flag set before a report was due")
	}

	// Exactly at the threshold.
	clock = created.Add(4 * time.Hour)
	worker.handleFailure(msg, "451 4.7.1 try later")
	got := drain(deliverer)
	if len(got) != 1 {
		t.Fatalf("delay reports at threshold = %d, want 1", len(got))
	}
	body := string(got[0].data)
	for _, want := range []string{"Action: delayed\r\n", "Status: 4.7.1\r\n", "Will-Retry-Until: "} {
		if !strings.Contains(body, want) {
			t.Errorf("report missing %q", want)
		}
	}
	if got[0].from != "" || len(got[0].recipients) != 1 || got[0].recipients[0] != "alice@example.com" {
		t.Errorf("delivery = from %q to %v", got[0].from, got[0].recipients)
	}
	stored, err := qs.Get("d1")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.DelayReportSent {
		t.Error("DelayReportSent not persisted on the stored record")
	}

	// A second failure, and a worker that only knows the stored record.
	clock = clock.Add(time.Hour)
	worker.handleFailure(stored, "connection refused")
	clock = clock.Add(time.Hour)
	worker.handleFailure(msg, "connection refused")
	if n := len(drain(deliverer)); n != 0 {
		t.Errorf("further delay reports = %d, want 0", n)
	}
}

// A delay report needs a recipient that asked for DELAY; no NOTIFY, FAILURE and
// SUCCESS alone keep today's behaviour of no delay report.
func TestDelayReportOnlyForNotifyDelay(t *testing.T) {
	tests := []struct {
		name   string
		notify uint32
		want   int
	}{
		{"no NOTIFY", 0, 0},
		{"FAILURE", mail.NotifyFailure, 0},
		{"SUCCESS", mail.NotifySuccess, 0},
		{"NEVER", mail.NotifyNever, 0},
		{"DELAY", mail.NotifyDelay, 1},
		{"DELAY,FAILURE", mail.NotifyDelay | mail.NotifyFailure, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := time.Unix(1_800_000_000, 0)
			clock := created.Add(5 * time.Hour)
			worker, qs, deliverer, hash := delayEnv(t, &clock)
			worker.bouncer.SetPostmasterCopy(true, func(string) bool { return true }, "admin@ops.example")
			msg := delayMsg("d2", hash, created, tt.notify)
			if err := qs.Enqueue(msg); err != nil {
				t.Fatal(err)
			}
			worker.handleFailure(msg, "connection refused")
			// A delay report is never copied to the postmaster.
			if n := len(drain(deliverer)); n != tt.want {
				t.Errorf("deliveries = %d, want %d", n, tt.want)
			}
		})
	}
}

// Delay reports go through the same gate as bounces. The flag stays unset when
// the report is not allowed.
func TestDelayReportBackscatterGate(t *testing.T) {
	tests := []struct {
		name          string
		sender        string
		origin        bongov1.OutboundOrigin
		authenticated bool
		want          int
	}{
		{"local sender (control)", "alice@example.com", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, false, 1},
		{"null sender", "", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, false, 0},
		{"unauthenticated forward", "SRS0=aaaa=bb=forged.example=mallory@hosted.example", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, false, 0},
		{"authenticated forward (control)", "SRS0=aaaa=bb=real.example=carol@hosted.example", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := time.Unix(1_800_000_000, 0)
			clock := created.Add(5 * time.Hour)
			worker, qs, deliverer, hash := delayEnv(t, &clock)
			msg := delayMsg("d3", hash, created, mail.NotifyDelay)
			msg.SenderEmail, msg.Origin, msg.SenderAuthenticated = tt.sender, tt.origin, tt.authenticated
			if err := qs.Enqueue(msg); err != nil {
				t.Fatal(err)
			}
			worker.handleFailure(msg, "connection refused")
			if n := len(drain(deliverer)); n != tt.want {
				t.Errorf("deliveries = %d, want %d", n, tt.want)
			}
			stored, _ := qs.Get("d3")
			if stored.DelayReportSent != (tt.want == 1) {
				t.Errorf("DelayReportSent = %v, want %v", stored.DelayReportSent, tt.want == 1)
			}
		})
	}
}

// The bounce path never sends a delay report: a NOTIFY=DELAY-only recipient
// whose message finally expires gets nothing at all, and one that also wants
// FAILURE gets exactly the failure report.
func TestBouncePathSendsNoDelayReport(t *testing.T) {
	tests := []struct {
		name   string
		notify uint32
		want   int
	}{
		{"DELAY only", mail.NotifyDelay, 0},
		{"DELAY,FAILURE", mail.NotifyDelay | mail.NotifyFailure, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := time.Unix(1_800_000_000, 0)
			clock := created.Add(6 * 24 * time.Hour) // past the 5 day lifetime
			worker, qs, deliverer, hash := delayEnv(t, &clock)
			msg := delayMsg("d4", hash, created, tt.notify)
			if err := qs.Enqueue(msg); err != nil {
				t.Fatal(err)
			}
			worker.handleFailure(msg, "connection refused")
			got := drain(deliverer)
			if len(got) != tt.want {
				t.Fatalf("deliveries = %d, want %d", len(got), tt.want)
			}
			if tt.want == 1 && strings.Contains(string(got[0].data), "Action: delayed") {
				t.Error("bounce path produced a delay report")
			}
		})
	}
}

// A MarkFailed that does not persist must not leave the flag claiming a report
// went out, and no report is sent for a record that was not saved.
func TestDelayReportNotSentWhenRecordNotSaved(t *testing.T) {
	created := time.Unix(1_800_000_000, 0)
	clock := created.Add(5 * time.Hour)
	worker, _, deliverer, hash := delayEnv(t, &clock)
	// Not enqueued: MarkFailed cannot find the pending key.
	msg := delayMsg("ghost", hash, created, mail.NotifyDelay)
	worker.handleFailure(msg, "connection refused")
	if n := len(drain(deliverer)); n != 0 {
		t.Errorf("deliveries = %d, want 0", n)
	}
	if msg.DelayReportSent {
		t.Error("flag left set after a failed save")
	}
}

func TestWithDelayWarningAfter(t *testing.T) {
	worker, _, _ := newWorkerTestEnv(t, false)
	if worker.delayWarning != 4*time.Hour {
		t.Fatalf("default = %v, want 4h", worker.delayWarning)
	}
	WithDelayWarningAfter(90 * time.Minute)(worker)
	if worker.delayWarning != 90*time.Minute {
		t.Errorf("set = %v", worker.delayWarning)
	}
	WithDelayWarningAfter(0)(worker)
	WithDelayWarningAfter(-time.Second)(worker)
	if worker.delayWarning != 90*time.Minute {
		t.Errorf("non-positive changed it to %v", worker.delayWarning)
	}
}
