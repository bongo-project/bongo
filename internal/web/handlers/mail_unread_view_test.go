package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// getAs is get with a locale header.
func (f *viewFixture) getAs(path, locale string) (int, string) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-Locale", locale)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func seedUnreadMix(f *viewFixture) {
	me := []string{"Me <user1@example.com>"}
	f.deliver(seedMsg{mailbox: "INBOX", guid: "Unr1", to: me, at: 300})
	f.deliver(seedMsg{mailbox: "Projects", guid: "Unr2", to: me, at: 200})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "Rd1", to: me, at: 250, flags: store.FlagSeen})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "NotMe", to: []string{"x@else.test"}, at: 400})
}

func TestToMeUnreadFilter(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"unread=1", "?unread=1", []string{"subj-Unr1", "subj-Unr2"}},
		{"no parameter", "", []string{"subj-Unr1", "subj-Rd1", "subj-Unr2"}},
		{"unread=0", "?unread=0", []string{"subj-Unr1", "subj-Rd1", "subj-Unr2"}},
		{"unread=2", "?unread=2", []string{"subj-Unr1", "subj-Rd1", "subj-Unr2"}},
		{"unread=true", "?unread=true", []string{"subj-Unr1", "subj-Rd1", "subj-Unr2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newViewFixture(t)
			seedUnreadMix(f)
			code, body := f.get("/mail/view/to-me"+tc.query, "", false)
			if code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if got := subjectsInOrder(body); !equalStrings(got, tc.want) {
				t.Errorf("subjects = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnreadFilterOnlyForToMe(t *testing.T) {
	f := newViewFixture(t)
	seedUnreadMix(f)
	_, body := f.get("/mail/INBOX?unread=1", "", false)
	if !strings.Contains(body, "subj-Rd1") {
		t.Errorf("INBOX?unread=1 filtered a real folder: %v", subjectsInOrder(body))
	}
	if strings.Contains(body, "unread=1") {
		t.Errorf("INBOX page carries unread=1")
	}
}

func TestToMeUnreadSurvivesPagination(t *testing.T) {
	setup := func(t *testing.T, infinite bool) *viewFixture {
		f := newViewFixture(t)
		me := []string{"Me <user1@example.com>"}
		for i := range 6 {
			f.deliver(seedMsg{mailbox: "INBOX", guid: fmt.Sprintf("U%d", i), to: me, at: int64(1000 - i*10)})
			f.deliver(seedMsg{mailbox: "INBOX", guid: fmt.Sprintf("R%d", i), to: me, at: int64(1005 - i*10), flags: store.FlagSeen})
		}
		if err := f.prefs.Set("user1", &bongov1.UserPreferences{MessagesPerPage: 2, InfiniteScroll: infinite}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	noRead := func(t *testing.T, body string) {
		t.Helper()
		if strings.Contains(body, "subj-R") {
			t.Errorf("a read message is listed: %v", subjectsInOrder(body))
		}
	}
	t.Run("page 2 and pagination links", func(t *testing.T) {
		f := setup(t, false)
		_, body := f.get("/mail/view/to-me?unread=1&page=2", "", false)
		if got := subjectsInOrder(body); !equalStrings(got, []string{"subj-U2", "subj-U3"}) {
			t.Errorf("page 2 = %v", got)
		}
		noRead(t, body)
		plain := strings.ReplaceAll(body, "&amp;", "&")
		if !strings.Contains(plain, `href="/mail/view/to-me?unread=1&page=3"`) || !strings.Contains(plain, `hx-get="/mail/view/to-me?unread=1&page=3"`) {
			t.Errorf("pagination lacks unread=1: %s", body)
		}
		if strings.Contains(plain, "unread=1?") {
			t.Errorf("second ? in a pagination URL")
		}
	})
	t.Run("sentinel and partial", func(t *testing.T) {
		f := setup(t, true)
		_, body := f.get("/mail/view/to-me?unread=1", "", false)
		i := strings.Index(body, "partial=1")
		if i < 0 {
			t.Fatalf("no sentinel: %s", body)
		}
		start := strings.LastIndex(body[:i], `hx-get="`)
		attr := body[start+len(`hx-get="`) : i+len("partial=1")]
		attr = strings.ReplaceAll(attr, "&amp;", "&")
		if !strings.Contains(attr, "unread=1") || !strings.Contains(attr, "page=2") || strings.Count(attr, "?") != 1 {
			t.Errorf("sentinel hx-get = %q", attr)
		}
		_, part := f.get(attr, "", true)
		noRead(t, part)
		if got := subjectsInOrder(part); !equalStrings(got, []string{"subj-U2", "subj-U3"}) {
			t.Errorf("partial = %v", got)
		}
		if !strings.Contains(strings.ReplaceAll(part, "&amp;", "&"), "unread=1&page=3&partial=1") {
			t.Errorf("next sentinel lost the filter: %s", part)
		}
	})
}

func TestToMeListURLCarriesFilter(t *testing.T) {
	f := newViewFixture(t)
	seedUnreadMix(f)
	for _, htmx := range []bool{false, true} {
		_, body := f.get("/mail/view/to-me?unread=1", "", htmx)
		if !strings.Contains(body, `data-list-url="/mail/view/to-me?unread=1"`) {
			t.Errorf("htmx=%v: no data-list-url", htmx)
		}
		if !strings.Contains(body, `hx-get="/mail/view/to-me?unread=1" hx-target="#main-content"`) {
			t.Errorf("htmx=%v: Refresh lacks unread=1", htmx)
		}
	}
	_, body := f.get("/mail/view/to-me", "", false)
	if strings.Contains(body, "unread=1") {
		t.Errorf("unfiltered view carries unread=1")
	}
	if !strings.Contains(body, `data-list-url="/mail/view/to-me"`) {
		t.Errorf("unfiltered view lacks data-list-url")
	}
}

func TestInboxShowsOnlyTheRecoveryWarning(t *testing.T) {
	const sent = "Recovery code used. You have 3 code(s) remaining."
	f := newViewFixture(t)
	seedUnreadMix(f)
	for _, htmx := range []bool{false, true} {
		_, body := f.get("/mail/INBOX?warning="+url.QueryEscape(sent), "", htmx)
		if !strings.Contains(body, "flash-warning") || !strings.Contains(body, sent) {
			t.Errorf("htmx=%v: warning missing", htmx)
		}
	}
	_, de := f.getAs("/mail/INBOX?warning="+url.QueryEscape(sent), "de")
	if !strings.Contains(de, "flash-warning") || !strings.Contains(de, "Wiederherstellungscode") || strings.Contains(de, sent) {
		t.Errorf("German warning wrong")
	}
	for _, bad := range []string{"<b>x</b>", "Call 555-0100 now"} {
		_, body := f.get("/mail/INBOX?warning="+url.QueryEscape(bad), "", false)
		if strings.Contains(body, "flash-warning") || strings.Contains(body, "&lt;b&gt;") || strings.Contains(body, "555-0100") {
			t.Errorf("%q was reflected", bad)
		}
	}
	_, body := f.get("/mail/Projects?warning="+url.QueryEscape(sent), "", false)
	if strings.Contains(body, "flash-warning") {
		t.Errorf("warning shown outside the INBOX")
	}
}
