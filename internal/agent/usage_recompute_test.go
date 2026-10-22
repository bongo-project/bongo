package agent

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newRecomputeTestEnv creates an isolated Pebble + DomainQuotaStore for
// testing the computeAndWriteDomainUsage function.
func newRecomputeTestEnv(t *testing.T) (*store.PebbleStore, *store.DomainQuotaStore, *testApplier) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	applier := &testApplier{store: ps}
	dqs := store.NewDomainQuotaStore(ps, applier)
	return ps, dqs, applier
}

// testApplier applies Raft commands directly to Pebble for tests (no Raft).
type testApplier struct {
	store *store.PebbleStore
}

func (a *testApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

// writeRecomputeProfile writes a UserProfile protobuf into Pebble.
func writeRecomputeProfile(t *testing.T, ps *store.PebbleStore, userID, email string) {
	t.Helper()
	prof := &bongov1.UserProfile{Email: email}
	b, err := proto.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := ps.Set([]byte("/user/"+userID+"/profile"), b); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// writeMailMeta writes a MailMeta protobuf into Pebble.
func writeMailMeta(t *testing.T, ps *store.PebbleStore, userID, guid string, rawSize int64) {
	t.Helper()
	meta := &bongov1.MailMeta{
		Guid:       guid,
		RawMessage: &bongov1.BlobRef{Hash: "fakehash-" + guid, Size: rawSize},
	}
	b, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal mail meta: %v", err)
	}
	if err := ps.Set([]byte("/user/"+userID+"/mail/"+guid), b); err != nil {
		t.Fatalf("write mail meta: %v", err)
	}
}

// TestComputeAndWriteDomainUsage_BasicAggregation verifies that the recompute
// function sums raw_message.size per primary domain from seeded mail metadata.
func TestComputeAndWriteDomainUsage_BasicAggregation(t *testing.T) {
	ps, dqs, _ := newRecomputeTestEnv(t)

	// Seed two users in example.com and one in other.com.
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeRecomputeProfile(t, ps, "u2", "bob@example.com")
	writeRecomputeProfile(t, ps, "u3", "carol@other.com")

	// alice has 2 messages: 1000 + 2000 = 3000 bytes.
	writeMailMeta(t, ps, "u1", "msg1", 1000)
	writeMailMeta(t, ps, "u1", "msg2", 2000)
	// bob has 1 message: 500 bytes.
	writeMailMeta(t, ps, "u2", "msg3", 500)
	// carol has 1 message: 4000 bytes.
	writeMailMeta(t, ps, "u3", "msg4", 4000)

	computeAndWriteDomainUsage(ps, dqs, testLogger())

	// example.com: 3000 + 500 = 3500 bytes.
	usage, err := dqs.GetUsage("example.com")
	if err != nil {
		t.Fatalf("GetUsage example.com: %v", err)
	}
	if usage != 3500 {
		t.Errorf("example.com usage: want 3500, got %d", usage)
	}

	// other.com: 4000 bytes.
	usage, err = dqs.GetUsage("other.com")
	if err != nil {
		t.Fatalf("GetUsage other.com: %v", err)
	}
	if usage != 4000 {
		t.Errorf("other.com usage: want 4000, got %d", usage)
	}
}

// TestComputeAndWriteDomainUsage_AttachmentsNotDoubleCount verifies that
// attachment blob sizes are NOT summed (they are embedded in the raw message;
// summing both would double-count).
func TestComputeAndWriteDomainUsage_AttachmentsNotDoubleCount(t *testing.T) {
	ps, dqs, _ := newRecomputeTestEnv(t)

	writeRecomputeProfile(t, ps, "u1", "alice@example.com")

	// A MailMeta with raw_message.size=1000 and an attachment of 500 bytes.
	// Only raw_message.size should be counted.
	meta := &bongov1.MailMeta{
		Guid:       "msg1",
		RawMessage: &bongov1.BlobRef{Hash: "hash1", Size: 1000},
		Attachments: []*bongov1.BlobRef{
			{Hash: "att-hash1", Size: 500},
		},
	}
	b, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := ps.Set([]byte("/user/u1/mail/msg1"), b); err != nil {
		t.Fatalf("write: %v", err)
	}

	computeAndWriteDomainUsage(ps, dqs, testLogger())

	usage, err := dqs.GetUsage("example.com")
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if usage != 1000 {
		t.Errorf("usage: want 1000 (raw only), got %d (attachment double-counted?)", usage)
	}
}

// TestComputeAndWriteDomainUsage_EmptyPebble verifies that with no mail, no
// usage is written (no crash on empty store).
func TestComputeAndWriteDomainUsage_EmptyPebble(t *testing.T) {
	ps, dqs, _ := newRecomputeTestEnv(t)

	// No data seeded.
	computeAndWriteDomainUsage(ps, dqs, testLogger())

	// No usage should be set (GetUsage returns 0).
	usage, err := dqs.GetUsage("example.com")
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if usage != 0 {
		t.Errorf("empty pebble: want usage=0, got %d", usage)
	}
}

// TestComputeAndWriteDomainUsage_Idempotent verifies that running the recompute
// twice produces consistent results (overwrites, not accumulates).
func TestComputeAndWriteDomainUsage_Idempotent(t *testing.T) {
	ps, dqs, _ := newRecomputeTestEnv(t)

	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeMailMeta(t, ps, "u1", "msg1", 1000)

	// First pass.
	computeAndWriteDomainUsage(ps, dqs, testLogger())
	// Second pass (idempotent).
	computeAndWriteDomainUsage(ps, dqs, testLogger())

	usage, err := dqs.GetUsage("example.com")
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if usage != 1000 {
		t.Errorf("idempotent: want 1000 after 2 passes, got %d", usage)
	}
}

// Events and contacts keep their iCal/vCard inline in Pebble, so they count
// toward domain usage by len(ical_raw)/len(vcard_raw). Otherwise a user could
// dodge the quota through CalDAV/CardDAV.
func TestComputeAndWriteDomainUsage_CountsEventsAndContacts(t *testing.T) {
	ps, dqs, _ := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeRecomputeProfile(t, ps, "u2", "bob@other.com")

	writeMailMeta(t, ps, "u1", "m1", 1000)
	for key, msg := range map[string]proto.Message{
		"/user/u1/event/e1":   &bongov1.Event{Guid: "e1", IcalRaw: make([]byte, 317)},
		"/user/u1/event/e2":   &bongov1.Event{Guid: "e2", IcalRaw: make([]byte, 41)},
		"/user/u1/contact/c1": &bongov1.Contact{Guid: "c1", VcardRaw: make([]byte, 203)},
		"/user/u2/contact/c2": &bongov1.Contact{Guid: "c2", VcardRaw: make([]byte, 59)},
	} {
		b, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal %s: %v", key, err)
		}
		if err := ps.Set([]byte(key), b); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	computeAndWriteDomainUsage(ps, dqs, testLogger())

	if got, _ := dqs.GetUsage("example.com"); got != 1000+317+41+203 {
		t.Errorf("example.com usage = %d, want %d", got, 1000+317+41+203)
	}
	if got, _ := dqs.GetUsage("other.com"); got != 59 {
		t.Errorf("other.com usage = %d, want 59", got)
	}
}

// IMAP COPY/MOVE write a second MailboxMessage that points at the same
// MailGuid. No MailMeta and no blob is added, so usage must not move.
func TestComputeAndWriteDomainUsage_CopyDoesNotCount(t *testing.T) {
	ps, dqs, _ := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeMailMeta(t, ps, "u1", "m1", 1234)

	for _, key := range []string{
		"/user/u1/mailbox/INBOX/msg/1",
		"/user/u1/mailbox/Archive/msg/1",
		"/user/u1/mailbox/Archive/msg/2",
	} {
		b, err := proto.Marshal(&bongov1.MailboxMessage{MailGuid: "m1", Uid: 1})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := ps.Set([]byte(key), b); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	computeAndWriteDomainUsage(ps, dqs, testLogger())

	if got, _ := dqs.GetUsage("example.com"); got != 1234 {
		t.Errorf("usage with copied references = %d, want 1234", got)
	}
}
