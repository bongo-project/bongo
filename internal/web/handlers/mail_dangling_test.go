package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newDanglingHandlerEnv(t *testing.T) (*DanglingMailHandler, *store.MailboxStore, *store.BlobStore) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })

	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	applier := &directApplierTags{ps: ps}
	ms := store.NewMailboxStore(ps, applier)
	return NewDanglingMailHandler(ps, blobs, applier, nil), ms, blobs
}

func seedDanglingMessage(t *testing.T, ms *store.MailboxStore, userID, guid, seed string) {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	meta := &bongov1.MailMeta{
		Guid:       guid,
		Subject:    "lost body",
		From:       "sender@example.com",
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: hex.EncodeToString(sum[:]), Size: 1},
	}
	if _, err := ms.DeliverMessage(userID, "INBOX", meta, 0); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
}

// TestDanglingMailHandlerListAndPurge covers the read-only report and the
// destructive endpoint in sequence: List must find the record and its mailbox
// entry, Purge must remove both, and a second List must come back clean.
func TestDanglingMailHandlerListAndPurge(t *testing.T) {
	h, ms, _ := newDanglingHandlerEnv(t)
	seedDanglingMessage(t, ms, "u1", "g1", "body-the-gc-ate")

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/internal/mail/dangling", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("List status = %d, want 200", rec.Code)
	}
	var listed struct {
		Count    int                  `json:"count"`
		Dangling []store.DanglingMail `json:"dangling"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding List response: %v", err)
	}
	if listed.Count != 1 || len(listed.Dangling) != 1 {
		t.Fatalf("List reported %d records, want 1: %s", listed.Count, rec.Body.String())
	}
	if len(listed.Dangling[0].Mailboxes) != 1 {
		t.Errorf("mailbox refs = %+v, want the INBOX entry", listed.Dangling[0].Mailboxes)
	}

	rec = httptest.NewRecorder()
	h.Purge(rec, httptest.NewRequest(http.MethodPost, "/internal/mail/purge-dangling", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Purge status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var purged struct {
		Found   int      `json:"found"`
		Count   int      `json:"count"`
		Deleted []string `json:"deleted"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &purged); err != nil {
		t.Fatalf("decoding Purge response: %v", err)
	}
	if purged.Count != 1 || len(purged.Deleted) != 1 || purged.Deleted[0] != "g1" {
		t.Fatalf("Purge deleted = %v (count %d), want [g1]", purged.Deleted, purged.Count)
	}
	if len(purged.Skipped) != 0 {
		t.Errorf("skipped = %v, want none", purged.Skipped)
	}

	rec = httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/internal/mail/dangling", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding second List response: %v", err)
	}
	if listed.Count != 0 {
		t.Errorf("record still reported after purge: %s", rec.Body.String())
	}
}

// TestDanglingMailHandlerPurgeLeavesReadableMessages: the endpoint decides
// from current state, so a record whose body blob is present -- because it
// came back, or was never gone -- must not be deleted. This is the direction
// that destroys live mail if it breaks.
func TestDanglingMailHandlerPurgeLeavesReadableMessages(t *testing.T) {
	h, ms, blobs := newDanglingHandlerEnv(t)
	body := []byte("body that comes back")
	seedDanglingMessage(t, ms, "u1", "g1", string(body))
	if _, err := blobs.Put(body); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The seeded hash is sha256(body), so writing body restores exactly the
	// blob the record points at.
	rec := httptest.NewRecorder()
	h.Purge(rec, httptest.NewRequest(http.MethodPost, "/internal/mail/purge-dangling", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Purge status = %d, want 200", rec.Code)
	}
	var purged struct {
		Found   int      `json:"found"`
		Count   int      `json:"count"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &purged); err != nil {
		t.Fatalf("decoding Purge response: %v", err)
	}
	if purged.Found != 0 || purged.Count != 0 {
		t.Fatalf("found=%d count=%d, want 0/0: the body is present so nothing is dangling", purged.Found, purged.Count)
	}
}

// The MailMeta and its mailbox entry must both survive.
func TestDanglingMailHandlerPurgeKeepsMetadataWhenBodyPresent(t *testing.T) {
	h, ms, blobs := newDanglingHandlerEnv(t)
	body := []byte("readable body")
	seedDanglingMessage(t, ms, "u1", "g1", string(body))
	if _, err := blobs.Put(body); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rec := httptest.NewRecorder()
	h.Purge(rec, httptest.NewRequest(http.MethodPost, "/internal/mail/purge-dangling", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Purge status = %d, want 200", rec.Code)
	}
	msgs, err := ms.ListMessages("u1", "INBOX")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].MailGuid != "g1" {
		t.Errorf("INBOX = %+v, want the readable message still listed", msgs)
	}
}
