package queue

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/mail"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// dsnFakeRelay implements both relay surfaces. DeliverOutbound reports the
// configured recipients as relayed without DSN; Deliver (which the worker must
// not use when DeliverOutbound exists) records that it was called.
type dsnFakeRelay struct {
	relayed      []string
	permFails    []string
	err          error
	deliverCalls int
}

func (f *dsnFakeRelay) Deliver(context.Context, string, []string, []byte) ([]string, error) {
	f.deliverCalls++
	return nil, f.err
}

func (f *dsnFakeRelay) DeliverOutbound(context.Context, *bongov1.OutboundMessage, []byte) ([]string, []string, error) {
	return f.permFails, f.relayed, f.err
}

func newRelayedWorker(t *testing.T, relay messageRelay) (*Worker, *QueueStore, *store.BlobStore, *mockDeliverer) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	blobs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	qs := NewQueueStore(ps, &directApplier{store: ps})
	deliverer := &mockDeliverer{messages: make(chan deliveredMsg, 100)}
	bouncer := mail.NewBounceGenerator(deliverer, blobs, slog.New(slog.NewTextHandler(io.Discard, nil)), "test.local", nil)
	w := NewWorker(qs, relay, nil, &mockLeader{leader: true}, bouncer, blobs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w, qs, blobs, deliverer
}

func relayedTestMessage(t *testing.T, blobs *store.BlobStore, sender string, origin bongov1.OutboundOrigin, authenticated bool) *bongov1.OutboundMessage {
	t.Helper()
	hash, err := blobs.Put([]byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: s\r\n\r\nHello"))
	if err != nil {
		t.Fatal(err)
	}
	return &bongov1.OutboundMessage{
		Id:                  "relayed-1",
		SenderId:            "user-a",
		SenderEmail:         sender,
		Recipients:          []string{"bob@external.com"},
		BlobHash:            hash,
		Origin:              origin,
		SenderAuthenticated: authenticated,
		RecipientDsn:        []*bongov1.RecipientDSN{{Address: "bob@external.com", Notify: mail.NotifySuccess}},
	}
}

func drainReports(d *mockDeliverer) []deliveredMsg {
	close(d.messages)
	var out []deliveredMsg
	for m := range d.messages {
		out = append(out, m)
	}
	return out
}

func TestWorkerRelayedReport(t *testing.T) {
	const local, fwd = bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, bongov1.OutboundOrigin_OUTBOUND_ORIGIN_FORWARD
	tests := []struct {
		name          string
		sender        string
		origin        bongov1.OutboundOrigin
		authenticated bool
		relayed       []string
		err           error
		want          int
	}{
		{"local sender, NOTIFY=SUCCESS to a non-DSN remote", "alice@example.com", local, false, []string{"bob@external.com"}, nil, 1},
		{"authenticated forward (control)", "alice@example.com", fwd, true, []string{"bob@external.com"}, nil, 1},
		{"nothing relayed without DSN (control)", "alice@example.com", local, false, nil, nil, 0},
		{"null sender", "", local, false, []string{"bob@external.com"}, nil, 0},
		{"unauthenticated forward", "alice@example.com", fwd, false, []string{"bob@external.com"}, nil, 0},
		{"delivery incomplete is retried, not reported", "alice@example.com", local, false, []string{"bob@external.com"}, context.DeadlineExceeded, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relay := &dsnFakeRelay{relayed: tt.relayed, err: tt.err}
			w, qs, blobs, d := newRelayedWorker(t, relay)
			if err := qs.Enqueue(relayedTestMessage(t, blobs, tt.sender, tt.origin, tt.authenticated)); err != nil {
				t.Fatal(err)
			}
			w.poll(context.Background())

			if relay.deliverCalls != 0 {
				t.Errorf("worker called Deliver %d times; DeliverOutbound must be preferred", relay.deliverCalls)
			}
			got := drainReports(d)
			if len(got) != tt.want {
				t.Fatalf("reports = %d, want %d", len(got), tt.want)
			}
			if tt.want == 1 {
				if got[0].from != "" || len(got[0].recipients) != 1 || got[0].recipients[0] != "alice@example.com" {
					t.Errorf("report envelope = from %q to %v", got[0].from, got[0].recipients)
				}
				if !strings.Contains(string(got[0].data), "Action: relayed") {
					t.Errorf("report is not an Action: relayed DSN:\n%s", got[0].data)
				}
			}
		})
	}
}

// A relayed report leaves with a null sender, so its own permanent failure
// produces no further report. The control is the same message with a sender.
func TestWorkerNoReportForFailedRelayedReport(t *testing.T) {
	for _, tt := range []struct {
		name   string
		sender string
		want   int
	}{
		{"server-generated report (null sender)", "", 0},
		{"ordinary message (control)", "alice@example.com", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			relay := &dsnFakeRelay{permFails: []string{"bob@external.com"}}
			w, qs, blobs, d := newRelayedWorker(t, relay)
			msg := relayedTestMessage(t, blobs, tt.sender, bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, false)
			msg.RecipientDsn = nil
			if err := qs.Enqueue(msg); err != nil {
				t.Fatal(err)
			}
			w.poll(context.Background())
			if got := len(drainReports(d)); got != tt.want {
				t.Errorf("reports = %d, want %d", got, tt.want)
			}
		})
	}
}

// relayedBackend accepts everything; the server's DSN setting decides whether
// the relay can pass NOTIFY on.
type relayedBackend struct{}

func (relayedBackend) NewSession(*gosmtp.Conn) (gosmtp.Session, error) { return relayedSession{}, nil }

type relayedSession struct{}

func (relayedSession) Mail(string, *gosmtp.MailOptions) error { return nil }
func (relayedSession) Rcpt(string, *gosmtp.RcptOptions) error { return nil }
func (relayedSession) Data(r io.Reader) error                 { _, err := io.Copy(io.Discard, r); return err }
func (relayedSession) Reset()                                 {}
func (relayedSession) Logout() error                          { return nil }

// TestWorkerWithRealRelayReportsRelayedOnlyWithoutDSN wires the real relay: the
// worker must reach DeliverOutbound, and only a remote without DSN yields a
// relayed report.
func TestWorkerWithRealRelayReportsRelayedOnlyWithoutDSN(t *testing.T) {
	for _, tt := range []struct {
		name      string
		serverDSN bool
		want      int
	}{
		{"remote without DSN", false, 1},
		{"remote with DSN (control)", true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := gosmtp.NewServer(relayedBackend{})
			srv.Domain = "localhost"
			srv.EnableDSN = tt.serverDSN
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = srv.Serve(l) }()
			t.Cleanup(func() { _ = srv.Close() })

			relay := bongosmtp.NewRelay(&config.SMTPRelay{Enabled: true, Host: l.Addr().String()}, "mail.example.test", slog.New(slog.NewTextHandler(io.Discard, nil)))
			w, qs, blobs, d := newRelayedWorker(t, relay)
			if err := qs.Enqueue(relayedTestMessage(t, blobs, "alice@example.com", bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL, false)); err != nil {
				t.Fatal(err)
			}
			w.poll(context.Background())
			if got := len(drainReports(d)); got != tt.want {
				t.Errorf("reports = %d, want %d", got, tt.want)
			}
		})
	}
}
