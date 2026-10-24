package templates

import (
	"bytes"
	"context"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

var (
	csrfTestSecret = []byte("templates-csrf-test-secret-0123456789")
	inputTagRe     = regexp.MustCompile(`^\s*<input\b[^>]*>`)
	attrRe         = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)
)

// csrfRenderCtx returns a context carrying a real CSRF token, produced by the
// same InjectCSRF middleware that production uses, plus the token itself.
func csrfRenderCtx(t *testing.T) (context.Context, string) {
	t.Helper()
	var ctx context.Context
	h := middleware.InjectCSRF(csrfTestSecret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context()
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), "u1", "u1@example.com"))
	h.ServeHTTP(httptest.NewRecorder(), req)
	token := middleware.CSRFTokenFromContext(ctx)
	if token == "" {
		t.Fatal("InjectCSRF produced no token")
	}
	return ctx, token
}

// firstFieldOfMultipartForms returns, for every multipart form in the markup,
// the attributes of the first element after the opening <form> tag. ok is
// false when that element is not an <input>.
func firstFieldOfMultipartForms(t *testing.T, markup string) []map[string]string {
	t.Helper()
	var out []map[string]string
	rest := markup
	for {
		i := strings.Index(rest, `enctype="multipart/form-data"`)
		if i < 0 {
			return out
		}
		rest = rest[i:]
		end := strings.Index(rest, ">")
		if end < 0 {
			t.Fatal("unterminated form tag")
		}
		rest = rest[end+1:]
		attrs := map[string]string{}
		if tag := inputTagRe.FindString(rest); tag != "" {
			for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
				attrs[m[1]] = html.UnescapeString(m[2])
			}
		}
		out = append(out, attrs)
	}
}

func TestMultipartFormsRenderCSRFFirst(t *testing.T) {
	ctx, token := csrfRenderCtx(t)
	tests := []struct {
		name string
		comp templ.Component
	}{
		{"compose", ComposePage("new", "", "", "", "", "", 0, nil, "", "")},
		{"import", ImportPage([]string{"INBOX"})},
		{"contacts", ContactsPage(nil, 1, 1, "u1@example.com", "")},
		{"calendar", CalendarSidebar(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tt.comp.Render(ctx, &buf); err != nil {
				t.Fatalf("render: %v", err)
			}
			forms := firstFieldOfMultipartForms(t, buf.String())
			if len(forms) == 0 {
				t.Fatal("no multipart form rendered")
			}
			for i, f := range forms {
				if f["name"] != "_csrf" || f["type"] != "hidden" {
					t.Errorf("form %d: first field is %v, want hidden _csrf", i, f)
				}
				if f["value"] != token {
					t.Errorf("form %d: _csrf value = %q, want the request token", i, f["value"])
				}
			}
		})
	}
}

// TestRenderedUploadFormPassesRequireCSRF takes the _csrf field a rendered
// form actually carries, serialises it first in a multipart body as a browser
// would, and sends it through the enforcing middleware.
func TestRenderedUploadFormPassesRequireCSRF(t *testing.T) {
	ctx, _ := csrfRenderCtx(t)
	var page bytes.Buffer
	if err := ImportPage([]string{"INBOX"}).Render(ctx, &page); err != nil {
		t.Fatal(err)
	}
	forms := firstFieldOfMultipartForms(t, page.String())
	if len(forms) != 1 || forms[0]["name"] != "_csrf" {
		t.Fatalf("unexpected form shape: %v", forms)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField(forms[0]["name"], forms[0]["value"])
	fw, _ := mw.CreateFormFile("file", "a.mbox")
	_, _ = fw.Write([]byte("From x\n\nbody\n"))
	_ = mw.Close()

	called := false
	h := middleware.RequireCSRF(csrfTestSecret, true)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	req := httptest.NewRequest("POST", "/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(middleware.WithUserContext(req.Context(), "u1", "u1@example.com"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called || rr.Code == http.StatusForbidden {
		t.Fatalf("rendered token rejected: called=%v status=%d", called, rr.Code)
	}
}

var sendIDFieldRe = regexp.MustCompile(`<input type="hidden" name="send_id" value="([^"]*)"`)

// TestComposeFormCarriesFreshSendID: the compose form carries a send ID of the
// shape handlers.Send accepts, and every render gets its own, so one rendering
// is one send. _csrf stays the first field.
func TestComposeFormCarriesFreshSendID(t *testing.T) {
	ctx, _ := csrfRenderCtx(t)
	render := func() string {
		var buf bytes.Buffer
		if err := ComposePage("new", "", "", "", "", "", 0, nil, "", "").Render(ctx, &buf); err != nil {
			t.Fatalf("render: %v", err)
		}
		m := sendIDFieldRe.FindStringSubmatch(buf.String())
		if m == nil {
			t.Fatal("compose form has no send_id field")
		}
		return m[1]
	}
	a, b := render(), render()
	for _, id := range []string{a, b} {
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
			t.Errorf("send_id %q is not 32 lowercase hex characters", id)
		}
	}
	if a == b {
		t.Errorf("two renders share send_id %q", a)
	}
}
