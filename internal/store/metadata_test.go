package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestManager(t *testing.T, opts ...MetadataManagerOption) *MetadataManager {
	t.Helper()
	mgr, err := NewMetadataManager(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return mgr
}

func TestSystemSchemaCreation(t *testing.T) {
	mgr := newTestManager(t)

	tables := []string{"users", "domains", "aliases"}
	for _, table := range tables {
		var name string
		err := mgr.System.DB().QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("system table %q not found: %v", table, err)
		}
	}
}

func TestUserDBCreation(t *testing.T) {
	mgr := newTestManager(t)

	idx, err := mgr.OpenUser("user1")
	if err != nil {
		t.Fatalf("OpenUser: %v", err)
	}

	// Verify user tables exist.
	tables := []string{"storeobject", "maildocument", "eventdocument", "conversation"}
	for _, table := range tables {
		var name string
		err := idx.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("user table %q not found: %v", table, err)
		}
	}
}

func TestPerUserInsertAndQuery(t *testing.T) {
	mgr := newTestManager(t)

	now := time.Now().Truncate(time.Second)

	// Insert objects for two different users.
	if err := mgr.IndexObject("u1", StoreObject{
		GUID: "mail-1", Type: DocTypeMail, Size: 1024, CreatedAt: now, ModifiedAt: now,
	}); err != nil {
		t.Fatalf("IndexObject u1: %v", err)
	}

	if err := mgr.IndexObject("u1", StoreObject{
		GUID: "mail-2", Type: DocTypeMail, Size: 2048, CreatedAt: now.Add(-time.Hour), ModifiedAt: now,
	}); err != nil {
		t.Fatalf("IndexObject u1 mail-2: %v", err)
	}

	if err := mgr.IndexObject("u2", StoreObject{
		GUID: "mail-3", Type: DocTypeMail, Size: 4096, CreatedAt: now, ModifiedAt: now,
	}); err != nil {
		t.Fatalf("IndexObject u2: %v", err)
	}

	// Query u1 — should see 2 mails.
	results, err := mgr.QueryByType("u1", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType u1: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 mails for u1, got %d", len(results))
	}
	// Ordered by created_at DESC.
	if results[0].GUID != "mail-1" {
		t.Errorf("first = %q, want %q", results[0].GUID, "mail-1")
	}
	if results[0].UserID != "u1" {
		t.Errorf("UserID = %q, want %q", results[0].UserID, "u1")
	}

	// Query u2 — should see 1 mail, completely isolated from u1.
	results2, err := mgr.QueryByType("u2", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType u2: %v", err)
	}
	if len(results2) != 1 {
		t.Fatalf("expected 1 mail for u2, got %d", len(results2))
	}
	if results2[0].GUID != "mail-3" {
		t.Errorf("u2 mail GUID = %q, want %q", results2[0].GUID, "mail-3")
	}
}

func TestPerUserUpsert(t *testing.T) {
	mgr := newTestManager(t)

	now := time.Now().Truncate(time.Second)
	obj := StoreObject{
		GUID: "mail-1", Type: DocTypeMail, Size: 1024, CreatedAt: now, ModifiedAt: now,
	}

	if err := mgr.IndexObject("u1", obj); err != nil {
		t.Fatalf("first IndexObject: %v", err)
	}

	obj.Size = 2048
	obj.ModifiedAt = now.Add(time.Minute)
	if err := mgr.IndexObject("u1", obj); err != nil {
		t.Fatalf("second IndexObject: %v", err)
	}

	results, err := mgr.QueryByType("u1", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 after upsert, got %d", len(results))
	}
	if results[0].Size != 2048 {
		t.Errorf("size = %d, want 2048", results[0].Size)
	}
}

func TestQueryByDateRange(t *testing.T) {
	mgr := newTestManager(t)

	base := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	objects := []StoreObject{
		{GUID: "m1", Type: DocTypeMail, CreatedAt: base.Add(-48 * time.Hour), ModifiedAt: base},
		{GUID: "m2", Type: DocTypeMail, CreatedAt: base.Add(-24 * time.Hour), ModifiedAt: base},
		{GUID: "m3", Type: DocTypeMail, CreatedAt: base, ModifiedAt: base},
		{GUID: "m4", Type: DocTypeMail, CreatedAt: base.Add(24 * time.Hour), ModifiedAt: base},
	}

	for _, obj := range objects {
		if err := mgr.IndexObject("u1", obj); err != nil {
			t.Fatalf("IndexObject %q: %v", obj.GUID, err)
		}
	}

	start := base.Add(-24 * time.Hour)
	results, err := mgr.QueryByDateRange("u1", DocTypeMail, start, base)
	if err != nil {
		t.Fatalf("QueryByDateRange: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 in range, got %d", len(results))
	}
	if results[0].GUID != "m3" {
		t.Errorf("first = %q, want %q", results[0].GUID, "m3")
	}
	if results[1].GUID != "m2" {
		t.Errorf("second = %q, want %q", results[1].GUID, "m2")
	}
}

func TestLRUEviction(t *testing.T) {
	// Set maxOpen to 3 so we can easily trigger eviction.
	mgr := newTestManager(t, WithMaxOpen(3))

	// Open 4 users — the first should be evicted.
	for i := 0; i < 4; i++ {
		userID := fmt.Sprintf("user-%d", i)
		if err := mgr.IndexObject(userID, StoreObject{
			GUID:       fmt.Sprintf("obj-%d", i),
			Type:       DocTypeMail,
			CreatedAt:  time.Now(),
			ModifiedAt: time.Now(),
		}); err != nil {
			t.Fatalf("IndexObject %s: %v", userID, err)
		}
	}

	if mgr.OpenCount() != 3 {
		t.Errorf("expected 3 open DBs after eviction, got %d", mgr.OpenCount())
	}

	// The evicted user's data should still be queryable (re-opens the DB).
	results, err := mgr.QueryByType("user-0", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType user-0: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for evicted user, got %d", len(results))
	}
}

func TestCloseUser(t *testing.T) {
	mgr := newTestManager(t)

	if err := mgr.IndexObject("u1", StoreObject{
		GUID: "obj-1", Type: DocTypeMail, CreatedAt: time.Now(), ModifiedAt: time.Now(),
	}); err != nil {
		t.Fatalf("IndexObject: %v", err)
	}

	if mgr.OpenCount() != 1 {
		t.Fatalf("expected 1 open, got %d", mgr.OpenCount())
	}

	if err := mgr.CloseUser("u1"); err != nil {
		t.Fatalf("CloseUser: %v", err)
	}

	if mgr.OpenCount() != 0 {
		t.Errorf("expected 0 open after CloseUser, got %d", mgr.OpenCount())
	}

	// Data should still be accessible after re-open.
	results, err := mgr.QueryByType("u1", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType after reopen: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result after reopen, got %d", len(results))
	}
}

func TestConcurrentUserDBs(t *testing.T) {
	mgr := newTestManager(t)

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", i)

			for j := 0; j < 5; j++ {
				err := mgr.IndexObject(userID, StoreObject{
					GUID:       fmt.Sprintf("obj-%d-%d", i, j),
					Type:       DocTypeMail,
					Size:       int64(j * 100),
					CreatedAt:  time.Now(),
					ModifiedAt: time.Now(),
				})
				if err != nil {
					errs <- fmt.Errorf("user %s obj %d: %w", userID, j, err)
					return
				}
			}

			results, err := mgr.QueryByType(userID, DocTypeMail)
			if err != nil {
				errs <- fmt.Errorf("query user %s: %w", userID, err)
				return
			}
			if len(results) != 5 {
				errs <- fmt.Errorf("user %s: expected 5 results, got %d", userID, len(results))
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestDeleteMail(t *testing.T) {
	mgr := newTestManager(t)
	userID := "u1"
	guid := "mail-del"

	// Index a mail into all three tiers.
	if err := mgr.IndexMail(userID, guid, "Subject", "from@example.com", 1000, 42); err != nil {
		t.Fatalf("IndexMail: %v", err)
	}
	if err := mgr.IndexMailTags(userID, guid, []string{"notjunk", "inbox"}); err != nil {
		t.Fatalf("IndexMailTags: %v", err)
	}

	idx, err := mgr.OpenUser(userID)
	if err != nil {
		t.Fatalf("OpenUser: %v", err)
	}
	count := func(table, col string) int {
		var n int
		if err := idx.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+col+" = ?", guid).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	if count("storeobject", "guid") != 1 || count("maildocument", "guid") != 1 || count("message_tags", "mail_guid") != 2 {
		t.Fatalf("pre-delete counts wrong: so=%d md=%d mt=%d",
			count("storeobject", "guid"), count("maildocument", "guid"), count("message_tags", "mail_guid"))
	}

	// DeleteMail must remove every derived row.
	if err := mgr.DeleteMail(userID, guid); err != nil {
		t.Fatalf("DeleteMail: %v", err)
	}
	if count("storeobject", "guid") != 0 || count("maildocument", "guid") != 0 || count("message_tags", "mail_guid") != 0 {
		t.Fatalf("post-delete counts wrong: so=%d md=%d mt=%d",
			count("storeobject", "guid"), count("maildocument", "guid"), count("message_tags", "mail_guid"))
	}

	// DeleteMail is tag-agnostic: every tag on the message is gone, not just one.
	// Check both the tags we indexed (and the count assertion above already
	// proves all message_tags rows were removed).
	for _, tag := range []string{"notjunk", "inbox"} {
		if guids, _ := mgr.QueryByTag(userID, tag); len(guids) != 0 {
			t.Fatalf("QueryByTag(%q) after delete = %v, want none", tag, guids)
		}
	}
}

func TestIndexMailTags(t *testing.T) {
	cases := []struct {
		name     string
		guid     string
		tags     []string
		wantTags []string
	}{
		{
			name:     "single tag",
			guid:     "mail-1",
			tags:     []string{"inbox"},
			wantTags: []string{"inbox"},
		},
		{
			name:     "multiple tags",
			guid:     "mail-2",
			tags:     []string{"work", "urgent", "inbox"},
			wantTags: []string{"inbox", "urgent", "work"}, // QueryByTag not used here; checked via direct query
		},
		{
			name:     "empty tags removes all",
			guid:     "mail-3",
			tags:     nil,
			wantTags: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestManager(t)

			if err := mgr.IndexMailTags("u1", tc.guid, tc.tags); err != nil {
				t.Fatalf("IndexMailTags: %v", err)
			}

			idx, err := mgr.OpenUser("u1")
			if err != nil {
				t.Fatalf("OpenUser: %v", err)
			}

			rows, err := idx.db.Query(
				`SELECT tag FROM message_tags WHERE mail_guid = ? ORDER BY tag`,
				tc.guid,
			)
			if err != nil {
				t.Fatalf("querying tags: %v", err)
			}
			defer rows.Close()

			var got []string
			for rows.Next() {
				var tag string
				if err := rows.Scan(&tag); err != nil {
					t.Fatalf("scan: %v", err)
				}
				got = append(got, tag)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}

			if len(got) != len(tc.wantTags) {
				t.Fatalf("got %d tags, want %d: %v", len(got), len(tc.wantTags), got)
			}
			for i, want := range tc.wantTags {
				if got[i] != want {
					t.Errorf("tag[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestIndexMailTagsReplace(t *testing.T) {
	mgr := newTestManager(t)

	// Set initial tags.
	if err := mgr.IndexMailTags("u1", "mail-1", []string{"inbox", "work"}); err != nil {
		t.Fatalf("first IndexMailTags: %v", err)
	}

	// Replace with different tags.
	if err := mgr.IndexMailTags("u1", "mail-1", []string{"archive"}); err != nil {
		t.Fatalf("second IndexMailTags: %v", err)
	}

	idx, err := mgr.OpenUser("u1")
	if err != nil {
		t.Fatalf("OpenUser: %v", err)
	}

	var count int
	if err := idx.db.QueryRow(`SELECT COUNT(*) FROM message_tags WHERE mail_guid = 'mail-1'`).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 tag after replace, got %d", count)
	}

	var tag string
	if err := idx.db.QueryRow(`SELECT tag FROM message_tags WHERE mail_guid = 'mail-1'`).Scan(&tag); err != nil {
		t.Fatalf("tag query: %v", err)
	}
	if tag != "archive" {
		t.Errorf("tag = %q, want %q", tag, "archive")
	}
}

func TestQueryByTag(t *testing.T) {
	cases := []struct {
		name      string
		messages  map[string][]string // guid -> tags
		queryTag  string
		wantGUIDs []string
	}{
		{
			name: "single match",
			messages: map[string][]string{
				"mail-1": {"inbox"},
				"mail-2": {"archive"},
			},
			queryTag:  "inbox",
			wantGUIDs: []string{"mail-1"},
		},
		{
			name: "multiple matches ordered",
			messages: map[string][]string{
				"mail-a": {"work", "urgent"},
				"mail-b": {"work"},
				"mail-c": {"inbox"},
			},
			queryTag:  "work",
			wantGUIDs: []string{"mail-a", "mail-b"},
		},
		{
			name: "no matches returns empty",
			messages: map[string][]string{
				"mail-1": {"inbox"},
			},
			queryTag:  "nonexistent",
			wantGUIDs: nil,
		},
		{
			name:      "empty index returns empty",
			messages:  map[string][]string{},
			queryTag:  "inbox",
			wantGUIDs: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestManager(t)

			for guid, tags := range tc.messages {
				if err := mgr.IndexMailTags("u1", guid, tags); err != nil {
					t.Fatalf("IndexMailTags %q: %v", guid, err)
				}
			}

			got, err := mgr.QueryByTag("u1", tc.queryTag)
			if err != nil {
				t.Fatalf("QueryByTag: %v", err)
			}

			if len(got) != len(tc.wantGUIDs) {
				t.Fatalf("got %d GUIDs, want %d: %v", len(got), len(tc.wantGUIDs), got)
			}
			for i, want := range tc.wantGUIDs {
				if got[i] != want {
					t.Errorf("guid[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestQueryByTagUserIsolation(t *testing.T) {
	mgr := newTestManager(t)

	// Both users tag the same GUID with "inbox".
	if err := mgr.IndexMailTags("u1", "mail-1", []string{"inbox"}); err != nil {
		t.Fatalf("IndexMailTags u1: %v", err)
	}
	if err := mgr.IndexMailTags("u2", "mail-2", []string{"inbox"}); err != nil {
		t.Fatalf("IndexMailTags u2: %v", err)
	}

	got1, err := mgr.QueryByTag("u1", "inbox")
	if err != nil {
		t.Fatalf("QueryByTag u1: %v", err)
	}
	if len(got1) != 1 || got1[0] != "mail-1" {
		t.Errorf("u1 inbox = %v, want [mail-1]", got1)
	}

	got2, err := mgr.QueryByTag("u2", "inbox")
	if err != nil {
		t.Fatalf("QueryByTag u2: %v", err)
	}
	if len(got2) != 1 || got2[0] != "mail-2" {
		t.Errorf("u2 inbox = %v, want [mail-2]", got2)
	}
}

func TestMessageTagsTableExists(t *testing.T) {
	mgr := newTestManager(t)

	idx, err := mgr.OpenUser("u1")
	if err != nil {
		t.Fatalf("OpenUser: %v", err)
	}

	var name string
	err = idx.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='message_tags'",
	).Scan(&name)
	if err != nil {
		t.Errorf("message_tags table not found: %v", err)
	}

	// Verify the index on tag column also exists.
	var indexName string
	err = idx.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_message_tags_tag'",
	).Scan(&indexName)
	if err != nil {
		t.Errorf("idx_message_tags_tag index not found: %v", err)
	}
}

func TestEmptyQueries(t *testing.T) {
	mgr := newTestManager(t)

	results, err := mgr.QueryByType("nonexistent", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	now := time.Now()
	results, err = mgr.QueryByDateRange("nonexistent", DocTypeMail, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatalf("QueryByDateRange: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
