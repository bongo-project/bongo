package store

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// legacyMailboxMetaBytes encodes a MailboxMeta the way a build from before the
// unsubscribed field existed did: fields 1 to 3 and nothing else. Hand-encoding
// is the point. Marshalling a current MailboxMeta with Unsubscribed left false
// happens to produce the same bytes today, so it would keep passing if someone
// changed the field's default, and it would not catch a switch to the positive
// sense at all.
func legacyMailboxMetaBytes(t *testing.T, name string, uidValidity, uidNext uint32) []byte {
	t.Helper()

	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, name)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uidValidity))
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(uidNext))

	assertNoSubscriptionField(t, b)
	return b
}

// assertNoSubscriptionField fails if the encoding carries field 4, so the
// upgrade-safety tests cannot quietly start testing a record that already
// declares its subscription state.
func assertNoSubscriptionField(t *testing.T, encoded []byte) {
	t.Helper()

	rest := encoded
	for len(rest) > 0 {
		num, _, n := protowire.ConsumeField(rest)
		if n < 0 {
			t.Fatalf("decoding mailbox meta: %v", protowire.ParseError(n))
		}
		if num == 4 {
			t.Fatal("encoded mailbox meta carries field 4; it is meant to predate the subscription field")
		}
		rest = rest[n:]
	}
}

// TestLegacyMailboxMetaDecodesAsSubscribed is the upgrade-safety test. Every
// mailbox already on disk was written without field 4, so it decodes as the
// proto3 zero value. The field is stored inverted precisely so that zero value
// means "subscribed": were it stored positively, this decode would report every
// existing folder of every existing user as unsubscribed on the first restart
// after the upgrade, and clients would stop listing them.
func TestLegacyMailboxMetaDecodesAsSubscribed(t *testing.T) {
	encoded := legacyMailboxMetaBytes(t, "Archive", 42, 7)

	var meta bongov1.MailboxMeta
	if err := proto.Unmarshal(encoded, &meta); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if meta.GetUnsubscribed() {
		t.Fatal("a mailbox written before the subscription field existed decoded as unsubscribed; " +
			"the field must be stored inverted so the zero value means subscribed")
	}
	// The rest of the record must still survive, or the test above is passing
	// on a record that failed to decode at all.
	if meta.GetName() != "Archive" || meta.GetUidValidity() != 42 || meta.GetUidNext() != 7 {
		t.Fatalf("legacy meta decoded as %+v, want Archive/42/7", &meta)
	}
}

// TestSetSubscribedRoundTrip covers the store half of SUBSCRIBE/UNSUBSCRIBE:
// the state has to survive the write, not merely be accepted.
func TestSetSubscribedRoundTrip(t *testing.T) {
	ms := newMailboxTestEnv(t)
	const userID = "user1"

	if err := ms.CreateMailbox(userID, "Archive"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	tests := []struct {
		name            string
		subscribe       bool
		wantUnsubscribe bool
	}{
		{name: "newly created mailbox starts subscribed", wantUnsubscribe: false},
		{name: "unsubscribe", subscribe: false, wantUnsubscribe: true},
		{name: "resubscribe", subscribe: true, wantUnsubscribe: false},
		{name: "unsubscribe is idempotent", subscribe: false, wantUnsubscribe: true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The first case asserts the state CreateMailbox leaves behind, so
			// it must not issue a write of its own.
			if i > 0 {
				if err := ms.SetSubscribed(userID, "Archive", tt.subscribe); err != nil {
					t.Fatalf("SetSubscribed(%v): %v", tt.subscribe, err)
				}
			}
			meta, err := ms.GetMailbox(userID, "Archive")
			if err != nil {
				t.Fatalf("GetMailbox: %v", err)
			}
			if meta.GetUnsubscribed() != tt.wantUnsubscribe {
				t.Fatalf("Unsubscribed = %v, want %v", meta.GetUnsubscribed(), tt.wantUnsubscribe)
			}
		})
	}
}

// TestSetSubscribedPreservesUidNext guards the reason SUBSCRIBE is a synthetic
// command applied inside the FSM rather than a plain PUT of a meta marshalled
// on the session's side. A stale write-back would roll uid_next backwards and
// hand the next delivered message a UID that is already in use.
func TestSetSubscribedPreservesUidNext(t *testing.T) {
	ms := newMailboxTestEnv(t)
	const userID = "user1"

	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if _, err := ms.AppendMessage(userID, "INBOX", "guid-1", 0, 0); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	before, err := ms.GetMailbox(userID, "INBOX")
	if err != nil {
		t.Fatalf("GetMailbox before: %v", err)
	}

	if err := ms.SetSubscribed(userID, "INBOX", false); err != nil {
		t.Fatalf("SetSubscribed: %v", err)
	}

	after, err := ms.GetMailbox(userID, "INBOX")
	if err != nil {
		t.Fatalf("GetMailbox after: %v", err)
	}
	if after.GetUidNext() != before.GetUidNext() {
		t.Fatalf("uid_next moved from %d to %d across a subscription change",
			before.GetUidNext(), after.GetUidNext())
	}
	if after.GetUidValidity() != before.GetUidValidity() {
		t.Fatalf("uid_validity moved from %d to %d across a subscription change",
			before.GetUidValidity(), after.GetUidValidity())
	}
	if !after.GetUnsubscribed() {
		t.Fatal("SetSubscribed(false) did not take effect, so the preservation check above proves nothing")
	}
}

func TestSetSubscribedRejectsUnknownMailbox(t *testing.T) {
	ms := newMailboxTestEnv(t)
	const userID = "user1"

	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	for _, subscribe := range []bool{true, false} {
		err := ms.SetSubscribed(userID, "NoSuchMailbox", subscribe)
		if err == nil {
			t.Fatalf("SetSubscribed(%v) on a mailbox that does not exist returned nil", subscribe)
		}
		if !strings.Contains(err.Error(), "NoSuchMailbox") {
			t.Fatalf("SetSubscribed error %q does not name the mailbox", err)
		}
	}
}

// TestApplyMailboxSubscribeRejectsUnknownMailbox exercises the FSM-side check
// directly. SetSubscribed's pre-flight check cannot be the only one: it races a
// concurrent DELETE, and a follower replaying the log never runs it.
func TestApplyMailboxSubscribeRejectsUnknownMailbox(t *testing.T) {
	ms := newMailboxTestEnv(t)

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxSubscribeKey("user1", "Ghost"),
		Value: []byte(`{"subscribed":true}`),
	}
	if _, err := ApplyMailboxCommand(ms.store, cmd); err == nil {
		t.Fatal("applying a subscribe command for a mailbox that does not exist returned nil")
	}
}

// TestRenamePreservesSubscription: a rename is the same mailbox under a new
// name, so it must not resubscribe a folder the user unsubscribed.
func TestRenamePreservesSubscription(t *testing.T) {
	ms := newMailboxTestEnv(t)
	const userID = "user1"

	if err := ms.CreateMailbox(userID, "Old"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if err := ms.SetSubscribed(userID, "Old", false); err != nil {
		t.Fatalf("SetSubscribed: %v", err)
	}
	if err := ms.RenameMailbox(userID, "Old", "New"); err != nil {
		t.Fatalf("RenameMailbox: %v", err)
	}

	meta, err := ms.GetMailbox(userID, "New")
	if err != nil {
		t.Fatalf("GetMailbox: %v", err)
	}
	if !meta.GetUnsubscribed() {
		t.Fatal("rename resubscribed a mailbox the user had unsubscribed")
	}
}

// TestDefaultMailboxesAreSubscribed pins the guarantee that the inverted field
// gives for free today. If mailbox creation ever starts setting the field
// explicitly, this fails rather than INBOX quietly vanishing from clients.
func TestDefaultMailboxesAreSubscribed(t *testing.T) {
	ms := newMailboxTestEnv(t)
	const userID = "user1"

	if err := ms.EnsureDefaultMailboxes(userID); err != nil {
		t.Fatalf("EnsureDefaultMailboxes: %v", err)
	}

	for _, name := range DefaultMailboxes {
		meta, err := ms.GetMailbox(userID, name)
		if err != nil {
			t.Fatalf("GetMailbox(%q): %v", name, err)
		}
		if meta.GetUnsubscribed() {
			t.Errorf("default mailbox %q was created unsubscribed", name)
		}
	}
}
