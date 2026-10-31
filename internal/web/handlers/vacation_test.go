package handlers

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failKeyApplier fails ApplyCommand for keys containing failOn.
type failKeyApplier struct {
	*directApplierTags
	failOn string
	failed int
}

func (a *failKeyApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if a.failOn != "" && strings.Contains(string(cmd.Key), a.failOn) {
		a.failed++
		return errors.New("raft unavailable")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

type vacEnv struct {
	ps      *store.PebbleStore
	prefs   *store.PreferenceStore
	h       *VacationHandler
	applier *failKeyApplier
	logs    *bytes.Buffer
}

const vacUser = "user-vac"

func newVacEnv(t *testing.T) *vacEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	applier := &failKeyApplier{directApplierTags: &directApplierTags{ps: ps}}
	logs := &bytes.Buffer{}
	prefs := store.NewPreferenceStore(ps, applier)
	return &vacEnv{
		ps: ps, prefs: prefs, applier: applier, logs: logs,
		h: NewVacationHandler(prefs, ps, applier, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))),
	}
}

func (e *vacEnv) post(form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/settings/vacation", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), vacUser, "vac@example.com"))
	w := httptest.NewRecorder()
	e.h.Save(w, r)
	return w
}

func validVacationForm() url.Values {
	return url.Values{
		"vacation_enabled":       {"1"},
		"vacation_subject":       {"Away"},
		"vacation_body":          {"Back on Monday.\nThanks."},
		"vacation_start":         {"2030-07-01"},
		"vacation_end":           {"2030-07-05"},
		"vacation_interval_days": {"5"},
	}
}

func (e *vacEnv) putTracking(t *testing.T, senders ...string) {
	t.Helper()
	for _, s := range senders {
		if err := e.applier.ApplyCommand(&bongov1.RaftCommand{
			Op: bongov1.Op_OP_PUT, Key: store.VacationSentKey(vacUser, s), Value: store.EncodeVacationSent(time.Now()),
		}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *vacEnv) trackingCount(t *testing.T) int {
	t.Helper()
	iter, err := e.ps.PrefixScan(store.VacationSentPrefix(vacUser))
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	n := 0
	for iter.Next() {
		n++
	}
	return n
}

func TestVacationSaveStoresFieldsAndKeepsTheRest(t *testing.T) {
	e := newVacEnv(t)
	p := store.DefaultPreferences()
	p.Theme = bongov1.Theme_THEME_DARK
	p.ForwardAddress = "x@example.net"
	p.Timezone = "America/New_York"
	if err := e.prefs.Set(vacUser, p); err != nil {
		t.Fatal(err)
	}

	w := e.post(validVacationForm())
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/settings/vacation?flash=") {
		t.Fatalf("response = %d %q", w.Code, w.Header().Get("Location"))
	}
	if flash, _ := url.QueryUnescape(strings.TrimPrefix(w.Header().Get("Location"), "/settings/vacation?flash=")); flash != "Vacation reply saved" {
		t.Errorf("flash = %q, want the translated message", flash)
	}
	got := e.prefs.Get(vacUser)
	ny, _ := time.LoadLocation("America/New_York")
	wantStart := time.Date(2030, 7, 1, 0, 0, 0, 0, ny).Unix()
	wantEnd := time.Date(2030, 7, 5, 23, 59, 59, 0, ny).Unix()
	if !got.VacationEnabled || got.VacationSubject != "Away" || got.VacationBody != "Back on Monday.\nThanks." ||
		got.VacationStart != wantStart || got.VacationEnd != wantEnd || got.VacationIntervalDays != 5 {
		t.Fatalf("stored = %v", got)
	}
	if got.Theme != bongov1.Theme_THEME_DARK || got.ForwardAddress != "x@example.net" || got.Timezone != "America/New_York" {
		t.Errorf("other preferences changed: %v", got)
	}

	// The window is inclusive of both whole days, in the user's zone.
	at := func(y int, m time.Month, d, hh, mm, ss int) bool {
		return store.VacationActive(got, time.Date(y, m, d, hh, mm, ss, 0, ny))
	}
	for _, c := range []struct {
		name string
		got  bool
		want bool
	}{
		{"one second before the first day", at(2030, 6, 30, 23, 59, 59), false},
		{"midnight of the first day", at(2030, 7, 1, 0, 0, 0), true},
		{"last second of the last day", at(2030, 7, 5, 23, 59, 59), true},
		{"midnight after the last day", at(2030, 7, 6, 0, 0, 0), false},
	} {
		if c.got != c.want {
			t.Errorf("%s: active = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestVacationSaveOpenWindowAndDefaultInterval(t *testing.T) {
	e := newVacEnv(t)
	form := validVacationForm()
	form.Set("vacation_start", "")
	form.Set("vacation_end", "")
	form.Del("vacation_interval_days")
	if w := e.post(form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d: %s", w.Code, w.Body)
	}
	got := e.prefs.Get(vacUser)
	if got.VacationStart != 0 || got.VacationEnd != 0 || store.VacationIntervalDays(got) != 7 {
		t.Fatalf("stored = %v", got)
	}
}

func TestVacationSaveValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(v url.Values)
		want   string
	}{
		{"end before start", func(v url.Values) { v.Set("vacation_end", "2030-06-30") }, "The last day cannot be before the first day"},
		{"subject with a line break", func(v url.Values) { v.Set("vacation_subject", "Away\r\nBcc: x@y.test") }, "The subject must be at most 200 characters on a single line"},
		{"subject too long", func(v url.Values) { v.Set("vacation_subject", strings.Repeat("é", 201)) }, "The subject must be at most 200 characters on a single line"},
		{"body too long", func(v url.Values) { v.Set("vacation_body", strings.Repeat("a", 4097)) }, "The message must be valid text of at most 4096 bytes"},
		{"body invalid UTF-8", func(v url.Values) { v.Set("vacation_body", "ab\xffcd") }, "The message must be valid text of at most 4096 bytes"},
		{"body missing while enabled", func(v url.Values) { v.Set("vacation_body", "  \n") }, "Enter a message before turning replies on"},
		{"interval zero", func(v url.Values) { v.Set("vacation_interval_days", "0") }, "The interval must be between 1 and 30 days"},
		{"interval 31", func(v url.Values) { v.Set("vacation_interval_days", "31") }, "The interval must be between 1 and 30 days"},
		{"interval not a number", func(v url.Values) { v.Set("vacation_interval_days", "x") }, "The interval must be between 1 and 30 days"},
		{"bad start date", func(v url.Values) { v.Set("vacation_start", "01/07/2030") }, "Enter dates as year-month-day"},
		{"bad end date", func(v url.Values) { v.Set("vacation_end", "2030-02-30") }, "Enter dates as year-month-day"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newVacEnv(t)
			before := e.prefs.Get(vacUser)
			form := validVacationForm()
			tc.mutate(form)
			w := e.post(form)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400", w.Code)
			}
			if got := strings.TrimSpace(w.Body.String()); got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
			if after := e.prefs.Get(vacUser); after.VacationEnabled != before.VacationEnabled || after.VacationBody != "" {
				t.Errorf("a refused save changed the preferences: %v", after)
			}
		})
	}
}

func TestVacationSaveSameDayWindowIsValid(t *testing.T) {
	e := newVacEnv(t)
	form := validVacationForm()
	form.Set("vacation_end", "2030-07-01")
	if w := e.post(form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d: %s", w.Code, w.Body)
	}
}

func TestVacationSavePersistFailureFailsTheRequest(t *testing.T) {
	e := newVacEnv(t)
	e.applier.failOn = "/preferences"
	e.putTracking(t, "bob@example.net")
	form := validVacationForm()
	form.Set("vacation_enabled", "") // switching off must not clear tracking if the save failed
	form.Set("vacation_body", "")
	w := e.post(form)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", w.Code)
	}
	if strings.Contains(w.Header().Get("Location"), "flash") {
		t.Error("a failed save redirected with a success flash")
	}
	if n := e.trackingCount(t); n != 1 {
		t.Errorf("tracking records = %d, want 1 (untouched)", n)
	}
}

func TestVacationDisableDeletesTracking(t *testing.T) {
	e := newVacEnv(t)
	e.putTracking(t, "a@example.net", "b@example.net")
	// Another user's records are left alone.
	if err := e.applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT,
		Key: store.VacationSentKey("other-user", "a@example.net"), Value: store.EncodeVacationSent(time.Now())}, time.Second); err != nil {
		t.Fatal(err)
	}

	// Saving with vacation on keeps them.
	if w := e.post(validVacationForm()); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}
	if n := e.trackingCount(t); n != 2 {
		t.Fatalf("tracking after an enabled save = %d, want 2", n)
	}

	form := validVacationForm()
	form.Del("vacation_enabled")
	if w := e.post(form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}
	if e.prefs.Get(vacUser).VacationEnabled {
		t.Error("vacation still enabled")
	}
	if n := e.trackingCount(t); n != 0 {
		t.Errorf("tracking after disabling = %d, want 0", n)
	}
	if _, err := e.ps.Get(store.VacationSentKey("other-user", "a@example.net")); err != nil {
		t.Errorf("another user's record was deleted: %v", err)
	}
}

// Deleting the records is best-effort: the setting did change, so the request
// succeeds, and the failure is logged with the user for the operator.
func TestVacationDisableDeleteFailureIsLoggedNotFatal(t *testing.T) {
	e := newVacEnv(t)
	e.putTracking(t, "a@example.net")
	e.applier.failOn = "/vacation/sent/"
	form := validVacationForm()
	form.Del("vacation_enabled")
	w := e.post(form)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", w.Code)
	}
	if e.applier.failed == 0 {
		t.Fatal("delete was not attempted")
	}
	if !strings.Contains(e.logs.String(), "deleting tracking record failed") || !strings.Contains(e.logs.String(), vacUser) {
		t.Errorf("failure not logged with context:\n%s", e.logs)
	}
}

// Saving the general settings form starts from the stored record and so keeps
// the vacation fields.
func TestGeneralSettingsSaveKeepsVacationFields(t *testing.T) {
	e := newVacEnv(t)
	if w := e.post(validVacationForm()); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}
	want := e.prefs.Get(vacUser)

	h := NewPreferencesHandler(e.prefs)
	form := url.Values{"theme": {"dark"}, "layout_mode": {"list"}, "density": {"compact"}, "messages_per_page": {"50"},
		"default_reply": {"all"}, "timezone": {"UTC"}, "locale": {"en"}}
	r := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(middleware.WithUserContext(r.Context(), vacUser, "vac@example.com"))
	w := httptest.NewRecorder()
	h.SaveSettings(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d: %s", w.Code, w.Body)
	}
	got := e.prefs.Get(vacUser)
	if got.Theme != bongov1.Theme_THEME_DARK {
		t.Fatal("general save did not apply")
	}
	if got.VacationEnabled != want.VacationEnabled || got.VacationSubject != want.VacationSubject ||
		got.VacationBody != want.VacationBody || got.VacationStart != want.VacationStart ||
		got.VacationEnd != want.VacationEnd || got.VacationIntervalDays != want.VacationIntervalDays {
		t.Errorf("vacation fields changed by the general save: %v, want %v", got, want)
	}
}

func TestVacationShowRendersStoredValues(t *testing.T) {
	e := newVacEnv(t)
	if w := e.post(validVacationForm()); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/settings/vacation", nil)
	r = r.WithContext(middleware.WithUserContext(r.Context(), vacUser, "vac@example.com"))
	w := httptest.NewRecorder()
	e.h.Show(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`action="/settings/vacation"`, `value="2030-07-01"`, `value="2030-07-05"`, `value="5"`, "Back on Monday.", `value="Away"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(body, "checked") {
		t.Error("enabled toggle not checked")
	}
}

// The route sits behind the CSRF middleware like every other settings POST.
func TestVacationSaveRequiresCSRFToken(t *testing.T) {
	e := newVacEnv(t)
	secret := []byte("vacation-csrf-test-secret-0123456789")
	var token string
	probe := middleware.InjectCSRF(secret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		token = middleware.CSRFTokenFromContext(r.Context())
	}))
	pr := httptest.NewRequest(http.MethodGet, "/", nil)
	probe.ServeHTTP(httptest.NewRecorder(), pr.WithContext(middleware.WithUserContext(pr.Context(), vacUser, "vac@example.com")))
	if token == "" {
		t.Fatal("no CSRF token")
	}
	guarded := middleware.RequireCSRF(secret, true)(http.HandlerFunc(e.h.Save))
	do := func(tok string) int {
		r := httptest.NewRequest(http.MethodPost, "/settings/vacation", strings.NewReader(validVacationForm().Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tok != "" {
			r.Header.Set("X-CSRF-Token", tok)
		}
		r = r.WithContext(middleware.WithUserContext(r.Context(), vacUser, "vac@example.com"))
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, r)
		return w.Code
	}
	if code := do(""); code != http.StatusForbidden {
		t.Fatalf("without a token = %d, want 403", code)
	}
	if e.prefs.Get(vacUser).VacationEnabled {
		t.Fatal("a request without a token changed the preferences")
	}
	if code := do(token); code != http.StatusSeeOther {
		t.Fatalf("with a token = %d, want 303", code)
	}
}
