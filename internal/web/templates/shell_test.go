package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

func TestAvatarInitials(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a.b@x.com", "AB"},
		{"jane.roe@bongo.dev", "JR"},
		{"alice@localhost", "A"},
		{"x", "X"},
		{"", "?"},
		{"@x.com", "?"},
		{"émile@x.com", "É"},
		{"a_b-c@x.com", "AB"},
	}
	for _, tt := range tests {
		if got := accountInitials(tt.in); got != tt.want {
			t.Errorf("accountInitials(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func renderLayoutShell(t *testing.T, email string) string {
	t.Helper()
	ctx, _ := csrfRenderCtx(t)
	ctx = middleware.WithSourceURL(ctx, "https://example.org/src")
	var buf bytes.Buffer
	if err := LayoutWithContent("t", email).Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestTopbarShell(t *testing.T) {
	for _, email := range []string{"a@b.c", ""} {
		out := renderLayoutShell(t, email)
		for _, want := range []string{
			`id="global-search"`, `name="q"`, `hx-get="/search"`,
			`aria-haspopup="true"`, `x-ref="button"`, `x-data="accountMenu"`,
			`method="post" action="/logout"`, `https://example.org/src`,
			`brand-glyph`, `<kbd`, `x-text="searchHint"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("email=%q: missing %s", email, want)
			}
		}
		if strings.Contains(out, `class="topbar-link hide-mobile"`) {
			t.Errorf("email=%q: logout still hide-mobile", email)
		}
		// Logout form and source link must sit inside the account menu.
		menu := out[strings.Index(out, `x-data="accountMenu"`):]
		menu = menu[:strings.Index(menu, "</header>")]
		for _, want := range []string{`action="/logout"`, `https://example.org/src`, `href="/settings"`} {
			if !strings.Contains(menu, want) {
				t.Errorf("email=%q: account menu missing %s", email, want)
			}
		}
	}
}

func renderSidebar(t *testing.T, admin bool) string {
	t.Helper()
	ctx, _ := csrfRenderCtx(t)
	ctx = middleware.WithAdminContext(ctx, admin)
	mbs := []MailboxInfo{
		{Name: "INBOX", Label: "INBOX", Unread: 3, Icon: "inbox"},
		{Name: "Sent", Label: "Sent", Unread: 2, Icon: "send"},
		{Name: "Projects", Label: "Projects", Icon: "folder"},
	}
	tags := []SidebarTagInfo{{Name: "work", ColourIndex: 1, Count: 4}}
	var buf bytes.Buffer
	if err := Sidebar(mbs, tags, "INBOX").Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestSidebarShell(t *testing.T) {
	for _, admin := range []bool{true, false} {
		out := renderSidebar(t, admin)
		// Group order.
		last := -1
		for _, g := range []string{`data-group="mail"`, `data-group="folders"`, `data-group="tags"`, `data-group="workspace"`} {
			i := strings.Index(out, g)
			if i < 0 || i < last {
				t.Fatalf("admin=%v: group %s missing or out of order", admin, g)
			}
			last = i
		}
		if n := strings.Count(out, `id="mailbox-drop-targets"`); n != 1 {
			t.Errorf("drop targets id count = %d", n)
		}
		if n := strings.Count(out, `id="unread-badge"`); n != 1 {
			t.Errorf("unread-badge id count = %d", n)
		}
		if n := strings.Count(out, `data-folder=`); n != 3 {
			t.Errorf("data-folder count = %d, want 3", n)
		}
		if strings.Contains(out, "&#128") || strings.Contains(out, "&amp;#") {
			t.Errorf("entity icons remain")
		}
		if !strings.Contains(out, "<svg") {
			t.Errorf("no svg icons")
		}
		if n := strings.Count(out, `class="unread"`); n != 1 {
			t.Errorf("unread pill count = %d, want 1 (INBOX only)", n)
		}
		// Mail/Folders split: system mailboxes in Mail, custom ones in Folders.
		mailAt := strings.Index(out, `data-group="mail"`)
		foldersAt := strings.Index(out, `data-group="folders"`)
		for _, name := range []string{"INBOX", "Sent"} {
			i := strings.Index(out, `data-folder="`+name+`"`)
			if i < mailAt || i > foldersAt {
				t.Errorf("admin=%v: %s not inside the Mail group", admin, name)
			}
		}
		if i := strings.Index(out, `data-folder="Projects"`); i < foldersAt {
			t.Errorf("admin=%v: Projects not inside the Folders group", admin)
		}
		// The unread pill belongs to INBOX only; other counts are muted.
		anchor := func(name string) string {
			i := strings.Index(out, `data-folder="`+name+`"`)
			if i < 0 {
				t.Fatalf("no link for %s", name)
			}
			rest := out[i:]
			return rest[:strings.Index(rest, "</a>")]
		}
		if !strings.Contains(anchor("INBOX"), `class="unread"`) {
			t.Errorf("admin=%v: INBOX lacks the unread pill", admin)
		}
		sent := anchor("Sent")
		if strings.Contains(sent, `class="unread"`) || !strings.Contains(sent, `class="count"`) {
			t.Errorf("admin=%v: Sent should use the muted count only: %s", admin, sent)
		}
		if got := strings.Contains(out, `href="/admin"`); got != admin {
			t.Errorf("admin link = %v, want %v", got, admin)
		}
		// Every nav link that navigates via htmx targets #main-content.
		for _, a := range regexp.MustCompile(`<a [^>]*hx-get=[^>]*>`).FindAllString(out, -1) {
			if !strings.Contains(a, `hx-target="#main-content"`) {
				t.Errorf("link without main-content target: %s", a)
			}
		}
	}
}

func TestLayoutsHaveVariantAttrsAndSearch(t *testing.T) {
	var buf bytes.Buffer
	if err := Layout("t", "a@b.c").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `id="global-search"`) {
		t.Error("Layout missing global-search")
	}
}

func TestIsSystemMailbox(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"INBOX", true}, {"Sent", true}, {"Drafts", true},
		{"Trash", true}, {"Junk", true}, {"Archive", true},
		{"Projects", false}, {"inbox", false}, {"Archives", false}, {"", false},
	} {
		if got := isSystemMailbox(tc.name); got != tc.want {
			t.Errorf("isSystemMailbox(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSidebarWithoutCustomFolders(t *testing.T) {
	ctx, _ := csrfRenderCtx(t)
	var buf bytes.Buffer
	mbs := []MailboxInfo{{Name: "INBOX", Label: "INBOX", Icon: "inbox"}}
	if err := Sidebar(mbs, nil, "INBOX").Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `data-group="folders"`) {
		t.Error("Folders group rendered with no custom folders")
	}
}

func TestAccountMenuLabelIncludesEmail(t *testing.T) {
	out := renderLayoutShell(t, "jane@x.com")
	if !regexp.MustCompile(`aria-label="[^"]*jane@x\.com[^"]*"`).MatchString(out) {
		t.Error("account chip aria-label lacks the email")
	}
	ctx, _ := csrfRenderCtx(t)
	if got := accountMenuLabel(ctx, ""); got != t_(ctx) {
		t.Errorf("empty email label = %q, want bare label", got)
	}
}

func t_(ctx context.Context) string { return t(ctx, "nav.accountMenu") }
