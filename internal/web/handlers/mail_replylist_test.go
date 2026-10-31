package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestShowMessageReplyListButton: the button follows a valid mailto: List-Post,
// taken from the stored field or, when that is empty, from the blob.
func TestShowMessageReplyListButton(t *testing.T) {
	const base = "From: Sender <sender@example.test>\r\nTo: user1@example.test\r\nSubject: S\r\nDate: Sun, 22 Feb 2026 12:00:00 +0000\r\n"
	tests := []struct {
		name     string
		listPost string
		raw      string
		want     bool
	}{
		{"stored mailto", "<mailto:list@example.org>", base + "\r\nBody\r\n", true},
		{"blob fallback", "", base + "List-Post: <mailto:list@example.org>\r\n\r\nBody\r\n", true},
		{"stored NO", "NO", base + "\r\nBody\r\n", false},
		{"blob NO", "", base + "List-Post: NO\r\n\r\nBody\r\n", false},
		{"blob https only", "", base + "List-Post: <https://example.org/p>\r\n\r\nBody\r\n", false},
		{"blob invalid address", "", base + "List-Post: <mailto:nonsense>\r\n\r\nBody\r\n", false},
		{"none", "", base + "\r\nBody\r\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ps, mailboxes, bs := setupViewSourceRouter(t, "user1")
			hash, err := bs.Put([]byte(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			meta := &bongov1.MailMeta{Guid: "g1", Subject: "S", From: "sender@example.test", ListPost: tt.listPost, RawMessage: &bongov1.BlobRef{Hash: hash}}
			uid, err := mailboxes.DeliverMessage("user1", "INBOX", meta, 0)
			if err != nil {
				t.Fatal(err)
			}
			h := NewMailHandler(ps, bs, mailboxes, discardLogger())
			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), "user1", "user1@example.test")))
				})
			})
			r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d", uid), nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			got := strings.Contains(w.Body.String(), fmt.Sprintf("/compose/reply-list/INBOX/%d", uid))
			if got != tt.want {
				t.Errorf("reply-list button present = %v, want %v", got, tt.want)
			}
			if strings.Contains(w.Body.String(), "list@example.org") {
				t.Error("list address leaked into the page")
			}
		})
	}
}
