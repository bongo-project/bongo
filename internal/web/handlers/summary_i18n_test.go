package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// summaryAs renders GET /summary for quotaUser in the given locale.
func (e *summaryEnv) summaryAs(t *testing.T, locale string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	req.Header.Set("X-Test-Locale", locale)
	w := httptest.NewRecorder()
	e.router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /summary = %d", w.Code)
	}
	return w.Body.String()
}

func TestSummaryFallbacksAndDatesAreLocalised(t *testing.T) {
	tests := []struct {
		locale, noSubject, noTitle, when string
	}{
		{"en", "(no subject)", "(no title)", "Fri 3 May"},
		{"de", "(kein Betreff)", "(ohne Titel)", "Fr 3 Mai"},
		{"es", "(sin asunto)", "(sin título)", "Vie 3 May"},
		{"fr", "(sans objet)", "(sans titre)", "Ven 3 Mai"},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			e := newSummaryEnv(t)
			e.mail(t, mailSeed{subject: "", toMe: true})
			// 2030-05-03 is a Friday.
			e.event(t, quotaUser, &bongov1.Event{Guid: "ev-x", Title: "", StartTime: summaryNow.Add(48 * time.Hour).Unix()})
			body := e.summaryAs(t, tc.locale)
			if got := section(t, body, "to-me"); !strings.Contains(got, tc.noSubject) {
				t.Errorf("to-me lacks %q: %s", tc.noSubject, got)
			}
			up := section(t, body, "upcoming")
			if !strings.Contains(up, tc.noTitle) {
				t.Errorf("upcoming lacks %q: %s", tc.noTitle, up)
			}
			if !strings.Contains(up, tc.when) {
				t.Errorf("upcoming lacks date %q: %s", tc.when, up)
			}
		})
	}
}

func TestSummaryShowsOnlyTheRecoveryCodeWarning(t *testing.T) {
	get := func(t *testing.T, warning, locale string) string {
		t.Helper()
		e := newSummaryEnv(t)
		req := httptest.NewRequest(http.MethodGet, "/summary?warning="+urlEncode(warning), nil)
		req.Header.Set("X-Test-Locale", locale)
		w := httptest.NewRecorder()
		e.router().ServeHTTP(w, req)
		return w.Body.String()
	}
	const sent = "Recovery code used. You have 2 code(s) remaining."
	t.Run("english", func(t *testing.T) {
		if body := get(t, sent, "en"); !strings.Contains(body, "flash-warning") || !strings.Contains(body, sent) {
			t.Errorf("recovery warning not shown: %s", body)
		}
	})
	t.Run("german", func(t *testing.T) {
		body := get(t, sent, "de")
		if !strings.Contains(body, "flash-warning") || !strings.Contains(body, "Wiederherstellungscode") || strings.Contains(body, sent) {
			t.Errorf("warning not shown in German: %s", body)
		}
	})
	t.Run("arbitrary text is not reflected", func(t *testing.T) {
		body := get(t, "Your account is locked, call 555-0100", "en")
		if strings.Contains(body, "555-0100") || strings.Contains(body, "flash-warning") {
			t.Errorf("summary reflected an arbitrary warning: %s", body)
		}
	})
}

func TestSummaryInvitationsLinkOpensTheInbox(t *testing.T) {
	tests := []struct{ locale, want string }{
		{"en", "Open INBOX"},
		{"de", "INBOX \u00f6ffnen"},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			e := newSummaryEnv(t)
			body := e.summaryAs(t, tc.locale)
			inv := section(t, body, "invitations")
			if !strings.Contains(inv, `href="/mail/INBOX"`) || !strings.Contains(inv, ">"+tc.want+"</a>") {
				t.Errorf("invitations link wrong: %s", inv)
			}
			if strings.Contains(inv, "View all") || strings.Contains(inv, "Alle anzeigen") {
				t.Errorf("invitations link still says view all: %s", inv)
			}
			if toMe := section(t, body, "to-me"); !strings.Contains(toMe, `href="/mail/view/to-me?unread=1"`) {
				t.Errorf("to-me link: %s", toMe)
			}
		})
	}
}
