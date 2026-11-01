package mail

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type fakeReportQueue struct {
	msgs []*bongov1.OutboundMessage
	err  error
}

func (q *fakeReportQueue) Enqueue(m *bongov1.OutboundMessage) error {
	if q.err != nil {
		return q.err
	}
	q.msgs = append(q.msgs, m)
	return nil
}

// externalFixture is a generator with external relay on. local lists the
// addresses isLocal reports as local; any other address is external.
type externalFixture struct {
	gen   *BounceGenerator
	d     *recordingDeliverer
	q     *fakeReportQueue
	blobs *store.BlobStore
}

func newExternalFixture(t *testing.T, perAddr, global int, local ...string) *externalFixture {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &externalFixture{d: &recordingDeliverer{}, q: &fakeReportQueue{}, blobs: bs}
	_, key := generateTestRSAKey(t)
	registry := NewDKIMRegistry()
	registry.Seed("example.test", NewDKIMSignerFromKey("test", "example.test", key))
	f.gen = NewBounceGenerator(f.d, bs, quietLogger(), "example.test", registry)
	isLocal := func(addr string) bool {
		for _, l := range local {
			if strings.EqualFold(l, addr) {
				return true
			}
		}
		return false
	}
	f.gen.SetExternalRelay(f.q, isLocal, NewRateLimiter(perAddr), NewRateLimiter(global))
	return f
}

func (f *externalFixture) bounce(sender string) error {
	return f.gen.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: sender}, []string{"bob@remote.test"}, "550 5.1.1 user unknown")
}

func TestExternalSenderReportIsQueuedNotDelivered(t *testing.T) {
	f := newExternalFixture(t, 5, 10)
	if err := f.bounce("alice@elsewhere.test"); err != nil {
		t.Fatalf("GenerateAndDeliver: %v", err)
	}
	if len(f.d.calls) != 0 {
		t.Fatalf("report was delivered locally: %+v", f.d.calls)
	}
	if len(f.q.msgs) != 1 {
		t.Fatalf("queued %d messages, want 1", len(f.q.msgs))
	}
	m := f.q.msgs[0]
	if m.SenderEmail != "" {
		t.Errorf("envelope sender = %q, want null (no bounce of a bounce)", m.SenderEmail)
	}
	if len(m.Recipients) != 1 || m.Recipients[0] != "alice@elsewhere.test" {
		t.Errorf("recipients = %v", m.Recipients)
	}
	if m.Origin != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL {
		t.Errorf("origin = %v, want LOCAL so the worker signs it", m.Origin)
	}
	rc, err := f.blobs.Get(m.BlobHash)
	if err != nil {
		t.Fatalf("report blob: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "To: alice@elsewhere.test") || !strings.Contains(string(body), "Action: failed") {
		t.Errorf("stored report is not the DSN:\n%s", body)
	}
	// The worker signs LOCAL mail; signing here as well would sign it twice.
	// The registry holds a key for the From domain, so a signed report would
	// carry the header.
	if strings.Contains(string(body), "DKIM-Signature:") {
		t.Error("report stored signed; the queue worker would sign it a second time")
	}
}

// A control for the test above: the same generator signs a report that goes
// the local way, which shows the registry in the fixture really can sign and
// so the unsigned assertion above is not vacuous.
func TestLocalSenderReportStillDeliveredLocallyAndSigned(t *testing.T) {
	f := newExternalFixture(t, 5, 10, "alice@example.test")
	if err := f.bounce("alice@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(f.q.msgs) != 0 {
		t.Fatalf("a local sender's report was queued for relay: %+v", f.q.msgs)
	}
	if len(f.d.calls) != 1 || !strings.Contains(string(f.d.calls[0].data), "DKIM-Signature:") {
		t.Fatalf("local report not delivered signed: %+v", f.d.calls)
	}
}

// A send-as identity on an external domain resolves to a local user, so
// isLocal says local and its reports must reach the local inbox rather than be
// mailed to that domain.
func TestSendAsExternalAddressReportDeliveredLocally(t *testing.T) {
	f := newExternalFixture(t, 5, 10, "me@gmail.test")
	if err := f.bounce("me@gmail.test"); err != nil {
		t.Fatal(err)
	}
	if len(f.q.msgs) != 0 {
		t.Fatalf("report for a local user's send-as identity was relayed: %+v", f.q.msgs)
	}
	if len(f.d.calls) != 1 || f.d.calls[0].recipients[0] != "me@gmail.test" {
		t.Fatalf("not delivered locally: %+v", f.d.calls)
	}
	// Control: the same domain, an address that is not a local identity, is relayed.
	if err := f.bounce("stranger@gmail.test"); err != nil {
		t.Fatal(err)
	}
	if len(f.q.msgs) != 1 {
		t.Fatalf("control: stranger@gmail.test not relayed (queued %d)", len(f.q.msgs))
	}
}

func TestExternalReportCaps(t *testing.T) {
	t.Run("per address", func(t *testing.T) {
		f := newExternalFixture(t, 2, 100)
		for range 5 {
			if err := f.bounce("alice@elsewhere.test"); err != nil {
				t.Fatal(err)
			}
		}
		if len(f.q.msgs) != 2 {
			t.Errorf("queued %d for one address, want the cap of 2", len(f.q.msgs))
		}
		// Control: another address has its own budget, so the cap is per address.
		if err := f.bounce("carol@elsewhere.test"); err != nil {
			t.Fatal(err)
		}
		if len(f.q.msgs) != 3 {
			t.Errorf("a second address was refused: queued %d", len(f.q.msgs))
		}
		if len(f.d.calls) != 0 {
			t.Errorf("a capped report fell back to local delivery: %+v", f.d.calls)
		}
	})
	t.Run("global", func(t *testing.T) {
		f := newExternalFixture(t, 100, 3)
		for _, s := range []string{"a@x.test", "b@x.test", "c@x.test", "d@x.test", "e@x.test"} {
			if err := f.bounce(s); err != nil {
				t.Fatal(err)
			}
		}
		if len(f.q.msgs) != 3 {
			t.Errorf("queued %d, want the global cap of 3", len(f.q.msgs))
		}
		if len(f.d.calls) != 0 {
			t.Errorf("a capped report fell back to local delivery: %+v", f.d.calls)
		}
	})
}

func TestExternalRelayDisabledKeepsOldBehaviour(t *testing.T) {
	tests := []struct {
		name  string
		setup func(g *BounceGenerator, q *fakeReportQueue)
	}{
		{"never configured", func(*BounceGenerator, *fakeReportQueue) {}},
		{"nil limiter", func(g *BounceGenerator, q *fakeReportQueue) {
			g.SetExternalRelay(q, func(string) bool { return false }, nil, NewRateLimiter(5))
		}},
		{"nil queue", func(g *BounceGenerator, _ *fakeReportQueue) {
			g.SetExternalRelay(nil, func(string) bool { return false }, NewRateLimiter(5), NewRateLimiter(5))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, q := &recordingDeliverer{}, &fakeReportQueue{}
			g := NewBounceGenerator(d, nil, quietLogger(), "example.test", nil)
			tt.setup(g, q)
			if err := g.GenerateAndDeliver(&bongov1.OutboundMessage{SenderEmail: "alice@elsewhere.test"}, []string{"b@remote.test"}, "550 no"); err != nil {
				t.Fatal(err)
			}
			if len(q.msgs) != 0 || len(d.calls) != 1 {
				t.Errorf("queued %d, delivered locally %d; want 0 and 1", len(q.msgs), len(d.calls))
			}
		})
	}
}

// A sender that is not a plain address never reaches the queue, whatever
// isLocal says: it goes the local way, where the deliver stage handles it.
func TestExternalRelayRefusesMalformedSender(t *testing.T) {
	for _, sender := range []string{"", "no-at-sign", "a@b@c.test", "a b@x.test", "a@x.test\r\nRCPT TO:<z@y>", "<a@x.test>", "a@nodot", "@x.test", "a@"} {
		t.Run(sender, func(t *testing.T) {
			f := newExternalFixture(t, 5, 10)
			_ = f.bounce(sender)
			if len(f.q.msgs) != 0 {
				t.Errorf("queued a report for %q: %+v", sender, f.q.msgs)
			}
		})
	}
	// Control: a well-formed address is relayed through the same fixture.
	f := newExternalFixture(t, 5, 10)
	_ = f.bounce("ok@x.test")
	if len(f.q.msgs) != 1 {
		t.Errorf("control: well-formed sender not relayed")
	}
}

func TestExternalRelayEnqueueFailureIsReported(t *testing.T) {
	f := newExternalFixture(t, 5, 10)
	f.q.err = errors.New("raft down")
	if err := f.bounce("alice@elsewhere.test"); err == nil {
		t.Error("an enqueue failure was swallowed")
	}
}

func TestRelayedReportWording(t *testing.T) {
	d := &recordingDeliverer{}
	g := NewBounceGenerator(d, nil, quietLogger(), "example.test", nil)
	g.SetPostmasterCopy(true, func(string) bool { return true }, "")
	orig := &bongov1.OutboundMessage{SenderEmail: "alice@example.test", RecipientDsn: []*bongov1.RecipientDSN{
		{Address: "bob@remote.test", Notify: NotifySuccess, OrcptType: "rfc822", Orcpt: "bob@orig.test"},
	}}
	if err := g.GenerateRelayedReport(orig, []string{"bob@remote.test"}); err != nil {
		t.Fatal(err)
	}
	if len(d.calls) != 1 {
		t.Fatalf("deliveries = %d, want 1 (no postmaster copy)", len(d.calls))
	}
	raw := string(d.calls[0].data)
	for _, want := range []string{"Action: relayed", "Status: 2.0.0", "Original-Recipient: rfc822; bob@orig.test", "does not return delivery="} {
		if !strings.Contains(raw, want) {
			t.Errorf("report missing %q:\n%s", want, raw)
		}
	}
	for _, banned := range []string{"Diagnostic-Code", "Reason:"} {
		if strings.Contains(raw, banned) {
			t.Errorf("relayed report carries %q", banned)
		}
	}
}
