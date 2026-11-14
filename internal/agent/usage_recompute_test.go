package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newRecomputeTestEnv creates an isolated Pebble + DomainQuotaStore for
// testing the computeAndWriteUsage function.
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
//
// Applies are serialised, as the FSM does, and the per-user usage command is
// dispatched to the real handler: stored as a literal key it would record
// nothing and negative assertions would pass vacuously. usageCmds counts the
// usage commands seen.
type testApplier struct {
	store     *store.PebbleStore
	mu        sync.Mutex
	usageCmds int
}

func (a *testApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cmd.Op == bongov1.Op_OP_PUT && store.IsUserUsageSyntheticCommand(cmd.Key) {
		a.usageCmds++
		return store.ApplyUserUsageCommand(a.store, cmd)
	}
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
	ps, dqs, applier := newRecomputeTestEnv(t)

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

	computeAndWriteUsage(ps, dqs, applier, testLogger())

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
	ps, dqs, applier := newRecomputeTestEnv(t)

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

	computeAndWriteUsage(ps, dqs, applier, testLogger())

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
	ps, dqs, applier := newRecomputeTestEnv(t)

	// No data seeded.
	computeAndWriteUsage(ps, dqs, applier, testLogger())

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
	ps, dqs, applier := newRecomputeTestEnv(t)

	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeMailMeta(t, ps, "u1", "msg1", 1000)

	// First pass.
	computeAndWriteUsage(ps, dqs, applier, testLogger())
	// Second pass (idempotent).
	computeAndWriteUsage(ps, dqs, applier, testLogger())

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
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeRecomputeProfile(t, ps, "u2", "bob@other.com")

	writeMailMeta(t, ps, "u1", "m1", 1000)
	stored := map[string]int64{}
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
		stored[key] = int64(len(b))
	}

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	// Events and contacts count as their stored (marshalled) size; mail counts
	// once as raw_message.size.
	want := 1000 + stored["/user/u1/event/e1"] + stored["/user/u1/event/e2"] + stored["/user/u1/contact/c1"]
	if got, _ := dqs.GetUsage("example.com"); got != want {
		t.Errorf("example.com usage = %d, want %d", got, want)
	}
	if got, _ := dqs.GetUsage("other.com"); got != stored["/user/u2/contact/c2"] {
		t.Errorf("other.com usage = %d, want %d", got, stored["/user/u2/contact/c2"])
	}
	if got, _ := dqs.GetUsage("other.com"); got < 59 {
		t.Errorf("other.com usage = %d, must be at least the raw vCard size 59", got)
	}
}

// A form-created event carries no ical_raw. It must still count its stored
// size, otherwise a user can grow storage without bound past the quota.
func TestComputeAndWriteDomainUsage_CountsFormEventWithoutRaw(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")

	events := store.NewEventStore(ps, applier)
	guid, err := events.Create("u1", &bongov1.Event{
		Title:       "Planning",
		Description: strings.Repeat("d", 1337),
		Location:    "Room 4",
		StartTime:   1900000000,
		EndTime:     1900003600,
	})
	if err != nil {
		t.Fatal(err)
	}
	val, err := ps.Get([]byte("/user/u1/event/" + guid))
	if err != nil {
		t.Fatal(err)
	}

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	got, _ := dqs.GetUsage("example.com")
	if got == 0 || got != int64(len(val)) {
		t.Errorf("usage = %d, want stored record size %d (non-zero)", got, len(val))
	}
}

// A new record's pre-check estimate must equal what the recompute counts.
func TestRecordSizeEstimateMatchesRecompute(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")

	ev := &bongov1.Event{Title: "t", Description: strings.Repeat("x", 977), StartTime: 1900000000}
	ct := &bongov1.Contact{DisplayName: "Ada", Email: "ada@x.example", VcardRaw: make([]byte, 211)}
	want := store.EventRecordSize(ev) + store.ContactRecordSize(ct)
	if _, err := store.NewEventStore(ps, applier).Create("u1", ev); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewContactStore(ps, applier).Create("u1", ct); err != nil {
		t.Fatal(err)
	}

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	if got, _ := dqs.GetUsage("example.com"); got != want {
		t.Errorf("usage = %d, estimate = %d", got, want)
	}
}

// IMAP COPY/MOVE write a second MailboxMessage that points at the same
// MailGuid. No MailMeta and no blob is added, so usage must not move.
func TestComputeAndWriteDomainUsage_CopyDoesNotCount(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
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

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	if got, _ := dqs.GetUsage("example.com"); got != 1234 {
		t.Errorf("usage with copied references = %d, want 1234", got)
	}
}

// Tasks live inline in Pebble like events and count towards the owner's
// domain, at their stored size; the pre-check estimate must agree.
func TestComputeAndWriteDomainUsage_CountsTasks(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeRecomputeProfile(t, ps, "u2", "bob@other.com")

	tasks := store.NewTaskStore(ps, applier)
	t1 := &bongov1.Task{CalendarId: "tasks", Summary: "one", IcalRaw: make([]byte, 400)}
	t2 := &bongov1.Task{CalendarId: "tasks", Summary: "two", IcalRaw: make([]byte, 90)}
	t3 := &bongov1.Task{Summary: "other", IcalRaw: make([]byte, 33)}
	want1 := store.TaskRecordSize(t1) + store.TaskRecordSize(t2)
	want2 := store.TaskRecordSize(t3)
	for user, task := range map[string]*bongov1.Task{"u1": t1, "u2": t3} {
		if _, err := tasks.Create(user, task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tasks.Create("u1", t2); err != nil {
		t.Fatal(err)
	}

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	if got, _ := dqs.GetUsage("example.com"); got != want1 || got < 490 {
		t.Errorf("example.com usage = %d, want %d (tasks must count)", got, want1)
	}
	if got, _ := dqs.GetUsage("other.com"); got != want2 {
		t.Errorf("other.com usage = %d, want %d", got, want2)
	}
}

// The schedule inbox holds copies of iTIP parts of mail that is already
// counted, and Place never adds to usage, so the recompute leaves it out.
func TestComputeAndWriteDomainUsage_ExcludesScheduleInbox(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeMailMeta(t, ps, "u1", "m1", 1000)

	inbox := store.NewScheduleInboxStore(ps, applier)
	if err := inbox.Place("u1", "0123456789abcdef", make([]byte, 5000)); err != nil {
		t.Fatal(err)
	}
	if items, err := inbox.List("u1"); err != nil || len(items) != 1 {
		t.Fatalf("inbox holds %d items, %v; the test needs one", len(items), err)
	}

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	if got, _ := dqs.GetUsage("example.com"); got != 1000 {
		t.Errorf("example.com usage = %d, want 1000 (mail only)", got)
	}
}

func setRecord(t *testing.T, ps *store.PebbleStore, key string, val []byte) {
	t.Helper()
	if err := ps.Set([]byte(key), val); err != nil {
		t.Fatal(err)
	}
}

// Per-user totals cover all four record kinds, a profile-only user gets 0, a
// user with an empty domain is still counted, and the users of a domain add up
// to the domain total.
func TestComputeAndWriteUsage_PerUser(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeRecomputeProfile(t, ps, "u2", "bob@example.com")
	writeRecomputeProfile(t, ps, "u3", "carol@other.com")
	writeRecomputeProfile(t, ps, "u4", "no-domain")
	writeRecomputeProfile(t, ps, "empty", "dave@example.com")

	writeMailMeta(t, ps, "u1", "m1", 1000)
	setRecord(t, ps, "/user/u1/event/e1", []byte(strings.Repeat("e", 100)))
	setRecord(t, ps, "/user/u1/contact/c1", []byte(strings.Repeat("c", 50)))
	setRecord(t, ps, "/user/u1/task/t1", []byte(strings.Repeat("t", 7)))
	setRecord(t, ps, "/user/u1/schedinbox/1-a", []byte(strings.Repeat("s", 999)))
	writeMailMeta(t, ps, "u2", "m2", 500)
	writeMailMeta(t, ps, "u3", "m3", 4000)
	writeMailMeta(t, ps, "u4", "m4", 300)

	computeAndWriteUsage(ps, dqs, applier, testLogger())

	want := map[string]int64{"u1": 1157, "u2": 500, "u3": 4000, "u4": 300, "empty": 0}
	for id, w := range want {
		got, err := store.GetUserUsage(ps, id)
		if err != nil || got != w {
			t.Errorf("%s usage = %d, %v; want %d", id, got, err, w)
		}
		if _, err := ps.Get(store.UserUsageKey(id)); err != nil {
			t.Errorf("%s: usage key must exist (also for 0): %v", id, err)
		}
	}
	if _, err := ps.Get([]byte("/__user_usage__/0")); err == nil {
		t.Error("command stored as a literal key")
	}
	if applier.usageCmds != 1 {
		t.Errorf("usage commands = %d, want exactly 1 per pass", applier.usageCmds)
	}

	example, _ := dqs.GetUsage("example.com")
	if sum := want["u1"] + want["u2"] + want["empty"]; example != sum {
		t.Errorf("example.com = %d, users sum to %d", example, sum)
	}
	if other, _ := dqs.GetUsage("other.com"); other != want["u3"] {
		t.Errorf("other.com = %d, want %d", other, want["u3"])
	}

	// A second pass over unchanged data changes nothing.
	computeAndWriteUsage(ps, dqs, applier, testLogger())
	if got, _ := store.GetUserUsage(ps, "u1"); got != 1157 {
		t.Errorf("second pass u1 = %d", got)
	}
	if applier.usageCmds != 2 {
		t.Errorf("usage commands after two passes = %d, want 2", applier.usageCmds)
	}
}

// A user removed between the scan and the apply must not get a usage key back.
func TestComputeAndWriteUsage_RemovedUserGetsNoKey(t *testing.T) {
	ps, dqs, applier := newRecomputeTestEnv(t)
	writeRecomputeProfile(t, ps, "u1", "alice@example.com")
	writeRecomputeProfile(t, ps, "gone", "gone@example.com")
	writeMailMeta(t, ps, "gone", "m", 10)

	racer := &removeOnUsage{testApplier: applier, ps: ps, userID: "gone"}
	computeAndWriteUsage(ps, dqs, racer, testLogger())

	if _, err := ps.Get(store.UserUsageKey("gone")); err == nil {
		t.Error("usage key written for a user removed after the scan")
	}
	if _, err := ps.Get(store.UserUsageKey("u1")); err != nil {
		t.Errorf("u1 usage key missing: %v", err)
	}
}

// removeOnUsage drops a user's profile just before the usage command lands,
// standing in for an admin delete racing the pass.
type removeOnUsage struct {
	*testApplier
	ps     *store.PebbleStore
	userID string
}

func (d *removeOnUsage) ApplyCommand(cmd *bongov1.RaftCommand, to time.Duration) error {
	if store.IsUserUsageSyntheticCommand(cmd.Key) {
		if err := d.ps.Delete([]byte("/user/" + d.userID + "/profile")); err != nil {
			return err
		}
	}
	return d.testApplier.ApplyCommand(cmd, to)
}
