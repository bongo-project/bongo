package handlers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func (e *adminScopeTestEnv) postFeatures(t *testing.T, targetID string, asDomainAdmin bool, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	ctx := func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) }
	if asDomainAdmin {
		ctx = func(r *http.Request) *http.Request { return withDomainAdmin1(r, e.domainAdmin1ID) }
	}
	return postToHandler(t, e.h, e.h.UpdateUserFeatures, http.MethodPost,
		"/admin/users/"+targetID+"/features", form, map[string]string{"userID": targetID}, ctx)
}

// allFeatures posts every known feature as enabled.
func allFeatures() url.Values {
	v := url.Values{}
	for _, f := range store.KnownFeatures() {
		v.Add("feature", f)
	}
	return v
}

func without(v url.Values, features ...string) url.Values {
	out := url.Values{}
	for k, vals := range v {
		for _, x := range vals {
			if k == "feature" && slices.Contains(features, x) {
				continue
			}
			out.Add(k, x)
		}
	}
	return out
}

func TestAdminUpdateUserFeatures(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	logs := &bytes.Buffer{}
	e.h.logger = slog.New(slog.NewTextHandler(logs, nil))

	// A name this binary does not know, written by a newer node, survives.
	p := loadTestProfile(t, e.ps, e.d1UserID)
	p.DisabledFeatures = []string{"from-the-future"}
	saveTestProfile(t, e.applier, e.d1UserID, p)
	hashBefore := p.PasswordHash

	form := without(allFeatures(), store.FeatureIMAP, store.FeatureVacation)
	form.Set("messages_per_minute", "7")
	form.Set("recipients_per_hour", "200")
	form.Set("max_recipients", "50")
	w := e.postFeatures(t, e.d1UserID, false, form)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=user-features-saved") {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
	got := loadTestProfile(t, e.ps, e.d1UserID)
	want := []string{store.FeatureIMAP, store.FeatureVacation, "from-the-future"}
	if len(got.DisabledFeatures) != len(want) {
		t.Fatalf("disabled = %v, want %v", got.DisabledFeatures, want)
	}
	for _, f := range want {
		if !slices.Contains(got.DisabledFeatures, f) {
			t.Errorf("disabled = %v, missing %q", got.DisabledFeatures, f)
		}
	}
	if l := got.SendLimits; l.GetMessagesPerMinute() != 7 || l.GetRecipientsPerHour() != 200 || l.GetMaxRecipients() != 50 {
		t.Errorf("limits = %v", l)
	}
	if !bytes.Equal(got.PasswordHash, hashBefore) || got.Email != p.Email || got.IsAdmin {
		t.Error("the save changed fields outside features and limits")
	}
	out := logs.String()
	if !strings.Contains(out, "admin_id="+e.globalAdminID) || !strings.Contains(out, "user_id="+e.d1UserID) ||
		!strings.Contains(out, "messages_per_minute=7") {
		t.Errorf("log lacks admin, user or limits: %s", out)
	}

	// Everything ticked and every limit cleared removes both fields again,
	// keeping the unknown name.
	w = e.postFeatures(t, e.d1UserID, false, allFeatures())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("clear: %d", w.Code)
	}
	got = loadTestProfile(t, e.ps, e.d1UserID)
	if len(got.DisabledFeatures) != 1 || got.DisabledFeatures[0] != "from-the-future" || got.SendLimits != nil {
		t.Errorf("after clearing: disabled=%v limits=%v", got.DisabledFeatures, got.SendLimits)
	}
}

func TestAdminUpdateUserFeaturesValidation(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	tests := []struct {
		name string
		form url.Values
	}{
		{"unknown feature", url.Values{"feature": {"imap", "telepathy"}}},
		{"not a number", url.Values{"messages_per_minute": {"many"}}},
		{"negative", url.Values{"max_recipients": {"-1"}}},
		{"messages above bound", url.Values{"messages_per_minute": {"10001"}}},
		{"recipients per hour above bound", url.Values{"recipients_per_hour": {"100001"}}},
		{"max recipients above ceiling", url.Values{"max_recipients": {"1001"}}},
		{"per hour below per message", url.Values{"recipients_per_hour": {"10"}, "max_recipients": {"20"}}},
		{"number too large for 32 bits", url.Values{"messages_per_minute": {"99999999999"}}},
	}
	before := loadTestProfile(t, e.ps, e.d1UserID)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := e.postFeatures(t, e.d1UserID, false, tt.form); w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", w.Code)
			}
			got := loadTestProfile(t, e.ps, e.d1UserID)
			if len(got.DisabledFeatures) != 0 || got.SendLimits != nil || got.Email != before.Email {
				t.Fatalf("a refused request changed the profile: %v %v", got.DisabledFeatures, got.SendLimits)
			}
		})
	}
}

func TestAdminUpdateUserFeaturesScope(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	form := without(allFeatures(), store.FeatureWeb)

	// A domain admin: the same 403 for another domain, a missing user and a
	// global admin, and nothing is written.
	for name, id := range map[string]string{
		"other domain": e.d2UserID, "missing": "no-such-user", "global admin": e.globalAdminID,
	} {
		if w := e.postFeatures(t, id, true, form); w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, w.Code)
		}
	}
	for _, id := range []string{e.d2UserID, e.globalAdminID} {
		if got := loadTestProfile(t, e.ps, id); len(got.DisabledFeatures) != 0 {
			t.Errorf("out-of-scope request changed %s: %v", id, got.DisabledFeatures)
		}
	}
	// A global admin gets the informative 404 for a missing user.
	if w := e.postFeatures(t, "no-such-user", false, form); w.Code != http.StatusNotFound {
		t.Errorf("global admin, missing user: %d, want 404", w.Code)
	}
	// Control: in scope works for the domain admin.
	if w := e.postFeatures(t, e.d1UserID, true, form); w.Code != http.StatusSeeOther {
		t.Fatalf("in scope: %d", w.Code)
	}
	if got := loadTestProfile(t, e.ps, e.d1UserID); !slices.Contains(got.DisabledFeatures, store.FeatureWeb) {
		t.Errorf("in-scope save lost: %v", got.DisabledFeatures)
	}
}

func TestAdminUpdateUserFeaturesRefusesNonPerson(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	for _, kind := range []string{"resource", "shared"} {
		p := loadTestProfile(t, e.ps, e.d1UserID)
		p.Kind = kind
		p.Capacity, p.ResourceType = 8, "room"
		saveTestProfile(t, e.applier, e.d1UserID, p)
		if w := e.postFeatures(t, e.d1UserID, false, allFeatures()); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", kind, w.Code)
		}
		got := loadTestProfile(t, e.ps, e.d1UserID)
		if len(got.DisabledFeatures) != 0 || got.Capacity != 8 || got.ResourceType != "room" {
			t.Errorf("%s: profile changed: %+v", kind, got)
		}
	}
}

func TestAdminUpdateUserFeaturesLastWebAdmin(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	noWeb := without(allFeatures(), store.FeatureWeb)
	webOf := func(id string) bool {
		return slices.Contains(loadTestProfile(t, e.ps, id).DisabledFeatures, store.FeatureWeb)
	}

	// The global admin is the only administrator: refused with the flash,
	// nothing written.
	w := e.postFeatures(t, e.globalAdminID, false, noWeb)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=cannot-disable-last-admin-web") {
		t.Fatalf("status %d location %q", w.Code, w.Header().Get("Location"))
	}
	if webOf(e.globalAdminID) {
		t.Fatal("web was switched off for the last administrator")
	}
	// Other switches of the last admin are not the problem.
	if w := e.postFeatures(t, e.globalAdminID, false, without(allFeatures(), store.FeatureIMAP)); w.Code != http.StatusSeeOther ||
		strings.Contains(w.Header().Get("Location"), "cannot-disable") {
		t.Fatalf("an unrelated switch was refused: %d %q", w.Code, w.Header().Get("Location"))
	}

	// A second admin who is disabled, or who has no web access, does not count.
	second := loadTestProfile(t, e.ps, e.d1UserID)
	second.IsAdmin, second.Disabled = true, true
	saveTestProfile(t, e.applier, e.d1UserID, second)
	if w := e.postFeatures(t, e.globalAdminID, false, noWeb); !strings.Contains(w.Header().Get("Location"), "cannot-disable-last-admin-web") {
		t.Fatalf("a disabled admin counted as a replacement: %q", w.Header().Get("Location"))
	}
	second.Disabled = false
	second.DisabledFeatures = []string{store.FeatureWeb}
	saveTestProfile(t, e.applier, e.d1UserID, second)
	if w := e.postFeatures(t, e.globalAdminID, false, noWeb); !strings.Contains(w.Header().Get("Location"), "cannot-disable-last-admin-web") {
		t.Fatalf("an admin without web counted as a replacement: %q", w.Header().Get("Location"))
	}

	// With a second admin who can sign in the switch is allowed.
	second.DisabledFeatures = nil
	saveTestProfile(t, e.applier, e.d1UserID, second)
	if w := e.postFeatures(t, e.globalAdminID, false, noWeb); !strings.Contains(w.Header().Get("Location"), "flash=user-features-saved") {
		t.Fatalf("refused with a second admin: %q", w.Header().Get("Location"))
	}
	if !webOf(e.globalAdminID) {
		t.Fatal("web not switched off")
	}
	// ...and now the second admin is the last one.
	if w := e.postFeatures(t, e.d1UserID, false, noWeb); !strings.Contains(w.Header().Get("Location"), "cannot-disable-last-admin-web") {
		t.Fatalf("the new last admin lost web: %q", w.Header().Get("Location"))
	}
}

type featuresFailingApplier struct{ err error }

func (f featuresFailingApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error { return f.err }

func TestAdminUpdateUserFeaturesWriteFailure(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	e.h.applier = featuresFailingApplier{errors.New("raft unavailable")}
	if w := e.postFeatures(t, e.d1UserID, false, without(allFeatures(), store.FeatureIMAP)); w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	if got := loadTestProfile(t, e.ps, e.d1UserID); len(got.DisabledFeatures) != 0 {
		t.Fatalf("a failed write is visible: %v", got.DisabledFeatures)
	}
}

// The edit form never touches the new fields, and its page shows the card only
// for a person.
func TestAdminUserFormFeaturesCard(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	p := loadTestProfile(t, e.ps, e.d1UserID)
	p.DisabledFeatures = []string{store.FeatureRules}
	p.SendLimits = &bongov1.SendLimits{MessagesPerMinute: 9}
	saveTestProfile(t, e.applier, e.d1UserID, p)

	// UpdateUser (display name, quota) leaves features and limits alone.
	w := postToHandler(t, e.h, e.h.UpdateUser, http.MethodPost, "/admin/users/"+e.d1UserID,
		url.Values{"display_name": {"Renamed"}, "quota_bytes": {"1000"}}, map[string]string{"userID": e.d1UserID},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) })
	if w.Code != http.StatusSeeOther {
		t.Fatalf("UpdateUser: %d", w.Code)
	}
	got := loadTestProfile(t, e.ps, e.d1UserID)
	if len(got.DisabledFeatures) != 1 || got.SendLimits.GetMessagesPerMinute() != 9 {
		t.Fatalf("UpdateUser cleared features or limits: %v %v", got.DisabledFeatures, got.SendLimits)
	}

	show := func(id string) string {
		w := postToHandler(t, e.h, e.h.ShowUser, http.MethodGet, "/admin/users/"+id, nil,
			map[string]string{"userID": id}, func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) })
		if w.Code != http.StatusOK {
			t.Fatalf("ShowUser %s: %d", id, w.Code)
		}
		return w.Body.String()
	}
	page := show(e.d1UserID)
	for _, want := range []string{
		`action="/admin/users/` + e.d1UserID + `/features"`,
		`name="feature" value="imap" checked`,
		`name="feature" value="rules"`,
		`name="messages_per_minute"`, `value="9"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `name="feature" value="rules" checked`) {
		t.Error("rules, which is switched off, renders as ticked")
	}

	r := loadTestProfile(t, e.ps, e.d1UserID)
	r.Kind, r.ResourceType = "resource", "room"
	saveTestProfile(t, e.applier, e.d1UserID, r)
	if strings.Contains(show(e.d1UserID), "/features") {
		t.Error("a resource page offers the features card")
	}
}

func limitForm(perMinute, perHour, maxRecipients string) url.Values {
	v := allFeatures()
	v.Set("messages_per_minute", perMinute)
	v.Set("recipients_per_hour", perHour)
	v.Set("max_recipients", maxRecipients)
	return v
}

func TestAdminUpdateUserFeaturesDomainAdminCannotEditSelf(t *testing.T) {
	e := newAdminScopeTestEnv(t)

	// A global admin restricts the domain admin.
	restricted := without(limitForm("1", "1", "1"), store.FeatureSubmission, store.FeatureIMAP)
	if w := e.postFeatures(t, e.domainAdmin1ID, false, restricted); w.Code != http.StatusSeeOther {
		t.Fatalf("global admin restricting: %d", w.Code)
	}

	// The domain admin tries to lift it on their own record: refused, and
	// nothing changes.
	w := e.postFeatures(t, e.domainAdmin1ID, true, limitForm("10000", "100000", "1000"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("self edit status %d, want 403", w.Code)
	}
	got := loadTestProfile(t, e.ps, e.domainAdmin1ID)
	if !slices.Contains(got.DisabledFeatures, store.FeatureSubmission) || !slices.Contains(got.DisabledFeatures, store.FeatureIMAP) {
		t.Errorf("switches were re-enabled: %v", got.DisabledFeatures)
	}
	if got.SendLimits.GetMessagesPerMinute() != 1 || got.SendLimits.GetMaxRecipients() != 1 {
		t.Errorf("limits were raised: %v", got.SendLimits)
	}
}

func TestAdminUpdateUserFeaturesDomainAdminLimitsBoundedByGlobal(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	e.h.SetGlobalSendLimits(10, 100, 50)

	for name, form := range map[string]url.Values{
		"per minute above": limitForm("11", "0", "0"),
		"per hour above":   limitForm("0", "101", "0"),
		"max above":        limitForm("0", "0", "51"),
	} {
		if w := e.postFeatures(t, e.d1UserID, true, form); w.Code != http.StatusBadRequest {
			t.Errorf("%s: domain admin got %d, want 400", name, w.Code)
		}
	}
	if got := loadTestProfile(t, e.ps, e.d1UserID); got.SendLimits != nil {
		t.Fatalf("a refused request stored limits: %v", got.SendLimits)
	}

	// At or below the global value, and 0 (use global), are allowed.
	if w := e.postFeatures(t, e.d1UserID, true, limitForm("10", "100", "0")); w.Code != http.StatusSeeOther {
		t.Fatalf("at global: %d", w.Code)
	}
	// A global admin may go above the global value, within the ceilings.
	if w := e.postFeatures(t, e.d1UserID, false, limitForm("11", "101", "51")); w.Code != http.StatusSeeOther {
		t.Fatalf("global admin above global: %d", w.Code)
	}
	if got := loadTestProfile(t, e.ps, e.d1UserID); got.SendLimits.GetMessagesPerMinute() != 11 {
		t.Fatalf("global admin limits not stored: %v", got.SendLimits)
	}
}

func TestAdminUpdateUserFeaturesDomainAdminLimitsFailClosedWithoutGlobals(t *testing.T) {
	e := newAdminScopeTestEnv(t)
	if w := e.postFeatures(t, e.d1UserID, true, limitForm("5", "0", "0")); w.Code != http.StatusBadRequest {
		t.Fatalf("unwired globals: %d, want 400", w.Code)
	}
	if w := e.postFeatures(t, e.d1UserID, true, limitForm("0", "0", "0")); w.Code != http.StatusSeeOther {
		t.Fatalf("no limits set: %d", w.Code)
	}
}

func (e *adminScopeTestEnv) deleteAs(t *testing.T, targetID, actorID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/delete", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", targetID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = withGlobalAdmin(r, actorID)
	w := httptest.NewRecorder()
	e.h.DeleteUser(w, r)
	return w
}

func TestLastLoginAdminSurvivesOrdering(t *testing.T) {
	noWeb := without(allFeatures(), store.FeatureWeb)

	t.Run("delete", func(t *testing.T) {
		e := newAdminScopeTestEnv(t)
		b := loadTestProfile(t, e.ps, e.d1UserID)
		b.IsAdmin = true
		saveTestProfile(t, e.applier, e.d1UserID, b)

		// A loses web while B can still sign in: allowed.
		if w := e.postFeatures(t, e.globalAdminID, false, noWeb); !strings.Contains(w.Header().Get("Location"), "user-features-saved") {
			t.Fatalf("switching off web for A: %q", w.Header().Get("Location"))
		}
		// Deleting B would leave nobody who can sign in: refused.
		w := e.deleteAs(t, e.d1UserID, e.globalAdminID)
		if !strings.Contains(w.Header().Get("HX-Redirect"), "cannot-delete-last-admin") {
			t.Fatalf("delete allowed: %d %q", w.Code, w.Header().Get("HX-Redirect"))
		}
		if _, err := e.ps.Get([]byte("/user/" + e.d1UserID + "/profile")); err != nil {
			t.Fatalf("profile removed: %v", err)
		}
	})

	t.Run("disable", func(t *testing.T) {
		e := newAdminScopeTestEnv(t)
		b := loadTestProfile(t, e.ps, e.d1UserID)
		b.IsAdmin = true
		saveTestProfile(t, e.applier, e.d1UserID, b)
		if w := e.postFeatures(t, e.globalAdminID, false, noWeb); w.Code != http.StatusSeeOther {
			t.Fatalf("switching off web for A: %d", w.Code)
		}
		w := postToHandler(t, e.h, e.h.ToggleDisabled, http.MethodPost, "/admin/users/"+e.d1UserID+"/toggle", url.Values{},
			map[string]string{"userID": e.d1UserID}, func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) })
		if !strings.Contains(w.Header().Get("Location"), "cannot-disable-last-admin-web") {
			t.Fatalf("disable allowed: %q", w.Header().Get("Location"))
		}
		if loadTestProfile(t, e.ps, e.d1UserID).Disabled {
			t.Fatal("last login admin was disabled")
		}
	})

	t.Run("demote", func(t *testing.T) {
		e := newAdminScopeTestEnv(t)
		b := loadTestProfile(t, e.ps, e.d1UserID)
		b.IsAdmin = true
		saveTestProfile(t, e.applier, e.d1UserID, b)
		if w := e.postFeatures(t, e.globalAdminID, false, noWeb); w.Code != http.StatusSeeOther {
			t.Fatalf("switching off web for A: %d", w.Code)
		}
		w := postToHandler(t, e.h, e.h.UpdateUser, http.MethodPost, "/admin/users/"+e.d1UserID, url.Values{"display_name": {"B"}},
			map[string]string{"userID": e.d1UserID}, func(r *http.Request) *http.Request { return withGlobalAdmin(r, e.globalAdminID) })
		if !strings.Contains(w.Header().Get("Location"), "cannot-disable-last-admin-web") {
			t.Fatalf("demote allowed: %q", w.Header().Get("Location"))
		}
		if !loadTestProfile(t, e.ps, e.d1UserID).IsAdmin {
			t.Fatal("last login admin was demoted")
		}
	})
}

func TestLastLoginAdminConcurrentSwitchOff(t *testing.T) {
	noWeb := without(allFeatures(), store.FeatureWeb)
	e := newAdminScopeTestEnv(t)
	b := loadTestProfile(t, e.ps, e.d1UserID)
	b.IsAdmin = true
	saveTestProfile(t, e.applier, e.d1UserID, b)

	// Hold each request after it has counted the other administrators until
	// the other request has counted too, or a short wait has passed. With the
	// lock the second request cannot count while the first is held, so the
	// first proceeds after the wait and the second then sees no other admin.
	// Without the lock both count one other admin and both switch web off.
	var arrived sync.WaitGroup
	arrived.Add(2)
	bothCounted := make(chan struct{})
	go func() { arrived.Wait(); close(bothCounted) }()
	lastAdminCountedHook = func() {
		arrived.Done()
		select {
		case <-bothCounted:
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Cleanup(func() { lastAdminCountedHook = nil })

	var wg sync.WaitGroup
	for _, id := range []string{e.globalAdminID, e.d1UserID} {
		wg.Go(func() { e.postFeatures(t, id, false, noWeb) })
	}
	wg.Wait()

	n := 0
	for _, id := range []string{e.globalAdminID, e.d1UserID} {
		if !slices.Contains(loadTestProfile(t, e.ps, id).DisabledFeatures, store.FeatureWeb) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d administrators can sign in to the web interface, want 1", n)
	}
}
