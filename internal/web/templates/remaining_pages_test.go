package templates

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func mustNotContain(t *testing.T, out string, nots ...string) {
	t.Helper()
	for _, n := range nots {
		if strings.Contains(out, n) {
			t.Errorf("unexpected %q", n)
		}
	}
}

func TestSearchResultsUseMailboxRowGrid(t *testing.T) {
	results := []SearchResult{{UID: 7, Mailbox: "Work/Q1", From: "ann@example.com", Subject: "Budget", Date: "3 Mar", Snippet: "the <mark>budget</mark>"}}
	out := renderToString(t, SearchPage("budget", results, 2, 3))
	mustContain(t, out,
		`class="page-head search-head"`, `<h1>`+translated("search.results")+`</h1>`,
		`msg-row msg-row-grid no-select`, `class="results-list" id="search-results"`,
		`href="/mail/Work%2FQ1/7"`, `hx-get="/mail/Work%2FQ1/7"`, `tabindex="0"`,
		`class="msg-from result-from"`, `class="msg-date"`, `class="msg-preview result-snippet"`, `Work/Q1`, `budget`)
	start := strings.Index(out, `<a class="search-result`)
	if start < 0 {
		t.Fatal("no search row anchor")
	}
	anchor := out[start : start+strings.Index(out[start:], ">")]
	for _, want := range []string{`href="/mail/Work%2FQ1/7"`, `hx-get="/mail/Work%2FQ1/7"`, `hx-target="#main-content"`, `hx-push-url="true"`, `tabindex="0"`} {
		if !strings.Contains(anchor, want) {
			t.Errorf("search row anchor lost %s: %s", want, anchor)
		}
	}
	// bongo.js scopes keyboard navigation, swipe and bulk selection to
	// .msg-list; search rows have no checkbox, so they must stay outside it.
	mustNotContain(t, out, `msg-list`, `results-header`, `type="checkbox"`)
}

const sidebarOpenAside = `<aside class="sidebar" id="sidebar" x-bind:class="{'open': sidebarOpen}">`

func TestSearchNoSnippetOmitsPreview(t *testing.T) {
	out := renderToString(t, SearchPage("x", []SearchResult{{UID: 1, Mailbox: "INBOX", Subject: "s"}}, 1, 1))
	mustNotContain(t, out, `msg-preview`)
}

func TestImportPagesAdoptShellAndKeepForm(t *testing.T) {
	out := renderToString(t, ImportPage([]string{"INBOX", "Work"}, "a@example.com"))
	mustContain(t, out,
		sidebarOpenAside, `class="mbox-import-form"`,
		`<main class="content" id="main-content">`, `class="page-head"`, `class="card"`,
		`method="post" action="/import" enctype="multipart/form-data"`, `name="_csrf"`,
		`name="mbox"`, `name="mailbox"`, `name="new_mailbox"`, `<option value="Work">`,
		translated("import.importMessages"), translated("import.mboxFile"), `a@example.com`)
	mustNotContain(t, out, `role="button"`, `u-ml-05`)

	res := renderToString(t, ImportResult(3, 1, "Work", "a@example.com"))
	mustContain(t, res, `<main class="content" id="main-content">`, `class="page-head"`, `class="card"`,
		`href="/mail/Work"`, `href="/import"`, `class="btn-primary"`, translated("import.messagesFailed"), `a@example.com`)
	mustNotContain(t, res, `role="button"`)
	if !strings.Contains(renderToString(t, ImportResult(3, 0, "Work", "a@example.com")), "3 ") ||
		strings.Contains(renderToString(t, ImportResult(3, 0, "Work", "a@example.com")), translated("import.messagesFailed")) {
		t.Error("failed count must only render when failed > 0")
	}
}

func TestQuotaRefusedPageCard(t *testing.T) {
	out := renderToString(t, QuotaRefusedPage("Over quota", "a@example.com"))
	mustContain(t, out, `class="page-head"`, `class="card"`, `Over quota`, `href="/mail/INBOX"`, translated("quota.title"))
}

func TestAddressVerifyPageCardAndHooks(t *testing.T) {
	addrs := []UserAddressInfo{
		{Address: "pending@example.com", PendingVerification: true},
		{Address: "new@example.com"},
		{Address: "ok@example.com", CanSend: true, Verified: true},
	}
	out := renderToString(t, AddressVerifyPage(addrs, "a@example.com", "", ""))
	mustContain(t, out,
		sidebarOpenAside, `class="page-head"`, `card-body flush`, `class="data-table"`,
		`action="/settings/addresses/pending@example.com/verify-submit"`, `name="code"`,
		`action="/settings/addresses/new@example.com/verify-request"`,
		`action="/settings/addresses"`, `name="address"`, translated("settings.sendAsAddresses"))
	mustNotContain(t, out, `admin-table`, `settings-card`, `login-card`, `<html lang="en" data-theme="auto"`)

	empty := renderToString(t, AddressVerifyPage(nil, "a@example.com", "", ""))
	mustContain(t, empty, translated("settings.noSendAsAddresses"))
	mustNotContain(t, empty, `data-table`, `card-body flush`)
}

func TestSourcePageCardAndCopyHook(t *testing.T) {
	src := MessageSourceDetail{UID: 4, Mailbox: "INBOX", Subject: "Subj", Source: "Received: <x>\nBody", Truncated: true, TotalSize: 5000, ShownSize: 1000}
	out := renderToString(t, MessageSourceFullPage(src, "a@example.com"))
	mustContain(t, out, `class="page-head"`, `class="banner warn"`, `class="card source-card"`,
		sidebarOpenAside, `href="/mail/INBOX/4/export"`, `Received: &lt;x&gt;`)
	// The copy handler reads previousElementSibling: the button must follow the <pre> directly.
	if !strings.Contains(out, "</pre>\n\t\t\t\t\t\t<button") && !strings.Contains(out, "</pre><button") {
		i := strings.Index(out, "</pre>")
		if i < 0 || !strings.HasPrefix(strings.TrimSpace(out[i+len("</pre>"):]), `<button type="button" class="btn-copy" data-copy-previous="true"`) {
			t.Error("copy button must be the element right after <pre>")
		}
	}
	banner := out[strings.Index(out, `class="banner warn"`):]
	if i := strings.Index(banner, "</div>"); i < 0 || !strings.Contains(banner[:i], "<svg") {
		t.Error("truncation banner must carry the alert icon")
	}
	mustNotContain(t, renderToString(t, MessageSourceFullPage(MessageSourceDetail{Mailbox: "INBOX", Source: "x"}, "a@example.com")), `banner warn`)
}

func TestEmojiReplacementsRenderIcons(t *testing.T) {
	for name, c := range map[string]templ.Component{
		"empty state": EmptyState("t", "s"),
	} {
		out := renderToString(t, c)
		if !strings.Contains(out, `<svg`) || strings.Contains(out, "&#") {
			t.Errorf("%s: want an svg and no entity, got %s", name, out)
		}
	}
	out := renderToString(t, RichEditorField("body", "", "compose"))
	for _, msg := range scanTemplForEmoji("richeditor.html", []byte(out)) {
		t.Errorf("rendered editor contains an emoji: %s", msg)
	}
	mustNotContain(t, out, "&#")
	mustContain(t, out, translated("editor.linkShort"), translated("editor.imageShort"),
		fmt.Sprintf(`aria-label="%s"`, translated("editor.insertLink")))
}
