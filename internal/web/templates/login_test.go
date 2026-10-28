package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderLogin(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The public pages share one card and keep every form contract the handlers
// and the ui-shots sign-in depend on.
func TestPublicPagesKeepFormContract(t *testing.T) {
	tests := []struct {
		name   string
		page   templ.Component
		action string
		fields []string
		extra  []string
	}{
		{"login", LoginPage("bad credentials"), "/login",
			[]string{`name="email"`, `name="password"`, `name="remember" value="1"`, `type="email"`, `type="password"`, `type="checkbox"`,
				`autocomplete="email"`, `autocomplete="current-password"`, `id="email"`, `id="password"`},
			[]string{"bad credentials", translated("auth.welcomeBack"), translated("auth.signInSubtitle"), translated("auth.rememberMe"), translated("auth.login")}},
		{"2fa", TwoFactorPage("wrong code", "save your codes"), "/login/2fa",
			[]string{`name="code"`, `id="code"`, `maxlength="9"`, `autocomplete="one-time-code"`, `inputmode="numeric"`, `href="/login"`},
			[]string{"wrong code", "save your codes", translated("auth.verify"), translated("auth.backToLogin")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderLogin(t, tt.page)
			if !strings.Contains(out, `<form method="post" action="`+tt.action+`"`) {
				t.Errorf("form post to %s missing", tt.action)
			}
			if n := strings.Count(out, "<form"); n != 1 {
				t.Errorf("%d forms, want 1", n)
			}
			for _, f := range append(tt.fields, tt.extra...) {
				if !strings.Contains(out, f) {
					t.Errorf("missing %q", f)
				}
			}
			for _, want := range []string{`class="login-card"`, `class="brand-glyph`, `class="wordmark"`, `class="login-submit"`, `class="login-source"`} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %s", want)
				}
			}
			if strings.Contains(out, "<script") {
				t.Error("public page must not carry script")
			}
			if regexp.MustCompile(`\sstyle=`).MatchString(out) {
				t.Error("public page must not carry inline style")
			}
			if regexp.MustCompile(`\son[a-z]+=`).MatchString(out) {
				t.Error("public page must not carry inline handlers")
			}
		})
	}
}

func TestLoginPageHasNoForgotLink(t *testing.T) {
	out := renderLogin(t, LoginPage(""))
	if strings.Contains(strings.ToLower(out), "forgot") {
		t.Error("there is no self-service reset; the Forgot link must stay out")
	}
}
