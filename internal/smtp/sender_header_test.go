package smtp

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// setApplier writes PUT/DELETE commands straight to Pebble (no Raft).
type setApplier struct{ ps *store.PebbleStore }

func (a *setApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

// SendAsFixture is a real AddressStore plus the policy adapter, with users
// "u-alice" (alice@example.com) and "u-bob" (bob@example.com) provisioned.
type sendAsFixture struct {
	policy    *store.SendAsPolicy
	addresses *store.AddressStore
}

func newSendAsFixture(t *testing.T) *sendAsFixture {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	as := store.NewAddressStore(ps, &setApplier{ps: ps}, nil)

	for uid, email := range map[string]string{testutil.SendAsUserID: "Alice@Example.com", "u-bob": "bob@example.com"} {
		data, err := proto.Marshal(&bongov1.UserProfile{Email: email})
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/"+uid+"/profile"), data); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range testutil.SendAsRecords {
		rec := &bongov1.AddressRecord{UserId: r.UserID, Kind: r.Kind, CanSend: r.CanSend, Verified: r.Verified}
		if err := as.Put(r.Addr, rec); err != nil {
			t.Fatalf("Put %s: %v", r.Addr, err)
		}
	}
	return &sendAsFixture{policy: &store.SendAsPolicy{Addresses: as, Pebble: ps}, addresses: as}
}

func (f *sendAsFixture) session(t *testing.T) (*Session, *fakeSplitDeliverer, *fakeRelayQueue) {
	b, d, q := testBackend("alice@example.com", "bob@example.com", "staff@example.com")
	b.SetSenderPolicy(f.policy)
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: testutil.SendAsUserID}
	return s, d, q
}

func TestSubmissionHeaderFromSpoofRejectedAtData(t *testing.T) {
	f := newSendAsFixture(t)
	for _, rcpt := range []string{"friend@external.com", "staff@example.com"} {
		s, d, q := f.session(t)
		if err := s.Mail("alice@example.com", nil); err != nil {
			t.Fatalf("Mail: %v", err)
		}
		if err := s.Rcpt(rcpt, nil); err != nil {
			t.Fatalf("Rcpt: %v", err)
		}
		err := s.Data(strings.NewReader("From: ceo@tenantb.example\r\nSubject: x\r\n\r\nbody"))
		if smtpCode(err) != 550 {
			t.Fatalf("rcpt %s: spoofed header From should be 550, got %v", rcpt, err)
		}
		if len(q.enqueued) != 0 || len(d.localCalls) != 0 {
			t.Fatalf("rcpt %s: nothing may be enqueued or delivered (relay=%d local=%d)", rcpt, len(q.enqueued), len(d.localCalls))
		}
	}
}

func TestSubmissionRejectsUnownedNonLocalMailFrom(t *testing.T) {
	f := newSendAsFixture(t)
	s, _, _ := f.session(t)
	if code := smtpCode(s.Mail("ext@gmail.com", nil)); code != 553 {
		t.Fatalf("unowned external envelope sender should be 553, got %d", code)
	}
}

func TestSubmissionRejectsNullSenderAndNilPolicy(t *testing.T) {
	f := newSendAsFixture(t)
	s, _, _ := f.session(t)
	if code := smtpCode(s.Mail("", nil)); code != 553 {
		t.Fatalf("null sender on submission should be 553, got %d", code)
	}

	b, _, _ := testBackend("alice@example.com")
	s2 := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "u-alice"}
	if err := s2.Mail("alice@example.com", nil); err == nil {
		t.Fatal("nil policy must fail closed")
	}
}

func TestSubmissionHeaderFromTable(t *testing.T) {
	f := newSendAsFixture(t)
	for _, tc := range testutil.SendAsCases {
		t.Run(tc.Name, func(t *testing.T) {
			s, _, q := f.session(t)
			// Envelope sender is always the user's own address so the header
			// is the only variable under test.
			if err := s.Mail("alice@example.com", nil); err != nil {
				t.Fatalf("Mail: %v", err)
			}
			if err := s.Rcpt("friend@external.com", nil); err != nil {
				t.Fatalf("Rcpt: %v", err)
			}
			err := s.Data(strings.NewReader(tc.Headers + "Subject: x\r\n\r\nbody"))
			if tc.Want {
				if err != nil {
					t.Fatalf("want accept, got %v", err)
				}
				if len(q.enqueued) != 1 {
					t.Fatalf("want 1 enqueued, got %d", len(q.enqueued))
				}
				return
			}
			if code := smtpCode(err); code < 550 || code > 553 {
				t.Fatalf("want 5xx rejection, got %v", err)
			}
			if len(q.enqueued) != 0 {
				t.Fatal("rejected message was enqueued")
			}
		})
	}
}

func TestSubmissionEnvelopeFromTable(t *testing.T) {
	f := newSendAsFixture(t)
	for _, tc := range testutil.SendAsCases {
		if tc.Addr == "" { // header-only case
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			s, _, _ := f.session(t)
			err := s.Mail(tc.Addr, nil)
			if tc.Want != (err == nil) {
				t.Fatalf("Mail(%q) err=%v, want accept=%v", tc.Addr, err, tc.Want)
			}
		})
	}
}
