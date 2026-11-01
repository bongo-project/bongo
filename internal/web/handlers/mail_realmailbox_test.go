package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var rowChunk = regexp.MustCompile(`(?s)data-uid="(\d+)"\s+data-real-mailbox="([^"]*)"`)

// realMailboxFixture holds a starred message in INBOX and a starred message in
// Archive that share UID 1, both tagged "work".
func realMailboxFixture(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	meta, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
	for _, name := range []string{"INBOX", "Archive"} {
		if err := mailboxes.CreateMailbox("user1", name); err != nil {
			t.Fatalf("CreateMailbox: %v", err)
		}
	}
	for i, mb := range []string{"INBOX", "Archive"} {
		guid := "rm-" + mb
		m := &bongov1.MailMeta{
			Guid:       guid,
			Subject:    "subject-in-" + mb,
			From:       "a@example.com",
			MessageId:  "<" + guid + "@example.test>",
			ReceivedAt: time.Unix(1_700_000_000+int64(i), 0).Unix(),
		}
		if _, err := mailboxes.DeliverMessage("user1", mb, m, store.FlagFlagged); err != nil {
			t.Fatalf("DeliverMessage %s: %v", mb, err)
		}
		if err := meta.IndexMailTags("user1", guid, []string{"work"}); err != nil {
			t.Fatalf("IndexMailTags: %v", err)
		}
	}

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	h.SetMetadata(meta)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), "user1", "admin@example.com")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/tag/{name}", h.ListTagMessages)
	r.Get("/mail/{mailbox}", h.ListMessages)
	return r
}

// TestListRowsCarryRealMailbox proves each row names the mailbox its message
// really lives in, so archive and unarchive can pick the right verb and URL in
// a virtual view. Both messages have UID 1, so a row labelled by UID alone, or
// a mailbox slice out of step with the messages, is caught by the subjects.
func TestListRowsCarryRealMailbox(t *testing.T) {
	r := realMailboxFixture(t)
	tests := []struct {
		name, path string
		// want maps a subject to the real mailbox its row must carry.
		want map[string]string
	}{
		{"starred", "/mail/starred", map[string]string{"subject-in-INBOX": "INBOX", "subject-in-Archive": "Archive"}},
		{"tag view", "/mail/tag/work", map[string]string{"subject-in-INBOX": "INBOX", "subject-in-Archive": "Archive"}},
		{"real mailbox", "/mail/Archive", map[string]string{"subject-in-Archive": "Archive"}},
		{"all mail", "/mail/all-mail", map[string]string{"subject-in-INBOX": "INBOX", "subject-in-Archive": "Archive"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			locs := rowChunk.FindAllStringSubmatchIndex(body, -1)
			if len(locs) != len(tt.want) {
				t.Fatalf("rows = %d, want %d", len(locs), len(tt.want))
			}
			for i, loc := range locs {
				end := len(body)
				if i+1 < len(locs) {
					end = locs[i+1][0]
				}
				row := body[loc[0]:end]
				real := body[loc[4]:loc[5]]
				var subject string
				for s := range tt.want {
					if strings.Contains(row, s) {
						subject = s
					}
				}
				if subject == "" {
					t.Fatalf("row %d has no known subject: %s", i, row)
				}
				if real != tt.want[subject] {
					t.Errorf("row for %s carries data-real-mailbox=%q, want %q", subject, real, tt.want[subject])
				}
			}
		})
	}
}
