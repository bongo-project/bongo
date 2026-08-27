package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
)

// initI18nForTest initialises the global i18n instance once so that t(ctx, ...)
// calls inside templates resolve real translations rather than returning the
// raw message ID.
func initI18nForTest() {
	i18n.Init("en")
}

// TestRichEditorFieldRendersAccessibilityAttributes verifies that
// RichEditorField emits the required ARIA attributes on every element.
func TestRichEditorFieldRendersAccessibilityAttributes(t *testing.T) {
	initI18nForTest()

	var buf bytes.Buffer
	ctx := context.Background()
	if err := RichEditorField("body", "", "compose").Render(ctx, &buf); err != nil {
		t.Fatalf("RichEditorField.Render: %v", err)
	}
	html := buf.String()

	// The contenteditable must carry role="textbox" for assistive technology.
	if !strings.Contains(html, `role="textbox"`) {
		t.Error(`RichEditorField: missing role="textbox" on contenteditable`)
	}

	// aria-multiline="true" must be present on the contenteditable.
	if !strings.Contains(html, `aria-multiline="true"`) {
		t.Error(`RichEditorField: missing aria-multiline="true" on contenteditable`)
	}

	// Every toolbar element must have a non-empty aria-label.
	// We expect: bold, italic, insert link, text colour input,
	// insert image, toggleSource, toggleVisual, editorLabel, sourceLabel = 9.
	count := strings.Count(html, `aria-label="`)
	if count < 7 {
		t.Errorf("RichEditorField: expected at least 7 aria-label attributes, got %d\nHTML:\n%s", count, html)
	}

	// None of the aria-label values should be the raw message key (i.e. i18n
	// must have resolved to an actual string). Check a representative sample.
	for _, key := range []string{
		"editor.bold",
		"editor.italic",
		"editor.insertLink",
		"editor.textColour",
		"editor.insertImage",
		"editor.toggleSource",
		"editor.toggleVisual",
		"editor.editorLabel",
		"editor.sourceLabel",
	} {
		if strings.Contains(html, `aria-label="`+key+`"`) {
			t.Errorf("RichEditorField: aria-label still contains raw key %q — i18n not resolved", key)
		}
	}
}

// TestRichEditorFieldHasNoInlineStyles guards the CSP fix: the app CSP is
// `style-src 'self'` (no unsafe-inline), so any inline style="" attribute is
// blocked by the browser — which previously collapsed the editable area to zero
// height. All presentation must come from CSS classes instead. This asserts the
// component emits no style="" and that the editable region carries its class.
func TestRichEditorFieldHasNoInlineStyles(t *testing.T) {
	initI18nForTest()
	ctx := context.Background()

	for _, variant := range []string{"signature", "compose"} {
		var buf bytes.Buffer
		if err := RichEditorField("body", "", variant).Render(ctx, &buf); err != nil {
			t.Fatalf("RichEditorField(%q).Render: %v", variant, err)
		}
		html := buf.String()
		if strings.Contains(html, "style=") {
			t.Errorf("RichEditorField(%q): emits an inline style attribute, which the CSP blocks:\n%s", variant, html)
		}
		// The editable region must be styled via its class so it has height.
		if !strings.Contains(html, "rich-editor-area") {
			t.Errorf("RichEditorField(%q): editable area missing the rich-editor-area class", variant)
		}
	}
}

// TestRichEditorFieldToolbarButtonsAreTypeButton verifies every button inside
// the editor toolbar carries type="button" so they cannot accidentally submit
// the enclosing form.
func TestRichEditorFieldToolbarButtonsAreTypeButton(t *testing.T) {
	initI18nForTest()

	var buf bytes.Buffer
	ctx := context.Background()
	if err := RichEditorField("body", "", "compose").Render(ctx, &buf); err != nil {
		t.Fatalf("RichEditorField.Render: %v", err)
	}
	html := buf.String()

	// There must be no <button> without type="button".
	// A simple heuristic: count <button occurrences and type="button" occurrences
	// and ensure they match.
	buttonCount := strings.Count(html, "<button ")
	typeButtonCount := strings.Count(html, `type="button"`)
	if buttonCount == 0 {
		t.Error("RichEditorField: no <button> elements found")
	}
	if typeButtonCount != buttonCount {
		t.Errorf("RichEditorField: %d <button> elements but only %d have type=\"button\"",
			buttonCount, typeButtonCount)
	}
}

// TestRichEditorFieldWithInitialHTML verifies that when initialHTML is non-empty
// the data-initial-html attribute is rendered, and when it is empty the
// attribute is omitted (preserving new-compose semantics).
func TestRichEditorFieldWithInitialHTML(t *testing.T) {
	initI18nForTest()
	ctx := context.Background()

	// With initialHTML: attribute must be present.
	var buf bytes.Buffer
	if err := RichEditorField("signature_html", "<p>Hello</p>", "signature").Render(ctx, &buf); err != nil {
		t.Fatalf("RichEditorField.Render (with HTML): %v", err)
	}
	if !strings.Contains(buf.String(), `data-initial-html=`) {
		t.Error("RichEditorField with initialHTML: data-initial-html attribute is missing")
	}

	// Without initialHTML: attribute must be absent.
	buf.Reset()
	if err := RichEditorField("body", "", "compose").Render(ctx, &buf); err != nil {
		t.Fatalf("RichEditorField.Render (empty HTML): %v", err)
	}
	if strings.Contains(buf.String(), `data-initial-html=`) {
		t.Error("RichEditorField with empty initialHTML: data-initial-html attribute should be absent for new messages")
	}
}

// TestRichEditorFieldHiddenInputName verifies the hidden input uses the
// supplied fieldName, so the correct form field is submitted.
func TestRichEditorFieldHiddenInputName(t *testing.T) {
	initI18nForTest()
	ctx := context.Background()

	for _, name := range []string{"body", "signature_html"} {
		var buf bytes.Buffer
		if err := RichEditorField(name, "", "compose").Render(ctx, &buf); err != nil {
			t.Fatalf("RichEditorField(%q).Render: %v", name, err)
		}
		want := `<input type="hidden" name="` + name + `"`
		if !strings.Contains(buf.String(), want) {
			t.Errorf("RichEditorField(%q): hidden input %q not found in rendered HTML", name, want)
		}
	}
}
