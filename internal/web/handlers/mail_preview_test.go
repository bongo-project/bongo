package handlers

import (
	"os"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// TestFillPreviewsReadsIndexNotBlobs proves the list preview comes from the
// SQLite index: the blob store is pointed at a directory that no longer exists,
// so any blob read would fail.
func TestFillPreviewsReadsIndexNotBlobs(t *testing.T) {
	env := newMailTagsTestEnv(t)
	env.seedMessage(t, "u1", "INBOX", "g1", nil)
	env.seedMessage(t, "u1", "INBOX", "g2", nil)

	blobDir := t.TempDir()
	bs, err := store.NewBlobStore(blobDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(blobDir); err != nil {
		t.Fatal(err)
	}
	env.handler.blobs = bs

	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	env.handler.SetMetadata(mm)
	for _, g := range []string{"g1", "g2"} {
		if err := mm.IndexMail("u1", g, "s", "f", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	// g1 has a computed preview; g2 is still pending (NULL).
	if err := mm.SetMailPreview("u1", "g1", "hello <b>world</b>", true); err != nil {
		t.Fatal(err)
	}

	msgs, _, err := env.mailboxes.ListMessagesPaged("u1", "INBOX", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	sums := env.handler.buildMessageSummaries("u1", msgs)
	env.handler.fillPreviews("u1", sums)

	if len(sums) != 2 {
		t.Fatalf("summaries = %d, want 2", len(sums))
	}
	for _, s := range sums {
		switch s.MailGUID {
		case "g1":
			if s.Preview != "hello <b>world</b>" || !s.HasAttachments {
				t.Errorf("g1 = %q %v", s.Preview, s.HasAttachments)
			}
		case "g2":
			if s.Preview != "" || s.HasAttachments {
				t.Errorf("pending row got %q %v", s.Preview, s.HasAttachments)
			}
		default:
			t.Errorf("unexpected guid %q", s.MailGUID)
		}
	}
}

func TestFillPreviewsWithoutMetadataIsNoop(t *testing.T) {
	env := newMailTagsTestEnv(t)
	env.seedMessage(t, "u1", "INBOX", "g1", nil)
	msgs, _, _ := env.mailboxes.ListMessagesPaged("u1", "INBOX", 0, 10)
	sums := env.handler.buildMessageSummaries("u1", msgs)
	env.handler.fillPreviews("u1", sums)
	if strings.TrimSpace(sums[0].Preview) != "" {
		t.Fatal("preview set without metadata")
	}
}

// TestPageSummariesFillsPreviews goes through the helper both list handlers
// use, so a handler that stopped calling it would lose the preview step.
func TestPageSummariesFillsPreviews(t *testing.T) {
	env := newMailTagsTestEnv(t)
	env.seedMessage(t, "u1", "INBOX", "g1", nil)
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	env.handler.SetMetadata(mm)
	if err := mm.IndexMail("u1", "g1", "s", "f", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := mm.SetMailPreview("u1", "g1", "hi there", true); err != nil {
		t.Fatal(err)
	}
	msgs, _, err := env.mailboxes.ListMessagesPaged("u1", "INBOX", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	sums, page, totalPages := env.handler.pageSummaries("u1", msgs, 9, 10)
	if page != 1 || totalPages != 1 || len(sums) != 1 {
		t.Fatalf("page=%d totalPages=%d n=%d", page, totalPages, len(sums))
	}
	if sums[0].Preview != "hi there" || !sums[0].HasAttachments {
		t.Fatalf("summary = %q %v", sums[0].Preview, sums[0].HasAttachments)
	}
}
