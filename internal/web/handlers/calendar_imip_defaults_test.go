package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/imip"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// seedFeedCalendar adds a subscription whose GUID sorts before "default", as
// a generated hex ID does.
func (e *imipEnv) seedFeedCalendar(t *testing.T) string {
	t.Helper()
	const guid = "0aaa11feed"
	cal := &bongov1.Calendar{Guid: guid, Name: "Feedcal", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.org/f.ics"}}
	if err := e.q.calendars.Update(quotaUser, guid, cal); err != nil {
		t.Fatal(err)
	}
	return guid
}

func TestInvitationCardOffersNoSubscription(t *testing.T) {
	e := newImipEnv(t)
	feed := e.seedFeedCalendar(t)
	if _, err := e.q.calendars.Create(quotaUser, &bongov1.Calendar{Guid: "default", Name: "Personal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.calendars.Create(quotaUser, &bongov1.Calendar{Guid: "work", Name: "Workcal"}); err != nil {
		t.Fatal(err)
	}
	body := e.view(t, e.invite(t, icsOpts{}))
	if strings.Contains(body, feed) || strings.Contains(body, "Feedcal") {
		t.Error("the card offers the subscribed calendar")
	}
	if !strings.Contains(body, `<option value="default" selected`) {
		t.Errorf("default is not preselected: %s", body)
	}
	if strings.Contains(body, `<option value="work" selected`) {
		t.Error("work is preselected")
	}
}

func TestAnswerWithoutCalendarFilesIntoDefault(t *testing.T) {
	e := newImipEnv(t)
	e.seedFeedCalendar(t)
	if _, err := e.q.calendars.Create(quotaUser, &bongov1.Calendar{Guid: "default", Name: "Personal"}); err != nil {
		t.Fatal(err)
	}
	uid := e.invite(t, icsOpts{})
	if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := e.stored(t, imipUID).CalendarId; got != "default" {
		t.Errorf("calendar = %q, want default", got)
	}
}

func TestAnswerRefusesSubscribedCalendar(t *testing.T) {
	e := newImipEnv(t)
	feed := e.seedFeedCalendar(t)
	uid := e.invite(t, icsOpts{})
	form := url.Values{"action": {"answer"}, "partstat": {"ACCEPTED"}, "calendar_id": {feed}}
	if w := e.post(quotaUser, quotaEmail, uid, form, true); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", w.Code, w.Body)
	}
	e.noEvent(t)
}

// namelessMail wraps ics as the Outlook or Google layout: a calendar part
// without a file name inside multipart/alternative, plus siblings after it.
func namelessMail(ics string, siblings ...string) string {
	s := "From: Olive <" + imipOrganiser + ">\r\nTo: " + quotaEmail + "\r\nSubject: Invitation\r\n" +
		"Authentication-Results: mx.test; spf=pass; dmarc=pass\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=OUT\r\n\r\n" +
		"--OUT\r\nContent-Type: multipart/alternative; boundary=ALT\r\n\r\n" +
		"--ALT\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nYou are invited.\r\n" +
		"--ALT\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>You are invited.</p>\r\n" +
		"--ALT\r\nContent-Type: text/calendar; method=REQUEST; charset=utf-8\r\n\r\n" + ics + "\r\n--ALT--\r\n"
	for _, sib := range siblings {
		s += "--OUT\r\n" + sib + "\r\n"
	}
	return s + "--OUT--\r\n"
}

const namedText = "Content-Type: text/plain; name=notes.txt\r\nContent-Disposition: attachment; filename=notes.txt\r\n\r\nnotes"

func (e *imipEnv) attachment(uid uint32, idx int) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.router(quotaUser, quotaEmail).ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mail/INBOX/%d/attachment/%d", uid, idx), nil))
	return w
}

func TestNamelessCalendarPartIsListedWhenNoCard(t *testing.T) {
	big := icsOpts{description: strings.Repeat("a", imip.MaxPartBytes+37)}.build("REQUEST")
	for name, ics := range map[string]string{"garbage": "not a calendar at all", "oversize": big} {
		t.Run(name, func(t *testing.T) {
			e := newImipEnv(t)
			uid := e.deliver(t, quotaUser, namelessMail(ics, namedText))
			body := e.view(t, uid)
			if strings.Contains(body, "invite-card") {
				t.Fatal("a card was built from a refused part")
			}
			if !strings.Contains(body, "invite.ics") {
				t.Fatal("the nameless part is not listed")
			}
			// The named attachment keeps index 0; the nameless one is appended.
			if !strings.Contains(body, "/attachment/0\"") || !strings.Contains(body, "/attachment/1\"") {
				t.Errorf("attachment links missing: %s", body)
			}
			if w := e.attachment(uid, 0); w.Body.String() != "notes" {
				t.Errorf("index 0 = %q, want the named attachment", w.Body)
			}
			w := e.attachment(uid, 1)
			if got := strings.TrimRight(w.Body.String(), "\r\n"); w.Code != http.StatusOK || got != strings.TrimRight(ics, "\r\n") {
				t.Fatalf("download status %d, %d bytes, want the part bytes", w.Code, w.Body.Len())
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
				t.Errorf("content type %q", ct)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("no nosniff")
			}
		})
	}
}

func TestNamelessValidInvitationShowsCardAndIsNotListed(t *testing.T) {
	e := newImipEnv(t)
	uid := e.deliver(t, quotaUser, namelessMail(icsOpts{}.build("REQUEST"), namedText))
	body := e.view(t, uid)
	if !strings.Contains(body, "invite-card") {
		t.Fatal("no card")
	}
	if strings.Contains(body, "invite.ics") {
		t.Error("the nameless part is listed next to the card")
	}
	if !strings.Contains(body, "notes.txt") || !strings.Contains(body, "/attachment/0\"") {
		t.Error("the named attachment lost its entry or index")
	}
	if w := e.attachment(uid, 0); w.Body.String() != "notes" {
		t.Errorf("index 0 = %q", w.Body)
	}
}

func TestOutlookFormListsInviteIcsOnce(t *testing.T) {
	named := "Content-Type: application/ics; name=invite.ics\r\nContent-Disposition: attachment; filename=invite.ics\r\n\r\n" + icsOpts{}.build("REQUEST")
	e := newImipEnv(t)
	uid := e.deliver(t, quotaUser, namelessMail(icsOpts{}.build("REQUEST"), named))
	body := e.view(t, uid)
	if !strings.Contains(body, "invite-card") {
		t.Fatal("no card")
	}
	if n := strings.Count(body, ">invite.ics<"); n != 1 {
		t.Errorf("invite.ics listed %d times, want 1: %s", n, body)
	}
}

func TestMessageWithoutCalendarKeepsAttachmentIndexes(t *testing.T) {
	e := newImipEnv(t)
	raw := "From: a@example.org\r\nTo: " + quotaEmail + "\r\nSubject: x\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=OUT\r\n\r\n" +
		"--OUT\r\nContent-Type: text/plain\r\n\r\nhi\r\n--OUT\r\n" + namedText + "\r\n--OUT--\r\n"
	uid := e.deliver(t, quotaUser, raw)
	body := e.view(t, uid)
	if !strings.Contains(body, "/attachment/0\"") || strings.Contains(body, "/attachment/1\"") || strings.Contains(body, "invite.ics") {
		t.Errorf("unexpected attachment list: %s", body)
	}
}
