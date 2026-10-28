package templates

import (
	"bytes"
	"context"
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
)

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	initI18nForTest()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// viewFixture is a tagged thread message with a blocked-image body and a PDF.
func viewFixture() MessageDetail {
	return MessageDetail{
		UID:              7,
		Mailbox:          "INBOX",
		Subject:          "Roadmap review",
		From:             "Marcus Webb <marcus@example.com>",
		To:               []string{"jane@bongo.dev"},
		Date:             time.Date(2026, 4, 19, 9, 30, 0, 0, time.UTC),
		HTMLBody:         `<p>MARKER-HTML-BODY</p><div class="blocked-image" data-src="https://t.example/x.png"></div>`,
		HasBlockedImages: true,
		Tags:             []TagInfo{{Name: "Work", ColourIndex: 1}},
		AllTags:          []TagInfo{{Name: "Work", ColourIndex: 1}, {Name: "Later", ColourIndex: 2}},
		Attachments: []Attachment{
			{Filename: "Q2-roadmap-v3.pdf", ContentType: "application/pdf", Size: 2936013, Hash: "abc", URL: "/mail/INBOX/7/attachment/0"},
		},
		Thread: []MessageDetail{{
			UID: 3, Mailbox: "INBOX", Subject: "Re: Roadmap review", From: "jane@bongo.dev",
			To: []string{"marcus@example.com"}, Date: time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC),
			TextBody: "earlier reply", HasBlockedImages: true,
			Tags: []TagInfo{{Name: "Later", ColourIndex: 2}},
		}},
	}
}

func allViews(t *testing.T, msg MessageDetail, boxes []MailboxInfo) map[string]string {
	t.Helper()
	return map[string]string{
		"MessagePage":          renderToString(t, MessagePage(msg, boxes)),
		"MessagePaneContent":   renderToString(t, MessagePaneContent(msg, boxes)),
		"ThreadMessageArticle": renderToString(t, ThreadMessageArticle(msg, boxes, false)),
		"messageTags":          renderToString(t, messageTags(msg)),
	}
}

var alpineAttr = regexp.MustCompile(`\s((?:x-[\w:.-]+)|(?:[:@][\w:.-]+))="([^"]*)"`)

// TestMessageViewAlpineExpressionsAreCSPSafe: the Alpine CSP build has no
// backtick handling, no arrow functions and no access to globals, so any such
// expression throws "CSP Parser Error" at runtime. No Go compile step sees it.
func TestMessageViewAlpineExpressionsAreCSPSafe(t *testing.T) {
	bad := []string{"`", "=>", "document.", "window.", "const ", "let ", "function"}
	boxes := []MailboxInfo{{Name: "INBOX"}, {Name: "Archive"}}
	for name, out := range allViews(t, viewFixture(), boxes) {
		matches := alpineAttr.FindAllStringSubmatch(out, -1)
		if len(matches) == 0 {
			t.Fatalf("%s: no Alpine attributes found; the scan is not looking at anything", name)
		}
		for _, m := range matches {
			val := html.UnescapeString(m[2])
			if m[1] == "x-data" && strings.HasPrefix(val, "messageTags(") {
				continue // JSON arguments, not an expression the parser evaluates
			}
			for _, b := range bad {
				if strings.Contains(val, b) {
					t.Errorf("%s: %s=%q contains %q, which the Alpine CSP build cannot parse", name, m[1], val, b)
				}
			}
		}
	}
}

func TestMessageTagsUseComponentMethodsAndTranslatedLabels(t *testing.T) {
	out := renderToString(t, messageTags(viewFixture()))
	for _, want := range []string{
		`:href="tagHref(tag)"`,
		`:aria-label="viewLabel(tag)"`,
		`:aria-label="removeLabel(tag)"`,
		`data-view-tag-label="`,
		`data-remove-tag-label="`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("messageTags missing %s", want)
		}
	}
	if strings.Contains(out, "View tag ${") || strings.Contains(out, "Remove tag ${") {
		t.Error("hard-coded English template literal still present")
	}
}

func TestAttachmentTilesShowBadgeNameAndSize(t *testing.T) {
	atts := []Attachment{{Filename: "Q2-roadmap-v3.pdf", ContentType: "application/pdf", Size: 2936013, Hash: "h", URL: "/a/0"}}
	out := renderToString(t, attachmentList(atts))
	for _, want := range []string{
		`<span class="att-ico" aria-hidden="true">pdf</span>`,
		`Q2-roadmap-v3.pdf`,
		`<span class="att-size">2.8 MB</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tile markup missing %q in:\n%s", want, out)
		}
	}
}

func TestAttachmentCardHeadingCountsFilesAndTotal(t *testing.T) {
	atts := []Attachment{
		{Filename: "a.zip", ContentType: "application/zip", Size: 1024, URL: "/a/0"},
		{Filename: "b.zip", ContentType: "application/zip", Size: 2048, URL: "/a/1"},
	}
	out := renderToString(t, attachmentList(atts))
	if !strings.Contains(out, "2 files") || !strings.Contains(out, "3.0 KB") {
		t.Errorf("heading should carry file count and total size:\n%s", out)
	}
	one := renderToString(t, attachmentList(atts[:1]))
	if strings.Contains(one, "1 files") {
		t.Error("singular heading must not say '1 files'")
	}
}

func TestAttachmentExt(t *testing.T) {
	tests := []struct {
		name, file, ct, want string
	}{
		{"simple", "report.pdf", "application/pdf", "pdf"},
		{"uppercase", "PHOTO.JPG", "image/jpeg", "jpg"},
		{"double extension", "backup.tar.gz", "application/gzip", "gz"},
		{"no extension falls back to subtype", "README", "text/csv", "csv"},
		{"plain text reads as txt", "README", "text/plain", "txt"},
		{"octet-stream reads as bin", "blob", "application/octet-stream", "bin"},
		{"no extension, unknown type", "README", "", "file"},
		{"very long extension is cut", "x.abcdefghij", "application/octet-stream", "abcd"},
		{"dotfile has no extension", ".bashrc", "", "file"},
		{"trailing dot", "notes.", "text/csv", "csv"},
		{"subtype with suffix", "data", "application/vnd.api+json", "json"},
		{"parameters ignored", "data", "text/csv; charset=utf-8", "csv"},
		{"extension of exactly four is kept", "x.html", "", "html"},
		{"extension of five is cut to four", "page.shtml", "", "shtm"},
		{"multi-byte extension is cut by rune", "x.日本語テスト", "", "日本語テ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := attachmentExt(tt.file, tt.ct); got != tt.want {
				t.Errorf("attachmentExt(%q, %q) = %q, want %q", tt.file, tt.ct, got, tt.want)
			}
		})
	}
}

func TestSenderParts(t *testing.T) {
	tests := []struct {
		name, in, wantName, wantAddr string
	}{
		{"name and address", "Marcus Webb <marcus@example.com>", "Marcus Webb", "marcus@example.com"},
		{"bare address", "jane@bongo.dev", "jane@bongo.dev", "jane@bongo.dev"},
		{"quoted name", `"Webb, Marcus" <marcus@example.com>`, "Webb, Marcus", "marcus@example.com"},
		{"garbage falls back to raw", "not an address <<", "not an address <<", ""},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, a := senderParts(tt.in)
			if n != tt.wantName || a != tt.wantAddr {
				t.Errorf("senderParts(%q) = (%q, %q), want (%q, %q)", tt.in, n, a, tt.wantName, tt.wantAddr)
			}
		})
	}
}

// TestMessageViewSecurityInvariants pins what the restyle must not change: the
// sanitised body stays inside div.email-body, remote images stay blocked behind
// the banner, the PDF preview stays sandboxed, text/html never gets an inline
// preview and plain text is escaped.
func TestMessageViewSecurityInvariants(t *testing.T) {
	msg := viewFixture()
	msg.Attachments = append(msg.Attachments, Attachment{Filename: "page.html", ContentType: "text/html", Size: 10, Hash: "hh", URL: "/a/1"})
	msg.Thread[0].TextBody = `<script>alert("x")</script>`
	boxes := []MailboxInfo{{Name: "INBOX"}}
	for name, out := range map[string]string{
		"MessagePage":          renderToString(t, MessagePage(msg, boxes)),
		"MessagePaneContent":   renderToString(t, MessagePaneContent(msg, boxes)),
		"ThreadMessageArticle": renderToString(t, ThreadMessageArticle(msg, boxes, false)),
	} {
		wrapped := regexp.MustCompile(`(?s)<div class="email-body">\s*<p>MARKER-HTML-BODY</p>`)
		if !wrapped.MatchString(out) {
			t.Errorf("%s: HTML body is not wrapped in div.email-body", name)
		}
		if !strings.Contains(out, `class="blocked-banner"`) {
			t.Errorf("%s: blocked-image banner missing", name)
		}
		if !strings.Contains(out, `class="blocked-image" data-src=`) {
			t.Errorf("%s: server-side blocked-image placeholder was altered", name)
		}
		if !strings.Contains(out, `sandbox=""`) || !strings.Contains(out, `class="pdf-preview"`) {
			t.Errorf("%s: PDF iframe lost its empty sandbox", name)
		}
		if !strings.Contains(out, "page.html") {
			t.Fatalf("%s: html attachment not rendered", name)
		}
		// The inline preview swaps the fetched blob in as innerHTML, so an
		// HTML attachment must never be fetched for it.
		if strings.Contains(out, "ct=text/html") || strings.Contains(out, "/mail/blob/hh") {
			t.Errorf("%s: text/html attachment got an inline preview", name)
		}
		if strings.Contains(out, "<script>alert") {
			t.Errorf("%s: plain-text body was not escaped", name)
		}
	}
	if !strings.Contains(renderToString(t, MessagePage(msg, boxes)), `&lt;script&gt;alert(`) {
		t.Error("thread text body should appear escaped")
	}
}

func TestImagesStayBehindOptInInTiles(t *testing.T) {
	atts := []Attachment{{Filename: "pic.png", ContentType: "image/png", Size: 10, URL: "/a/0"}}
	out := renderToString(t, attachmentList(atts))
	if strings.Index(out, `<template x-if="showImages">`) > strings.Index(out, `class="attachment-thumbnail"`) {
		t.Error("thumbnail escaped its x-if gate")
	}
	if !strings.Contains(out, `class="att-ico"`) {
		t.Error("hidden placeholder tile lost the extension badge")
	}
}

func TestRemoteImagesButtonUsesNamedComponent(t *testing.T) {
	out := renderToString(t, MessagePage(viewFixture(), nil))
	if got := strings.Count(out, `x-data="remoteImages"`); got != 2 {
		t.Errorf("remoteImages components = %d, want one per blocked message (2)", got)
	}
	if !strings.Contains(out, `@click="load()"`) {
		t.Error("load remote images button should call load()")
	}
}

func TestMoreMenuIsKeyboardAccessibleAndHiddenByDefault(t *testing.T) {
	out := renderToString(t, MessagePage(viewFixture(), []MailboxInfo{{Name: "INBOX"}, {Name: "Sent"}}))
	for _, want := range []string{
		`x-data="messageMore"`,
		`x-on:keydown.escape="close()"`,
		`x-on:click.outside="dismiss()"`,
		`x-ref="button"`,
		`aria-haspopup="true"`,
		`aria-expanded="false"`,
		`x-bind:aria-expanded="open"`,
		`aria-controls="msg-more-INBOX-7"`,
		`id="msg-more-INBOX-7"`,
		`hidden x-bind:hidden="!open" x-cloak=""`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("More menu missing %s", want)
		}
	}
	// The menu holds Export, View source, Print and the Move select.
	i := strings.Index(out, `id="msg-more-INBOX-7"`)
	menu := out[i:]
	menu = menu[:strings.Index(menu, "</select>")]
	for _, want := range []string{"/mail/INBOX/7/export", "/mail/INBOX/7/source", `data-print-page="true"`, `name="destination"`, `hx-trigger="change"`, `/mail/INBOX/7/move`} {
		if !strings.Contains(menu, want) {
			t.Errorf("More menu does not contain %s", want)
		}
	}
}

func TestArchiveOnlyWhenArchiveMailboxExists(t *testing.T) {
	msg := viewFixture()
	without := renderToString(t, MessagePage(msg, []MailboxInfo{{Name: "INBOX"}, {Name: "Sent"}}))
	if strings.Contains(without, `hx-vals`) {
		t.Error("Archive rendered although the user has no Archive mailbox")
	}
	with := renderToString(t, MessagePage(msg, []MailboxInfo{{Name: "INBOX"}, {Name: "Archive"}}))
	if !strings.Contains(with, `hx-vals="{&#34;destination&#34;:&#34;Archive&#34;}"`) {
		t.Errorf("Archive button missing or wrong vals:\n%s", with)
	}
	msg.Mailbox = "Archive"
	msg.Thread = nil
	inArchive := renderToString(t, MessagePage(msg, []MailboxInfo{{Name: "INBOX"}, {Name: "Archive"}}))
	if strings.Contains(inArchive, `hx-vals`) {
		t.Error("Archive offered for a message already in Archive")
	}
}

func TestReplyAllAndQuickReplyHandOffToComposer(t *testing.T) {
	out := renderToString(t, MessagePage(viewFixture(), nil))
	if !strings.Contains(out, `href="/compose/reply/INBOX/7?all=1"`) {
		t.Error("Reply all should link to the reply route with all=1")
	}
	if got := strings.Count(out, `class="quick-reply"`); got != 1 {
		t.Errorf("quick reply cards = %d, want exactly one per thread", got)
	}
	for _, want := range []string{`method="post"`, `action="/compose/reply/INBOX/7"`, `name="quick"`, `name="_csrf"`} {
		if !strings.Contains(out, want) {
			t.Errorf("quick reply form missing %s", want)
		}
	}
	if strings.Contains(out, "Press") {
		t.Error("shortcut hint for an unimplemented shortcut")
	}
}

var idAttr = regexp.MustCompile(`\sid="([^"]+)"`)

func TestThreadRendersUniqueIDs(t *testing.T) {
	msg := viewFixture()
	msg.Thread = append(msg.Thread, MessageDetail{UID: 4, Mailbox: "INBOX", From: "x@y.z", TextBody: "t",
		Attachments: []Attachment{{Filename: "a.zip", ContentType: "application/zip", URL: "/a/0"}}})
	for name, out := range map[string]string{
		"MessagePage":        renderToString(t, MessagePage(msg, nil)),
		"MessagePaneContent": renderToString(t, MessagePaneContent(msg, nil)),
	} {
		seen := map[string]bool{}
		for _, m := range idAttr.FindAllStringSubmatch(out, -1) {
			if seen[m[1]] {
				t.Errorf("%s: duplicate id %q", name, m[1])
			}
			seen[m[1]] = true
		}
		if len(seen) < 3 {
			t.Errorf("%s: expected an id per message menu, got %d ids", name, len(seen))
		}
	}
}

func TestSenderAvatarTintUsesClassesNotInlineStyle(t *testing.T) {
	for name, out := range allViews(t, viewFixture(), nil) {
		if strings.Contains(out, ` style=`) {
			t.Errorf("%s: inline style attribute breaks the style-src 'self' CSP", name)
		}
	}
	out := renderToString(t, MessagePage(viewFixture(), nil))
	if !regexp.MustCompile(`class="msg-avatar avatar-tint avatar-colour-\d"[^>]*>MW<`).MatchString(out) {
		t.Errorf("sender avatar should carry initials MW and a tint class:\n%s", out)
	}
	if !strings.Contains(out, "&lt;marcus@example.com&gt;") {
		t.Error("sender address should be shown after the name")
	}
}

func TestMessageHeaderTagRow(t *testing.T) {
	out := renderToString(t, MessagePage(viewFixture(), nil))
	if got := strings.Count(out, `class="msg-tags-row"`); got != 1 {
		t.Errorf("tag rows = %d, want 1 (header only)", got)
	}
	if !strings.Contains(out, `class="tag-add-btn"`) {
		t.Error("dashed add-tag button missing")
	}
}

func TestMoreMenuIDIsIDSafe(t *testing.T) {
	tests := []struct {
		mailbox string
		uid     uint32
		want    string
	}{
		{"INBOX", 7, "msg-more-INBOX-7"},
		{"Projects/Bongo Go", 12, "msg-more-Projects_Bongo_Go-12"},
		{"a.b&c\"d", 1, "msg-more-a_b_c_d-1"},
		{"keep-this_one", 3, "msg-more-keep-this_one-3"},
	}
	for _, tt := range tests {
		got := moreMenuID(MessageDetail{Mailbox: tt.mailbox, UID: tt.uid})
		if got != tt.want {
			t.Errorf("moreMenuID(%q, %d) = %q, want %q", tt.mailbox, tt.uid, got, tt.want)
		}
		if strings.ContainsAny(got, " /.&\"") {
			t.Errorf("id %q contains characters unsafe for aria-controls", got)
		}
	}
}

func TestSenderFirstName(t *testing.T) {
	tests := []struct{ from, want string }{
		{"Marcus Webb <marcus@example.com>", "Marcus"},
		{"\"Webb, Marcus\" <marcus@example.com>", "Webb,"},
		{"marcus@example.com", "marcus@example.com"},
		{"Cher <cher@example.com>", "Cher"},
	}
	for _, tt := range tests {
		if got := senderFirstName(tt.from); got != tt.want {
			t.Errorf("senderFirstName(%q) = %q, want %q", tt.from, got, tt.want)
		}
	}
}

func TestTruncateBadge(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"abcd", "abcd"},
		{"abcde", "abcd"},
		{"日本語テ", "日本語テ"},
		{"日本語テス", "日本語テ"},
	}
	for _, tt := range tests {
		if got := truncateBadge(tt.in); got != tt.want {
			t.Errorf("truncateBadge(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestOnlyTopMessageOffersAddToContacts: a thread of non-contact senders gets
// exactly one quick-add form, on the message the page was opened on.
func TestOnlyTopMessageOffersAddToContacts(t *testing.T) {
	msg := viewFixture()
	if len(msg.Thread) == 0 {
		t.Fatal("fixture needs a thread")
	}
	pages := func(m MessageDetail) map[string]string {
		return map[string]string{
			"MessagePage":        renderToString(t, MessagePage(m, nil)),
			"MessagePaneContent": renderToString(t, MessagePaneContent(m, nil)),
		}
	}
	for name, out := range pages(msg) {
		if got := strings.Count(out, `action="/contacts/quick-add"`); got != 1 {
			t.Errorf("%s: quick-add forms = %d, want 1 (top message only)", name, got)
		}
	}
	msg.IsContact = true
	for name, out := range pages(msg) {
		// The thread entry is still a non-contact but is not the top message.
		if got := strings.Count(out, `action="/contacts/quick-add"`); got != 0 {
			t.Errorf("%s: contact sender still offers quick-add (%d forms)", name, got)
		}
	}
}

func TestSenderAvatarInitialsAreRuneSafe(t *testing.T) {
	tests := []struct{ name, email, want string }{
		{"Marcus Webb", "m@x.io", "MW"},
		{"marcus", "m@x.io", "MA"},
		{"Émile", "e@x.io", "ÉM"},
		{"李雷", "l@x.io", "李雷"},
		{"李", "li@x.io", "LI"},
		{"Zoë Åberg", "z@x.io", "ZÅ"},
		{"", "bob@x.io", "BO"},
		{"", "é", "??"},
		{"", "", "??"},
	}
	for _, tt := range tests {
		if got := avatarInitials(tt.name, tt.email); got != tt.want {
			t.Errorf("avatarInitials(%q, %q) = %q, want %q", tt.name, tt.email, got, tt.want)
		}
	}
}
