package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

func renderQuarantine(t *testing.T, msgs []QuarantineEntry) string {
	t.Helper()
	var buf bytes.Buffer
	if err := AdminQuarantine(msgs, "admin@example.com", "").Render(context.Background(), &buf); err != nil {
		t.Fatalf("render AdminQuarantine: %v", err)
	}
	return buf.String()
}

func quarantineFixture() []QuarantineEntry {
	return []QuarantineEntry{
		{ID: "aaa111", Sender: "a@spam.test", Subject: "Win", Score: 0.97, ReceivedAt: time.Unix(1700000000, 0)},
		{ID: "bbb222", Sender: "b@spam.test", Subject: "Maybe", Score: 0.42, ReceivedAt: time.Unix(1700000100, 0)},
	}
}

// formFor returns the markup of the <form> whose action equals action,
// starting at the first match at or after from.
func formFor(t *testing.T, html, action string) string {
	t.Helper()
	i := strings.Index(html, `action="`+action+`"`)
	if i < 0 {
		t.Fatalf("no form with action %q", action)
	}
	start := strings.LastIndex(html[:i], "<form")
	end := strings.Index(html[i:], "</form>")
	return html[start : i+end]
}

func TestQuarantineScoreRenderedInSpamScoreSpan(t *testing.T) {
	html := renderQuarantine(t, quarantineFixture())
	for _, want := range []string{"97%", "42%"} {
		re := regexp.MustCompile(`<span class="quarantine-spamscore">\s*` + regexp.QuoteMeta(want) + `\s*</span>`)
		if !re.MatchString(html) {
			t.Errorf("score %s not rendered in .quarantine-spamscore", want)
		}
	}
	if strings.Count(html, `class="quarantine-spamscore"`) != 2 {
		t.Errorf("want one score span per row")
	}
	if strings.Contains(html, "badge-pending") {
		t.Error("old score badge classes must be gone")
	}
}

func TestQuarantineRowActions(t *testing.T) {
	html := renderQuarantine(t, quarantineFixture())
	for _, id := range []string{"aaa111", "bbb222"} {
		rel := formFor(t, html, "/admin/quarantine/"+id+"/release")
		if !strings.Contains(rel, `class="btn-outline"`) {
			t.Errorf("%s release is not an outline button: %s", id, rel)
		}
		del := formFor(t, html, "/admin/quarantine/"+id+"/delete")
		if !strings.Contains(del, `class="btn-flush"`) {
			t.Errorf("%s delete is not the danger button: %s", id, del)
		}
	}
}

func TestQuarantineHeaderForms(t *testing.T) {
	html := renderQuarantine(t, quarantineFixture())
	// Header forms are the ones carrying server-rendered ids.
	re := regexp.MustCompile(`(?s)<form[^>]*action="/admin/quarantine/bulk"[^>]*>.*?</form>`)
	var rescan, empty string
	for _, f := range re.FindAllString(html, -1) {
		if strings.Contains(f, `name="ids"`) {
			switch {
			case strings.Contains(f, `value="rescan"`):
				rescan = f
			case strings.Contains(f, `value="delete"`):
				empty = f
			}
		}
	}
	if rescan == "" || empty == "" {
		t.Fatalf("header rescan/empty forms missing (rescan=%t empty=%t)", rescan != "", empty != "")
	}
	for name, f := range map[string]string{"rescan": rescan, "empty": empty} {
		for _, id := range []string{"aaa111", "bbb222"} {
			if !strings.Contains(f, `name="ids" value="`+id+`"`) {
				t.Errorf("%s form lacks hidden id %s", name, id)
			}
		}
		if strings.Contains(f, "appendIds") || strings.Contains(f, `id="bulk-ids`) {
			t.Errorf("%s form must stay off the selection mechanism", name)
		}
	}
	if !strings.Contains(empty, `@submit="confirmEmpty"`) || !strings.Contains(empty, "data-confirm=") {
		t.Errorf("empty junk form lacks confirm hook: %s", empty)
	}
	if !strings.Contains(empty, `class="btn-flush"`) {
		t.Errorf("empty junk is not the danger button")
	}
	if !strings.Contains(rescan, `class="btn-outline"`) {
		t.Errorf("rescan is not an outline button")
	}
}

func TestQuarantineNoHeaderFormsWhenEmpty(t *testing.T) {
	html := renderQuarantine(t, nil)
	if strings.Contains(html, `name="ids"`) {
		t.Error("no hidden ids expected with zero entries")
	}
	if strings.Contains(html, "confirmEmpty") {
		t.Error("empty junk form must not render with zero entries")
	}
	if !strings.Contains(html, `colspan="7"`) {
		t.Error("empty-state row missing")
	}
	if strings.Contains(html, `class="meta"`) || strings.Contains(html, quarantineCountLabel()) {
		t.Error("no count expected with zero entries")
	}
}

// quarantineCountLabel is the translated count label; inside a test the
// parameter t hides the package's t().
func quarantineCountLabel() string {
	return t(context.Background(), "quarantine.messagesInQuarantine")
}

func TestQuarantineCountShownOnceInHeader(t *testing.T) {
	html := renderQuarantine(t, quarantineFixture())
	label := quarantineCountLabel()
	if !strings.Contains(html, `<span class="meta">2 `+label+`</span>`) {
		t.Error("header meta count missing")
	}
	if n := strings.Count(html, label); n != 1 {
		t.Errorf("count rendered %d times, want 1", n)
	}
	if strings.Index(html, `class="meta"`) > strings.Index(html, `class="banner warn"`) {
		t.Error("count must sit in the header, before the banner")
	}
	if strings.Contains(html, `role="alert"`) {
		t.Error("static banner must not be role=alert")
	}
}

func TestQuarantineStructure(t *testing.T) {
	html := renderQuarantine(t, quarantineFixture())
	for _, want := range []string{
		`class="page-head"`, `class="banner warn"`, `role="note"`,
		`class="admin-table data-table"`, `class="card-body flush"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
}
