package handlers

import (
	"errors"
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

type attFailingApplier struct{}

func (attFailingApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	return errors.New("raft down")
}

type attEnv struct {
	h   *AdminAttachmentsHandler
	ps  *store.PebbleStore
	pol *store.AttachmentPolicyStore
}

func newAttEnv(t *testing.T, applier store.RaftApplier) *attEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	direct := &directApplierTags{ps: ps}
	put := func(k, v string) {
		if err := direct.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(k), Value: []byte(v)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	put("/system/domain/example.com", "enabled")
	put("/system/domain/other.test", "enabled")
	put("/system/domain/alias.test", "enabled")
	put("/system/domain/alias.test/alias_of", "example.com")
	if applier == nil {
		applier = direct
	}
	pol := store.NewAttachmentPolicyStore(ps, applier)
	return &attEnv{h: NewAdminAttachmentsHandler(pol, ps, discardLogger()), ps: ps, pol: pol}
}

func (e *attEnv) do(method, domain string, scope middleware.AdminScope, form url.Values) *httptest.ResponseRecorder {
	var r *http.Request
	if method == http.MethodPost {
		r = httptest.NewRequest(method, "/admin/domains/"+domain+"/attachments", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, "/admin/domains/"+domain+"/attachments", nil)
	}
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, scope)
	r = withChiParam(r.WithContext(ctx), "domain", domain)
	w := httptest.NewRecorder()
	if method == http.MethodPost {
		e.h.Save(w, r)
	} else {
		e.h.Show(w, r)
	}
	return w
}

func validForm() url.Values {
	return url.Values{"enabled": {"1"}, "extensions": {".EXE\nscr, js"}, "mime_types": {"application/x-msdownload"}, "action": {"strip"}, "language": {"de"}}
}

func TestAttachmentPolicyScopeMatrix(t *testing.T) {
	e := newAttEnv(t, nil)
	tests := []struct {
		name   string
		domain string
		scope  middleware.AdminScope
		status int
	}{
		{"global, own domain", "example.com", globalAdminScope(), http.StatusOK},
		{"global, missing domain", "nowhere.test", globalAdminScope(), http.StatusNotFound},
		{"global, alias domain", "alias.test", globalAdminScope(), http.StatusBadRequest},
		{"domain admin, own domain", "example.com", domainAdminScope("example.com"), http.StatusOK},
		{"domain admin, other domain", "other.test", domainAdminScope("example.com"), http.StatusForbidden},
		{"domain admin, missing domain", "nowhere.test", domainAdminScope("example.com"), http.StatusForbidden},
		{"domain admin, missing but granted", "gone.test", domainAdminScope("gone.test"), http.StatusForbidden},
		{"path trick", "example.com/quota", globalAdminScope(), http.StatusForbidden},
	}
	var forbiddenBodies []string
	for _, tt := range tests {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(tt.name+" "+method, func(t *testing.T) {
				w := e.do(method, tt.domain, tt.scope, validForm())
				want := tt.status
				if method == http.MethodPost && want == http.StatusOK {
					want = http.StatusSeeOther
				}
				if w.Code != want {
					t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
				}
				if want == http.StatusForbidden {
					forbiddenBodies = append(forbiddenBodies, w.Body.String())
				}
			})
		}
	}
	for _, b := range forbiddenBodies {
		if b != forbiddenBodies[0] {
			t.Errorf("403 bodies differ: %q vs %q", b, forbiddenBodies[0])
		}
	}
	// Refused requests wrote nothing.
	for _, d := range []string{"other.test", "nowhere.test", "alias.test"} {
		if p, _ := e.pol.Get(d); p != nil {
			t.Errorf("policy written for %s", d)
		}
	}
}

func TestAttachmentPolicyDefaultsPrefilled(t *testing.T) {
	e := newAttEnv(t, nil)
	w := e.do(http.MethodGet, "example.com", globalAdminScope(), nil)
	body := w.Body.String()
	for _, want := range []string{"exe", "scr", "ps1", "application/x-msdownload", "application/java-archive", "No policy is set yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "checked") && strings.Contains(body, `name="enabled" value="1" checked`) {
		t.Error("a domain with no policy must not show the policy as enabled")
	}
	// The defaults link is offered once a policy exists.
	if err := e.pol.Put("example.com", &bongov1.AttachmentPolicy{Enabled: true, Action: "quarantine", BlockedExtensions: []string{"zzz"}}, "x"); err != nil {
		t.Fatal(err)
	}
	body = e.do(http.MethodGet, "example.com", globalAdminScope(), nil).Body.String()
	if !strings.Contains(body, "zzz") || strings.Contains(body, "ps1") || !strings.Contains(body, "defaults=1") {
		t.Errorf("stored policy not shown: %s", body)
	}
	body = httptest.NewRecorder().Body.String()
	r := httptest.NewRequest(http.MethodGet, "/admin/domains/example.com/attachments?defaults=1", nil)
	ctx := middleware.WithAdminScopeContext(middleware.WithUserContext(r.Context(), "a", "a@b.test"), globalAdminScope())
	rec := httptest.NewRecorder()
	e.h.Show(rec, withChiParam(r.WithContext(ctx), "domain", "example.com"))
	if !strings.Contains(rec.Body.String(), "ps1") {
		t.Error("?defaults=1 should refill the suggested lists")
	}
	_ = body
}

func TestAttachmentPolicySaveStoresNormalised(t *testing.T) {
	e := newAttEnv(t, nil)
	w := e.do(http.MethodPost, "example.com", domainAdminScope("example.com"), validForm())
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "/admin/domains/example.com/attachments") {
		t.Fatalf("status %d loc %q", w.Code, w.Header().Get("Location"))
	}
	p, err := e.pol.Get("example.com")
	if err != nil || p == nil {
		t.Fatalf("Get: %v %v", p, err)
	}
	if strings.Join(p.BlockedExtensions, ",") != "exe,js,scr" || p.Action != "strip" || p.Language != "de" || !p.Enabled || p.UpdatedBy != "admin-id" {
		t.Errorf("stored %+v", p)
	}
	// Unchecked box disables.
	form := validForm()
	form.Del("enabled")
	e.do(http.MethodPost, "example.com", globalAdminScope(), form)
	if p, _ := e.pol.Get("example.com"); p == nil || p.Enabled {
		t.Errorf("policy should be disabled: %+v", p)
	}
}

func TestAttachmentPolicyFieldErrors(t *testing.T) {
	e := newAttEnv(t, nil)
	tests := []struct {
		name  string
		field string
		set   func(url.Values)
		msg   string
	}{
		{"extension", "extensions", func(v url.Values) { v.Set("extensions", "ex/e") }, "Each extension must be 1 to 16"},
		{"mime", "mime_types", func(v url.Values) { v.Set("mime_types", "nonsense") }, "Each MIME type must look like"},
		{"action", "action", func(v url.Values) { v.Set("action", "delete") }, "Choose either removing"},
		{"language", "language", func(v url.Values) { v.Set("language", "xx") }, "That language is not available"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validForm()
			tt.set(f)
			w := e.do(http.MethodPost, "example.com", globalAdminScope(), f)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d", w.Code)
			}
			if !strings.Contains(w.Body.String(), tt.msg) {
				t.Errorf("body lacks %q", tt.msg)
			}
			if p, _ := e.pol.Get("example.com"); p != nil {
				t.Errorf("an invalid form wrote %+v", p)
			}
		})
	}
}

func TestAttachmentPolicyFailedWriteFailsRequest(t *testing.T) {
	e := newAttEnv(t, attFailingApplier{})
	w := e.do(http.MethodPost, "example.com", globalAdminScope(), validForm())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
}

func TestAttachmentPolicyBodyBounded(t *testing.T) {
	e := newAttEnv(t, nil)
	f := validForm()
	f.Set("extensions", strings.Repeat("a\n", maxAttachmentFormBytes))
	w := e.do(http.MethodPost, "example.com", globalAdminScope(), f)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}
