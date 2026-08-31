package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newDanglingTestEnv builds a MailboxStore and a BlobStore over the same temp
// dir. The MailboxStore carries both the PebbleStore (ms.store) and the
// directApplier (ms.applier) the dangling-mail helpers need, so this reuses
// newMailboxTestEnv rather than standing up a second copy of it.
func newDanglingTestEnv(t *testing.T) (*MailboxStore, *BlobStore) {
	t.Helper()
	ms := newMailboxTestEnv(t)
	blobs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	return ms, blobs
}

// absentHash returns a well-formed SHA-256 hex string for content that was
// never written to the blob store. It must be well-formed: BlobStore.Exists
// returns false for a malformed hash too, so a junk string would pass the
// "missing" check for the wrong reason and prove nothing.
func absentHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// storeMail writes a MailMeta whose raw_message points at hash.
func storeMail(t *testing.T, ms *MailboxStore, userID, guid, hash string, atts ...string) {
	t.Helper()
	meta := &bongov1.MailMeta{
		Guid:       guid,
		Subject:    "subject " + guid,
		From:       guid + "@example.com",
		ReceivedAt: time.Now().Add(-2 * time.Hour).Unix(),
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: 10},
	}
	for _, a := range atts {
		meta.Attachments = append(meta.Attachments, &bongov1.BlobRef{Hash: a, Size: 5})
	}
	if err := ms.StoreMailMeta(userID, meta); err != nil {
		t.Fatalf("StoreMailMeta %s: %v", guid, err)
	}
}

// putMailboxEntry writes a MailboxMessage directly, the same way the existing
// orphan-mail test seeds mailbox references.
func putMailboxEntry(t *testing.T, ms *MailboxStore, userID, mailbox string, uid uint32, guid string) {
	t.Helper()
	b, err := proto.Marshal(&bongov1.MailboxMessage{Uid: uid, MailGuid: guid})
	if err != nil {
		t.Fatalf("marshal mailbox message: %v", err)
	}
	if err := ms.store.Set(mailboxMsgKey(userID, mailbox, uid), b); err != nil {
		t.Fatalf("set mailbox msg: %v", err)
	}
}

func putProfile(t *testing.T, ms *MailboxStore, userID string) {
	t.Helper()
	b, err := proto.Marshal(&bongov1.UserProfile{Email: userID + "@example.com"})
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := ms.store.Set([]byte("/user/"+userID+"/profile"), b); err != nil {
		t.Fatalf("set profile: %v", err)
	}
}

func findByGUID(t *testing.T, got []DanglingMail, guid string) DanglingMail {
	t.Helper()
	for _, d := range got {
		if d.GUID == guid {
			return d
		}
	}
	t.Fatalf("guid %q not reported; got %+v", guid, got)
	return DanglingMail{}
}

func hasGUID(got []DanglingMail, guid string) bool {
	for _, d := range got {
		if d.GUID == guid {
			return true
		}
	}
	return false
}

// TestFindDanglingMail is the core discrimination test: a record whose body
// blob is gone must be reported, and a record whose body is present must NOT
// be, since a false positive here deletes live mail.
func TestFindDanglingMail(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)

	liveHash, err := blobs.Put([]byte("a real message body"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	gone := absentHash("deleted-by-gc")

	storeMail(t, ms, userID, "dangling", gone)
	storeMail(t, ms, userID, "healthy", liveHash)
	// A record with no body reference at all is not "dangling": there is no
	// missing blob, so there is nothing this tool can conclude about it.
	if err := ms.StoreMailMeta(userID, &bongov1.MailMeta{Guid: "no-body"}); err != nil {
		t.Fatalf("StoreMailMeta no-body: %v", err)
	}

	got, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("reported %d records, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.GUID != "dangling" {
		t.Errorf("guid = %q, want %q", d.GUID, "dangling")
	}
	if d.UserID != userID {
		t.Errorf("user = %q, want %q", d.UserID, userID)
	}
	if d.MissingHash != gone {
		t.Errorf("missing hash = %q, want %q", d.MissingHash, gone)
	}
	if d.Subject != "subject dangling" || d.From != "dangling@example.com" {
		t.Errorf("subject/from = %q/%q, want the values from the MailMeta", d.Subject, d.From)
	}
	if d.ReceivedAt == 0 {
		t.Error("received_at not carried through from the MailMeta")
	}
	if !d.UserExists {
		t.Error("UserExists = false, but the profile key was written")
	}
}

// TestFindDanglingMailReportsMailboxEntries is the question the report exists
// to answer: is this debris, or a message sitting in someone's mailbox?
// Every referencing mailbox entry must be listed, including a second copy in
// another mailbox.
func TestFindDanglingMailReportsMailboxEntries(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	for _, mb := range []string{"INBOX", "Archive"} {
		if err := ms.CreateMailbox(userID, mb); err != nil {
			t.Fatalf("CreateMailbox %s: %v", mb, err)
		}
	}

	gone := absentHash("body-gone")
	storeMail(t, ms, userID, "g1", gone)
	putMailboxEntry(t, ms, userID, "INBOX", 7, "g1")
	putMailboxEntry(t, ms, userID, "Archive", 3, "g1")
	// An unrelated message in the same mailbox must not be attributed to g1.
	putMailboxEntry(t, ms, userID, "INBOX", 8, "other")

	got, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	d := findByGUID(t, got, "g1")
	if len(d.Mailboxes) != 2 {
		t.Fatalf("mailbox refs = %+v, want 2 (INBOX/7 and Archive/3)", d.Mailboxes)
	}
	seen := map[string]bool{}
	for _, ref := range d.Mailboxes {
		seen[fmt.Sprintf("%s/%d", ref.Mailbox, ref.UID)] = true
	}
	for _, want := range []string{"INBOX/7", "Archive/3"} {
		if !seen[want] {
			t.Errorf("mailbox ref %q missing from %+v", want, d.Mailboxes)
		}
	}
}

// TestFindDanglingMailIgnoresMailboxNamedMail: a mailbox literally named
// "mail" produces keys containing "/mail/" (/user/{id}/mailbox/mail/msg/{uid}
// and .../mail/meta) that a substring filter would accept as MailMeta keys.
// Nothing under those keys may be reported or error out.
//
// Honest scope: this test does NOT by itself prove the parse is structural.
// A MailboxMessage decoded as a MailMeta carries no raw_message, so it is
// dropped a few lines later for having no body hash and the substring bug
// stays invisible here -- verified by mutating the parser to a substring
// match, which this test survives. TestParseMailMetaKey, next to the parser,
// is what pins the parse; this test pins the outcome at this call site.
func TestFindDanglingMailIgnoresMailboxNamedMail(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	if err := ms.CreateMailbox(userID, "mail"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	putMailboxEntry(t, ms, userID, "mail", 1, "some-guid")

	got, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("reported %d records for a mailbox named \"mail\" with no MailMeta at all: %+v", len(got), got)
	}
}

// TestFindDanglingMailUserExists covers the owner check: a record under a user
// with no profile key is still dangling, but is flagged so an operator can see
// the account is gone as well.
func TestFindDanglingMailUserExists(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	putProfile(t, ms, "live")

	storeMail(t, ms, "live", "g-live", absentHash("a"))
	storeMail(t, ms, "deleted", "g-dead", absentHash("b"))

	got, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("reported %d records, want 2: %+v", len(got), got)
	}
	if !findByGUID(t, got, "g-live").UserExists {
		t.Error("UserExists = false for a user whose profile key exists")
	}
	if findByGUID(t, got, "g-dead").UserExists {
		t.Error("UserExists = true for a user with no profile key")
	}
}

// TestFindDanglingMailAttachmentsAreNotGrounds: a missing attachment is
// recorded but does not by itself make a record dangling. The body is what
// makes a message unreadable; an attachment loss is data loss inside a message
// that still opens, and deleting the message would make that worse.
func TestFindDanglingMailAttachmentsAreNotGrounds(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)

	body, err := blobs.Put([]byte("body still here"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	liveAtt, err := blobs.Put([]byte("attachment still here"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	lostAtt := absentHash("lost-attachment")

	storeMail(t, ms, userID, "body-ok-att-gone", body, lostAtt)
	storeMail(t, ms, userID, "body-gone-att-ok", absentHash("lost-body"), liveAtt)

	got, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if hasGUID(got, "body-ok-att-gone") {
		t.Error("a record with a readable body but a lost attachment was reported as dangling; purging it would destroy a message that still opens")
	}
	d := findByGUID(t, got, "body-gone-att-ok")
	if len(d.MissingAttachments) != 0 {
		t.Errorf("MissingAttachments = %v, want empty (that attachment is present)", d.MissingAttachments)
	}

	// The missing attachment is still worth reporting when the body is gone too.
	storeMail(t, ms, userID, "both-gone", absentHash("lost-body-2"), lostAtt)
	got, err = FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	d = findByGUID(t, got, "both-gone")
	if len(d.MissingAttachments) != 1 || d.MissingAttachments[0] != lostAtt {
		t.Errorf("MissingAttachments = %v, want [%s]", d.MissingAttachments, lostAtt)
	}
}

// TestDeleteDanglingMailRemovesMetaAndMailboxEntries: the delete must clear
// every referencing mailbox entry as well as the MailMeta. A surviving mailbox
// entry pointing at a deleted MailMeta lists a message that cannot be opened
// at all, and nothing in the tree cleans that up.
func TestDeleteDanglingMailRemovesMetaAndMailboxEntries(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	for _, mb := range []string{"INBOX", "Archive"} {
		if err := ms.CreateMailbox(userID, mb); err != nil {
			t.Fatalf("CreateMailbox %s: %v", mb, err)
		}
	}

	storeMail(t, ms, userID, "g1", absentHash("gone"))
	putMailboxEntry(t, ms, userID, "INBOX", 7, "g1")
	putMailboxEntry(t, ms, userID, "Archive", 3, "g1")
	putMailboxEntry(t, ms, userID, "INBOX", 8, "keep-me")

	targets, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	deleted, err := DeleteDanglingMail(ms.store, blobs, ms.applier, targets)
	if err != nil {
		t.Fatalf("DeleteDanglingMail: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "g1" {
		t.Fatalf("deleted = %v, want [g1]", deleted)
	}

	if _, err := ms.store.Get(mailMetaKey(userID, "g1")); err == nil {
		t.Error("MailMeta for g1 still present after delete")
	}
	for _, ref := range []struct {
		mailbox string
		uid     uint32
	}{{"INBOX", 7}, {"Archive", 3}} {
		if _, err := ms.store.Get(mailboxMsgKey(userID, ref.mailbox, ref.uid)); err == nil {
			t.Errorf("mailbox entry %s/%d survived; it now points at a deleted MailMeta", ref.mailbox, ref.uid)
		}
	}
	// The unrelated entry must be untouched.
	if _, err := ms.store.Get(mailboxMsgKey(userID, "INBOX", 8)); err != nil {
		t.Errorf("unrelated mailbox entry INBOX/8 was deleted: %v", err)
	}
}

// TestDeleteDanglingMailSkipsRecoveredBlob is the re-validation test. A blob
// can come back between the scan and the purge -- blob sync pulling it from a
// peer, or a restore -- and at that point the message is readable again and
// must be left completely alone.
func TestDeleteDanglingMailSkipsRecoveredBlob(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	body := []byte("body that comes back from a peer")
	hash := absentHash(string(body))
	storeMail(t, ms, userID, "returns", hash)
	putMailboxEntry(t, ms, userID, "INBOX", 1, "returns")
	storeMail(t, ms, userID, "stays-gone", absentHash("never-coming-back"))
	putMailboxEntry(t, ms, userID, "INBOX", 2, "stays-gone")

	targets, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("scan found %d records, want 2", len(targets))
	}

	// The blob reappears AFTER the scan, exactly the race being guarded.
	gotHash, err := blobs.Put(body)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if gotHash != hash {
		t.Fatalf("blob hash = %q, want %q -- the test seeded the wrong hash", gotHash, hash)
	}

	deleted, err := DeleteDanglingMail(ms.store, blobs, ms.applier, targets)
	if err != nil {
		t.Fatalf("DeleteDanglingMail: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "stays-gone" {
		t.Fatalf("deleted = %v, want [stays-gone] only", deleted)
	}
	if _, err := ms.store.Get(mailMetaKey(userID, "returns")); err != nil {
		t.Errorf("MailMeta for a message whose body came back was deleted: %v", err)
	}
	if _, err := ms.store.Get(mailboxMsgKey(userID, "INBOX", 1)); err != nil {
		t.Errorf("mailbox entry for a message whose body came back was deleted: %v", err)
	}
}

// TestDeleteDanglingMailRederivesMailboxEntries: references are recomputed at
// delete time, not taken from the caller's list. A copy made after the scan
// (IMAP COPY, a filter run) would otherwise be left pointing at a MailMeta
// this function is about to delete.
func TestDeleteDanglingMailRederivesMailboxEntries(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	for _, mb := range []string{"INBOX", "Archive"} {
		if err := ms.CreateMailbox(userID, mb); err != nil {
			t.Fatalf("CreateMailbox %s: %v", mb, err)
		}
	}

	storeMail(t, ms, userID, "g1", absentHash("gone"))
	putMailboxEntry(t, ms, userID, "INBOX", 1, "g1")

	targets, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(targets) != 1 || len(targets[0].Mailboxes) != 1 {
		t.Fatalf("scan = %+v, want one record with one mailbox ref", targets)
	}

	// A copy appears after the scan; the stale target list does not know it.
	putMailboxEntry(t, ms, userID, "Archive", 9, "g1")

	if _, err := DeleteDanglingMail(ms.store, blobs, ms.applier, targets); err != nil {
		t.Fatalf("DeleteDanglingMail: %v", err)
	}
	if _, err := ms.store.Get(mailboxMsgKey(userID, "Archive", 9)); err == nil {
		t.Error("mailbox entry created after the scan survived, leaving a dangling reference to a deleted MailMeta")
	}
}

// failingApplier fails the ApplyCommand whose key matches failKey, so the
// partial-failure ordering can be observed.
type failingApplier struct {
	inner   RaftApplier
	failKey string
	applied []string
}

func (f *failingApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	if string(cmd.Key) == f.failKey {
		return fmt.Errorf("raft apply refused")
	}
	f.applied = append(f.applied, string(cmd.Key))
	return f.inner.ApplyCommand(cmd, timeout)
}

// TestDeleteDanglingMailPartialFailureLeavesOrphanNotDangling pins the
// documented ordering. When the MailMeta delete fails after the mailbox
// entries are gone, the leftover state must be an orphaned MailMeta -- which
// sweepOrphanedMail reclaims on its own -- and never a mailbox entry pointing
// at a MailMeta that no longer exists, which nothing cleans up.
func TestDeleteDanglingMailPartialFailureLeavesOrphanNotDangling(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	storeMail(t, ms, userID, "g1", absentHash("gone"))
	putMailboxEntry(t, ms, userID, "INBOX", 1, "g1")

	targets, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}

	fa := &failingApplier{inner: ms.applier, failKey: string(mailMetaKey(userID, "g1"))}
	deleted, err := DeleteDanglingMail(ms.store, blobs, fa, targets)
	if err == nil {
		t.Fatal("expected an error when the MailMeta delete fails")
	}
	if len(deleted) != 0 {
		t.Errorf("deleted = %v, want empty: the MailMeta delete failed", deleted)
	}
	if _, err := ms.store.Get(mailMetaKey(userID, "g1")); err != nil {
		t.Errorf("MailMeta gone despite the failed apply: %v", err)
	}
	if _, err := ms.store.Get(mailboxMsgKey(userID, "INBOX", 1)); err == nil {
		t.Error("mailbox entry survived: the mailbox entries must be deleted BEFORE the MailMeta")
	}
	if len(fa.applied) != 1 || fa.applied[0] != string(mailboxMsgKey(userID, "INBOX", 1)) {
		t.Errorf("applied order = %v, want the mailbox entry first", fa.applied)
	}
}

// TestDeleteDanglingMailStopsWhenMailboxDeleteFails: if a mailbox entry cannot
// be removed, the MailMeta must survive. Deleting it anyway is the one outcome
// worse than doing nothing.
func TestDeleteDanglingMailStopsWhenMailboxDeleteFails(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	storeMail(t, ms, userID, "g1", absentHash("gone"))
	putMailboxEntry(t, ms, userID, "INBOX", 1, "g1")

	targets, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}

	fa := &failingApplier{inner: ms.applier, failKey: string(mailboxMsgKey(userID, "INBOX", 1))}
	if _, err := DeleteDanglingMail(ms.store, blobs, fa, targets); err == nil {
		t.Fatal("expected an error when the mailbox entry delete fails")
	}
	if _, err := ms.store.Get(mailMetaKey(userID, "g1")); err != nil {
		t.Errorf("MailMeta was deleted even though its mailbox entry could not be: %v", err)
	}
}

// TestDeleteDanglingMailRejectsMissingDependencies keeps the endpoint from
// silently reporting success on a node with no applier or blob store.
func TestDeleteDanglingMailRejectsMissingDependencies(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	targets := []DanglingMail{{UserID: "u1", GUID: "g1", MissingHash: absentHash("x")}}

	if _, err := DeleteDanglingMail(ms.store, blobs, nil, targets); err == nil {
		t.Error("expected an error with a nil applier")
	}
	if _, err := DeleteDanglingMail(ms.store, nil, ms.applier, targets); err == nil {
		t.Error("expected an error with a nil blob store")
	}
}

// TestFindDanglingMailNilBlobStore: a role with no blob store cannot judge
// whether a body exists, so it must report nothing rather than report every
// message on the node as lost.
func TestFindDanglingMailNilBlobStore(t *testing.T) {
	ms, _ := newDanglingTestEnv(t)
	storeMail(t, ms, "u1", "g1", absentHash("gone"))

	got, err := FindDanglingMail(ms.store, nil)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("reported %d records with no blob store to check against: %+v", len(got), got)
	}
}

// TestFindDanglingMailAfterBlobRemoval reproduces the production shape end to
// end: a message delivered normally, then its blob deleted underneath it by
// the GC.
func TestFindDanglingMailAfterBlobRemoval(t *testing.T) {
	ms, blobs := newDanglingTestEnv(t)
	userID := "u1"
	putProfile(t, ms, userID)
	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	hash, err := blobs.Put([]byte("From: a@example.com\r\n\r\nhello"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	meta := &bongov1.MailMeta{
		Guid:       "delivered",
		Subject:    "hello",
		From:       "a@example.com",
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: 28},
	}
	if _, err := ms.DeliverMessage(userID, "INBOX", meta, 0); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	got, err := FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("healthy delivered message reported as dangling: %+v", got)
	}

	// The GC removes the blob out from under the metadata.
	if err := os.Remove(filepath.Join(blobs.blobDir, hash[:2], hash)); err != nil {
		t.Fatalf("removing blob: %v", err)
	}

	got, err = FindDanglingMail(ms.store, blobs)
	if err != nil {
		t.Fatalf("FindDanglingMail: %v", err)
	}
	d := findByGUID(t, got, "delivered")
	if len(d.Mailboxes) != 1 || d.Mailboxes[0].Mailbox != "INBOX" {
		t.Errorf("mailbox refs = %+v, want one INBOX entry: this message is live in a mailbox, not debris", d.Mailboxes)
	}
}
