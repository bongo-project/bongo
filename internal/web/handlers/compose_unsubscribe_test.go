package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

func TestUnsubscribeMailtoCompose(t *testing.T) {
	const mailtoHdr = "List-Unsubscribe: <https://news.example.org/u/x>, <mailto:leave@news.example.org?subject=Leave%20me&body=please%0Aremove&cc=extra@x.test&bcc=hidden@x.test>\r\n"
	tests := []struct {
		name      string
		mailbox   string
		headers   string
		spam      float32
		wantTo    string // empty means a redirect to a blank composer
		wantInPag []string
		notInPage []string
	}{
		{"subject and body prefilled", "INBOX", mailtoHdr, 0, "leave@news.example.org", []string{"Leave me", "please", "remove"}, []string{"extra@x.test", "hidden@x.test"}},
		{"no mailto", "INBOX", "List-Unsubscribe: <https://news.example.org/u/x>\r\n", 0, "", nil, nil},
		{"two addresses refused", "INBOX", "List-Unsubscribe: <mailto:a@x.test,b@x.test>\r\n", 0, "", nil, nil},
		{"junk refused", "Junk", mailtoHdr, 0, "", nil, nil},
		{"spam score refused", "INBOX", mailtoHdr, 0.9, "", nil, nil},
		{"header injection in subject", "INBOX", "List-Unsubscribe: <mailto:leave@news.example.org?subject=Hi%0D%0ABcc:%20evil@x.test>\r\n", 0, "leave@news.example.org", []string{"HiBcc: evil@x.test"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newUnsubFixture(t)
			uid := f.deliver(unsubMsg{mailbox: tt.mailbox, guid: "g1", headers: tt.headers, spam: tt.spam})
			ch := NewComposeHandler(f.ps, f.bs, f.mailboxes, nil, nil, discardLogger())
			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), "user1", "user1@example.com")))
				})
			})
			r.Get("/compose/unsubscribe/{mailbox}/{uid}", ch.UnsubscribeMailto)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/compose/unsubscribe/%s/%d", strings.ReplaceAll(tt.mailbox, "/", "%2F"), uid), nil))
			if tt.wantTo == "" {
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/compose" {
					t.Fatalf("status %d location %q, want a redirect to /compose", w.Code, w.Header().Get("Location"))
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %.300s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.wantTo) {
				t.Errorf("composer lacks the address %q", tt.wantTo)
			}
			for _, s := range tt.wantInPag {
				if !strings.Contains(body, s) {
					t.Errorf("composer lacks %q", s)
				}
			}
			for _, s := range tt.notInPage {
				if strings.Contains(body, s) {
					t.Errorf("composer carries %q from the URI", s)
				}
			}
		})
	}

	t.Run("other user", func(t *testing.T) {
		f := newUnsubFixture(t)
		uid := f.deliver(unsubMsg{mailbox: "INBOX", guid: "g1", headers: mailtoHdr})
		ch := NewComposeHandler(f.ps, f.bs, f.mailboxes, nil, nil, discardLogger())
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), "user2", "user2@example.com")))
			})
		})
		r.Get("/compose/unsubscribe/{mailbox}/{uid}", ch.UnsubscribeMailto)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/compose/unsubscribe/INBOX/%d", uid), nil))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want a redirect", w.Code)
		}
	})
}

func TestMailtoSanitisers(t *testing.T) {
	if got := mailtoSubject("a\r\nBcc: x\x00y"); got != "aBcc: xy" {
		t.Errorf("subject = %q", got)
	}
	long := strings.Repeat("é", 600) // two bytes each
	got := mailtoSubject(long)
	if len(got) > 998 || !utf8.ValidString(got) {
		t.Errorf("subject length %d valid=%v", len(got), utf8.ValidString(got))
	}
	if got := mailtoBody("one\r\ntwo\rthree\x00\x07"); got != "one\ntwo\nthree" {
		t.Errorf("body = %q", got)
	}
	if got := mailtoBody(strings.Repeat("x", 5000)); len(got) != maxQuickReplyRunes {
		t.Errorf("body length = %d, want %d", len(got), maxQuickReplyRunes)
	}
}
