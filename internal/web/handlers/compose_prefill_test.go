package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewMessagePrefillsToFromQuery(t *testing.T) {
	long := strings.Repeat("a", 330) + "@x.example"
	tests := []struct {
		name string
		to   string
		want string // expected value="..." of input#to; empty means no prefill
	}{
		{"valid address", "ada@example.com", "ada@example.com"},
		{"plus address", "a+b@x.example", "a+b@x.example"},
		{"garbage", "not an address", ""},
		{"crlf injection", "a@x.example\r\nBcc: evil@x.example", ""},
		{"lf only", "a@x.example\nBcc: evil@x.example", ""},
		{"address list", "a@x.example, b@x.example", ""},
		{"display name form", "Ada <ada@example.com>", ""},
		{"angle brackets only", "<a@x.example>", ""},
		{"leading space", " a@x.example", ""},
		{"quoted local part", "\"a\"@x.example", ""},
		{"too long", long, ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		for _, hx := range []bool{true, false} {
			name := tt.name + "/full"
			if hx {
				name = tt.name + "/partial"
			}
			t.Run(name, func(t *testing.T) {
				env, uid, email := newPipelineEnv(t)
				h := env.handler
				req := httptest.NewRequest(http.MethodGet, "/compose?to="+url.QueryEscape(tt.to), nil)
				if hx {
					req.Header.Set("HX-Request", "true")
				}
				req = withUserContext(req, uid, email)
				rec := httptest.NewRecorder()
				h.NewMessage(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d", rec.Code)
				}
				body := rec.Body.String()
				i := strings.Index(body, `id="to"`)
				if i < 0 {
					t.Fatal("no input#to in output")
				}
				end := strings.Index(body[i:], ">")
				tag := body[i : i+end]
				if tt.want == "" {
					if strings.Contains(tag, `value="`) && !strings.Contains(tag, `value=""`) {
						t.Errorf("unexpected prefill in %q", tag)
					}
					return
				}
				if !strings.Contains(tag, `value="`+tt.want+`"`) {
					t.Errorf("input#to = %q, want value %q", tag, tt.want)
				}
			})
		}
	}
}
