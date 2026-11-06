package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// gapsAutoconfig uses no default port and a host that is not mail.example.com,
// so a hard-coded value or a swapped field cannot match by coincidence.
func gapsAutoconfig() *AutoconfigHandler {
	return NewAutoconfigHandler("mx.example.com", 143, 1993, 2525, 4465)
}

type gapsRow struct{ label, value string }

// Every row of the Add a device settings is checked against literals for the
// non-default values and against ClientSettings for the rest, so neither a
// value fixed in the handler nor CalDAV and CardDAV swapped can pass.
func TestSettingGroupsMatchClientSettingsRowByRow(t *testing.T) {
	e := newSecEnv(t)
	auto := gapsAutoconfig()
	e.h.SetClientSettings(auto.ClientSettings)

	r := httptest.NewRequest(http.MethodPost, devicesURL, nil)
	r.Host = "other.example.org" // the configured host must win
	cs := auto.ClientSettings(r)
	if cs.Host != "mx.example.com" {
		t.Fatalf("fixture: ClientSettings host = %q", cs.Host)
	}
	if cs.CalDAV.Path == cs.CardDAV.Path {
		t.Fatalf("fixture: CalDAV and CardDAV share a path")
	}

	groups := e.h.settingGroups(r, "en", "alice@example.com")

	type want struct {
		title string
		rows  []gapsRow
	}
	mail := func(title, port, sec string) want {
		return want{title, []gapsRow{{"Server", "mx.example.com"}, {"Port", port}, {"Encryption", sec}}}
	}
	dav := func(title, port, path string) want {
		return want{title, []gapsRow{{"Server", "mx.example.com"}, {"Port", port}, {"Path", path}}}
	}
	davPort := strconv.Itoa(cs.CalDAV.Port)
	wants := []want{
		{"", []gapsRow{{"User name", "alice@example.com"}}},
		mail("Incoming mail (IMAP)", "1993", "SSL"),
		mail("Outgoing mail (SMTP) - 4465", "4465", "SSL"),
		mail("Outgoing mail (SMTP) - 2525", "2525", "STARTTLS"),
		dav("Calendar (CalDAV)", davPort, "/caldav/"),
		dav("Contacts (CardDAV)", davPort, "/carddav/"),
	}
	if len(groups) != len(wants) {
		t.Fatalf("%d groups, want %d: %+v", len(groups), len(wants), groups)
	}
	for i, w := range wants {
		g := groups[i]
		if g.Title != w.title {
			t.Errorf("group %d title = %q, want %q", i, g.Title, w.title)
		}
		if len(g.Rows) != len(w.rows) {
			t.Errorf("group %d has %d rows, want %d", i, len(g.Rows), len(w.rows))
			continue
		}
		for j, row := range w.rows {
			if g.Rows[j].Label != row.label || g.Rows[j].Value != row.value {
				t.Errorf("group %d row %d = %q: %q, want %q: %q", i, j, g.Rows[j].Label, g.Rows[j].Value, row.label, row.value)
			}
		}
	}

	// The same rows, derived from ClientSettings, so a change to the settings
	// (a new default port) moves both.
	if got, want := groups[1].Rows[1].Value, strconv.Itoa(cs.IMAP.Port); got != want {
		t.Errorf("IMAP port row = %q, ClientSettings says %q", got, want)
	}
	for i, ep := range cs.SMTP {
		if got, want := groups[2+i].Rows[1].Value, strconv.Itoa(ep.Port); got != want {
			t.Errorf("SMTP %d port row = %q, ClientSettings says %q", i, got, want)
		}
	}
}

// The page shows the groups in this order, with the values from the configured
// settings.
func TestAddDeviceRevealShowsConfiguredSettingsInOrder(t *testing.T) {
	e := newSecEnv(t)
	e.h.SetClientSettings(gapsAutoconfig().ClientSettings)
	w := e.post(t, e.h.CreateDevice, secUserA, devicesURL, createForm("laptop", secPassword, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	last := -1
	for _, s := range []string{
		secUserA + "@example.com", "Incoming mail (IMAP)", "1993",
		"Outgoing mail (SMTP) - 4465", "4465", "Outgoing mail (SMTP) - 2525", "2525",
		"Calendar (CalDAV)", "/caldav/", "Contacts (CardDAV)", "/carddav/",
	} {
		i := strings.Index(body[max(last, 0):], s)
		if i < 0 {
			t.Fatalf("%q missing or out of order after offset %d", s, last)
		}
		last = max(last, 0) + i
	}
	if !strings.Contains(body, "mx.example.com") {
		t.Error("the configured host is not on the page")
	}
}

// createdAt is late in a UTC day, so a conversion to any zone east of UTC would
// show the next day.
func TestAppPasswordRowShowsCreatedDate(t *testing.T) {
	e := newSecEnv(t)
	created := time.Date(2026, 3, 7, 23, 50, 0, 0, time.UTC)
	rec := &bongov1.AppPassword{Id: "abcdef", Label: "idle-handset", CreatedAt: created.Unix(), PasswordHash: []byte("h")}
	if err := e.recs.Put(secUserA, rec); err != nil {
		t.Fatal(err)
	}
	body := e.get(t, secUserA).Body.String()
	i := strings.Index(body, "idle-handset")
	if i < 0 {
		t.Fatal("the row is not listed")
	}
	row := body[i:]
	if end := strings.Index(row, "</tr>"); end >= 0 {
		row = row[:end]
	}
	if !strings.Contains(row, "<td>Sat 7 Mar 2026</td>") {
		t.Errorf("the Created cell lacks the created date: %s", row)
	}
	if !strings.Contains(row, "Never") {
		t.Errorf("an unused credential is not shown as never used: %s", row)
	}
	// A credential with a different last-used date shows both, Created then Last used, in that column order.
	used := &bongov1.AppPassword{Id: "bbbbbb", Label: "active-handset", CreatedAt: created.Unix(), LastUsedAt: time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC).Unix(), PasswordHash: []byte("h")}
	if err := e.recs.Put(secUserA, used); err != nil {
		t.Fatal(err)
	}
	body = e.get(t, secUserA).Body.String()
	i = strings.Index(body, "active-handset")
	row = body[i:]
	if end := strings.Index(row, "</tr>"); end >= 0 {
		row = row[:end]
	}
	// Adjacent cells in column order: a swap of Created and Last used fails here.
	if !strings.Contains(row, "<td>Sat 7 Mar 2026</td><td>Thu 2 Apr 2026</td>") {
		t.Errorf("the row lacks the created or the last-used date in their cells: %s", row)
	}
}

// A wrong current password with the revoke box ticked must change nothing:
// credentials, pending links and the session generation all stay.
func TestChangePasswordWrongCurrentRevokesNothing(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	var plain []string
	for _, l := range []string{"a", "b", "c"} {
		plain = append(plain, plaintextIn(t, e.create(t, secUserA, l).Body.String()))
	}
	link := e.mustLink(t, secUserA)
	if e.ticketCount(t, secUserA) != 1 {
		t.Fatal("fixture: no pending link")
	}

	f := url.Values{
		"current_password":     {"not-the-password"},
		"new_password":         {"a-new-password"},
		"confirm_password":     {"a-new-password"},
		"revoke_app_passwords": {"1"},
	}
	w := e.post(t, e.h.ChangePassword, secUserA, "/settings/security/password", f)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "security-invalid-password") {
		t.Fatalf("response = %d %q, want the invalid-password flash", w.Code, w.Header().Get("Location"))
	}
	if n := e.count(t, secUserA); n != 3 {
		t.Errorf("credentials after a failed change = %d, want 3", n)
	}
	if g := e.profile(t, secUserA).SessionGeneration; g != 0 {
		t.Errorf("session generation = %d, want 0", g)
	}
	if n := e.ticketCount(t, secUserA); n != 1 {
		t.Errorf("pending links = %d, want 1", n)
	}
	for i, p := range plain {
		if id, _, _, err := e.la.AuthenticateProtocol(secUserA+"@example.com", p); err != nil || id != secUserA {
			t.Errorf("credential %d no longer authenticates: %v", i, err)
		}
	}
	e.apps.Wait()
	if w := e.fetch(linkPath(t, link), fetchOpts{}); w.Code != http.StatusOK {
		t.Errorf("the pending link no longer redeems: %d", w.Code)
	}
}

// refuse is called without any middleware: SecurityHeaders defaults
// Cache-Control to no-store, which would hide a missing noStore here.
func TestRefuseSetsNoStoreItself(t *testing.T) {
	w := httptest.NewRecorder()
	refuse(w)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := w.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
}

// gatedRecords blocks the first Put after arm, to hold a create between its
// count and its write.
type gatedRecords struct {
	auth.AppPasswordRecords
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedRecords(inner auth.AppPasswordRecords) *gatedRecords {
	return &gatedRecords{AppPasswordRecords: inner, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *gatedRecords) open() { g.once.Do(func() { close(g.release) }) }

func (g *gatedRecords) Put(userID string, rec *bongov1.AppPassword) error {
	if g.armed.CompareAndSwap(true, false) {
		close(g.entered)
		<-g.release
	}
	return g.AppPasswordRecords.Put(userID, rec)
}

// A web create and a QR redemption racing at nine credentials must leave ten,
// not eleven. The web create is held between its count and its write; the
// redemption can finish only if it does not wait for the same per-user lock.
// This proves the single-node lock only: Create's documented overshoot across
// nodes is not closed here.
func TestRedeemRacingWebCreateHoldsTheCap(t *testing.T) {
	e := newLinkEnv(t, "mail.example.com")
	for range auth.MaxAppPasswordsPerUser - 1 {
		if _, _, err := e.apps.Create(secUserA, "d"); err != nil {
			t.Fatal(err)
		}
	}
	link := e.mustLink(t, secUserA)

	gate := newGatedRecords(e.recs)
	// A failed assertion must still open the gate, or the held goroutine and
	// the per-user lock would stay taken for later tests.
	t.Cleanup(gate.open)
	apps := auth.NewAppPasswords(gate, nil)
	e.h.SetAppPasswords(apps)
	e.handler.apps = apps

	gate.armed.Store(true)
	webDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { webDone <- e.create(t, secUserA, "web") }()
	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the web create never reached its write")
	}

	redeemDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { redeemDone <- e.fetch(linkPath(t, link), fetchOpts{}) }()

	// The consume comes before the lock, so a spent link means the redemption
	// has reached the point where it must wait.
	deadline := time.Now().Add(10 * time.Second)
	for e.ticketOf(t, secUserA, link).UsedAt == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the redemption never consumed the link")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var early *httptest.ResponseRecorder
	select {
	case early = <-redeemDone:
	case <-time.After(300 * time.Millisecond):
	}
	gate.open()

	web := <-webDone
	redeem := early
	if redeem == nil {
		select {
		case redeem = <-redeemDone:
		case <-time.After(10 * time.Second):
			t.Fatal("the redemption never finished")
		}
	}
	e.apps.Wait()
	if n := e.count(t, secUserA); n != auth.MaxAppPasswordsPerUser {
		t.Errorf("credentials = %d, want %d", n, auth.MaxAppPasswordsPerUser)
	}
	if web.Code != http.StatusOK {
		t.Errorf("web create = %d, want 200", web.Code)
	}
	if redeem.Code != http.StatusNotFound {
		t.Errorf("redemption = %d, want 404 once the cap was reached", redeem.Code)
	}
}

// Add a device tells a user with two-factor to wait for a fresh code, once per
// form. The expected text is a literal: i18n.T would give an empty fragment, and
// Contains of an empty string always passes.
func TestAddDeviceWaitCopyOnlyForEnrolledAccounts(t *testing.T) {
	const fragment = "wait for the next code"
	e := newSecEnv(t)
	e.h.SetClientSettings(gapsAutoconfig().ClientSettings)
	putProfileCodes(t, e.ps, secUserB, true)

	page := func(user string) string {
		w := e.do(t, e.h.AddDevice, user, http.MethodGet, devicesURL, nil, false)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", user, w.Code)
		}
		return w.Body.String()
	}

	enrolled := page(secUserB)
	forms := strings.Count(enrolled, `name="label"`)
	if forms != 2 {
		t.Fatalf("fixture: %d device forms, want Apple and Other", forms)
	}
	if got := strings.Count(enrolled, fragment); got != forms {
		t.Errorf("wait copy shown %d times for %d forms", got, forms)
	}
	if plain := page(secUserA); strings.Contains(plain, fragment) {
		t.Error("an account without two-factor is told to wait for a code")
	}
}
