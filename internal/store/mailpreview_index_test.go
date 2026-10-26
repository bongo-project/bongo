package store

import (
	"path/filepath"
	"testing"
)

// oldMailDocumentDDL is the maildocument table as shipped before the preview
// columns, copied literally.
const oldMailDocumentDDL = `
CREATE TABLE storeobject (
	guid TEXT PRIMARY KEY, collection_guid TEXT, type INTEGER NOT NULL,
	flags INTEGER DEFAULT 0, size INTEGER, created_at INTEGER, modified_at INTEGER
);
CREATE TABLE maildocument (
	guid TEXT PRIMARY KEY REFERENCES storeobject(guid),
	conversation_guid TEXT, subject TEXT, sender TEXT, message_id TEXT,
	parent_message_id TEXT, list_id TEXT, received_at INTEGER
);
INSERT INTO storeobject (guid, type) VALUES ('g1', 1);
INSERT INTO maildocument (guid, subject) VALUES ('g1', 'old subject');
`

func TestUserIndexAddsPreviewColumnsIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "u.db")
	db, err := openSQLiteDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(oldMailDocumentDDL); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for pass := 1; pass <= 2; pass++ {
		idx, err := newUserIndex(path, "u")
		if err != nil {
			t.Fatalf("open pass %d: %v", pass, err)
		}
		var subject string
		var preview *string
		var att int
		if err := idx.db.QueryRow(`SELECT subject, preview, has_attachments FROM maildocument WHERE guid='g1'`).
			Scan(&subject, &preview, &att); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if subject != "old subject" || preview != nil || att != 0 {
			t.Fatalf("pass %d: row = %q %v %d", pass, subject, preview, att)
		}
		pending, err := idx.PendingPreviews("", 10)
		if err != nil || len(pending) != 1 || pending[0] != "g1" {
			t.Fatalf("pass %d: pending = %v, %v", pass, pending, err)
		}
		idx.db.Close()
	}
}

func TestMailPreviewSurvivesReindex(t *testing.T) {
	mgr := newTestManager(t)
	if err := mgr.IndexMail("u", "g1", "s", "a@x", 100, 10); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetMailPreview("u", "g1", "hello", true); err != nil {
		t.Fatal(err)
	}
	// A flag or tag change re-runs IndexMail.
	if err := mgr.IndexMail("u", "g1", "s2", "a@x", 100, 10); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.MailPreviews("u", []string{"g1", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := got["g1"]; !ok || r.Preview != "hello" || !r.HasAttachments {
		t.Fatalf("preview lost after reindex: %+v", got)
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("unknown guid returned")
	}
}

func TestMailPreviewsOmitsPendingRows(t *testing.T) {
	mgr := newTestManager(t)
	if err := mgr.IndexMail("u", "g1", "s", "f", 1, 1); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.MailPreviews("u", []string{"g1"})
	if err != nil || len(got) != 0 {
		t.Fatalf("pending row: %v, %v", got, err)
	}
}

func TestPendingPreviewsExcludesFilledAndEmpty(t *testing.T) {
	mgr := newTestManager(t)
	for _, g := range []string{"a", "b", "c", "d"} {
		if err := mgr.IndexMail("u", g, "s", "f", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.SetMailPreview("u", "a", "text", false); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetMailPreview("u", "b", "", false); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.PendingPreviews("u", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("pending = %v, want [c d]", got)
	}
	got, _ = mgr.PendingPreviews("u", "c", 10)
	if len(got) != 1 || got[0] != "d" {
		t.Fatalf("after cursor = %v", got)
	}
	got, _ = mgr.PendingPreviews("u", "", 1)
	if len(got) != 1 {
		t.Fatalf("limit ignored: %v", got)
	}
}
