package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
)

func init() { i18n.Init("en") }

func renderRows(t *testing.T, msgs []MessageSummary, mailbox string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := MessageRows(msgs, mailbox).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render MessageRows: %v", err)
	}
	return buf.String()
}

func renderContent(t *testing.T, mailbox string, counts ListCounts, msgs []MessageSummary) string {
	t.Helper()
	var buf bytes.Buffer
	if err := MailboxContent(mailbox, counts, msgs, 1, 1, "", false).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render MailboxContent: %v", err)
	}
	return buf.String()
}

func baseMsg() MessageSummary {
	return MessageSummary{UID: 42, From: "Ann", Subject: "Quarterly plan", Date: time.Now(), Seen: true}
}

func TestMessageRowPreview(t *testing.T) {
	tests := []struct {
		name    string
		preview string
		want    []string
		absent  []string
	}{
		{"shown", "Let us meet on Thursday", []string{`class="msg-preview"`, "Let us meet on Thursday"}, nil},
		{"escaped", "<b>x</b>", []string{"&lt;b&gt;x&lt;/b&gt;"}, []string{"<b>x</b>"}},
		{"empty omits the line", "", nil, []string{"msg-preview"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := baseMsg()
			m.Preview = tc.preview
			out := renderRows(t, []MessageSummary{m}, "INBOX")
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in %s", w, out)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(out, a) {
					t.Errorf("unexpected %q in %s", a, out)
				}
			}
		})
	}
}

func TestMessageRowPaperclipOnlyWithAttachments(t *testing.T) {
	for _, has := range []bool{true, false} {
		m := baseMsg()
		m.HasAttachments = has
		out := renderRows(t, []MessageSummary{m}, "INBOX")
		got := strings.Contains(out, `class="msg-attach"`)
		if got != has {
			t.Errorf("HasAttachments=%v: paperclip present=%v", has, got)
		}
		if has && (!strings.Contains(out, "<svg") || strings.Contains(out, "mail.hasAttachments")) {
			t.Errorf("paperclip should render an icon and a translated label: %s", out)
		}
	}
}

func TestMessageRowThreadCountPill(t *testing.T) {
	tests := []struct {
		count int
		want  bool
	}{{0, false}, {1, false}, {2, true}, {7, true}}
	for _, tc := range tests {
		m := baseMsg()
		m.ThreadCount = tc.count
		m.ThreadRoot = "root9"
		out := renderRows(t, []MessageSummary{m}, "INBOX")
		if got := strings.Contains(out, `class="msg-thread-count"`); got != tc.want {
			t.Errorf("ThreadCount=%d: pill present=%v, want %v", tc.count, got, tc.want)
		}
		if got := strings.Contains(out, `id="thread-root9"`); got != tc.want {
			t.Errorf("ThreadCount=%d: thread container present=%v, want %v", tc.count, got, tc.want)
		}
		if tc.want && !strings.Contains(out, `hx-target="#thread-root9"`) {
			t.Errorf("thread pill lost its hx-target: %s", out)
		}
	}
}

func TestMessageRowTagPills(t *testing.T) {
	m := baseMsg()
	m.Tags = []TagDot{{Name: "Work", ColourIndex: 2}, {Name: "Home", ColourIndex: 5}}
	m.OverflowTags = 4
	out := renderRows(t, []MessageSummary{m}, "INBOX")
	for _, want := range []string{
		"Work", "Home", "tag-palette-bg-2", "tag-palette-bg-5", `class="msg-tags"`, "+4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "mail.moreTags") {
		t.Errorf("overflow label is an untranslated key: %s", out)
	}
	if strings.Contains(out, `class="tag-dots" aria-hidden`) {
		t.Errorf("tag names must not be hidden from assistive tech")
	}

	m.Tags, m.OverflowTags = nil, 0
	if out := renderRows(t, []MessageSummary{m}, "INBOX"); strings.Contains(out, "msg-tags") {
		t.Errorf("untagged message rendered a tag line: %s", out)
	}
}

func TestMessageRowKeepsHooks(t *testing.T) {
	m := baseMsg()
	m.Seen = false
	m.ThreadCount = 2
	m.ThreadRoot = "r1"
	out := renderRows(t, []MessageSummary{m}, "INBOX")
	for _, want := range []string{
		`class="msg-row msg-row-grid unread"`, `role="listitem"`, `data-uid="42"`, `data-pane-url="/mail/INBOX/42?pane=1"`,
		`data-message-link="true"`, `hx-post="/mail/INBOX/42/flags"`, `hx-swap="outerHTML"`,
		`x-on:contextmenu`, `x-on:touchstart`, `class="msg-check"`, `id="thread-r1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing hook %q in %s", want, out)
		}
	}
}

func TestMailboxHeaderCounts(t *testing.T) {
	out := renderContent(t, "INBOX", ListCounts{Unread: 3, Total: 7}, []MessageSummary{baseMsg()})
	if !strings.Contains(out, "3 unread") || !strings.Contains(out, "7 total") {
		t.Errorf("header counts missing: %s", out)
	}
	if strings.Contains(out, "mailbox.unreadCount") || strings.Contains(out, "mailbox.totalCount") {
		t.Errorf("count labels are untranslated keys")
	}
	swapped := renderContent(t, "INBOX", ListCounts{Unread: 7, Total: 3}, nil)
	if strings.Contains(swapped, "3 unread") || !strings.Contains(swapped, "7 unread") || !strings.Contains(swapped, "3 total") {
		t.Errorf("unread and total must not be interchangeable: %s", swapped)
	}
}

func TestMailboxContentKeepsStructure(t *testing.T) {
	out := renderContent(t, "INBOX", ListCounts{1, 2}, []MessageSummary{baseMsg()})
	for _, want := range []string{
		`id="message-list"`, `id="reading-pane"`, `class="mail-layout"`, `class="message-list-pane"`,
		`x-data="messageList"`, `x-on:click="setLayout('list')"`, `x-on:click="setLayout('two-pane')"`,
		`x-on:click="setLayout('three-pane')"`, `hx-get="/mail/INBOX"`, `hx-target="#main-content"`,
		`class="bulk-toolbar"`, `x-data="spamScan(&#39;INBOX&#39;)"`, `class="page-info"`, "1 of 1",
		`<h1`, `INBOX`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "&#9776;") || strings.Contains(out, "&#9707;") || strings.Contains(out, "&#9701;") {
		t.Errorf("layout toggle still uses text glyphs")
	}
	if strings.Count(out, `class="layout-opt`) != 3 {
		t.Errorf("want three layout options")
	}
}

func TestEmptyReadingPane(t *testing.T) {
	out := renderContent(t, "INBOX", ListCounts{}, nil)
	i := strings.Index(out, `id="reading-pane"`)
	if i < 0 {
		t.Fatal("no reading pane")
	}
	pane := out[i:]
	for _, want := range []string{`class="reading-empty"`, "Select a message", "<svg"} {
		if !strings.Contains(pane, want) {
			t.Errorf("empty reading pane missing %q", want)
		}
	}
	if strings.Contains(pane, "mail.selectAMessage") || strings.Contains(pane, "mail.readingKeyboardHint") {
		t.Errorf("empty reading pane shows raw i18n keys")
	}
}

func TestInfiniteScrollSentinelUnchanged(t *testing.T) {
	var buf bytes.Buffer
	if err := InfiniteScrollSentinel("INBOX", 2).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "msg-row-grid") {
		t.Errorf("sentinel must not take the message grid")
	}
}
