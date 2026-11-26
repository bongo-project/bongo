package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderShare(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The three share forms carry the combobox and its listbox, and every Alpine
// expression in them is one the CSP build can run.
func TestShareFormsRenderCombobox(t *testing.T) {
	tests := []struct {
		name string
		out  string
		// extra strings the form must keep.
		want []string
	}{
		{
			name: "folders",
			out: renderShare(t, FolderSharePage(FolderSharePageData{
				Email:   "me@example.com",
				Folders: []FolderShareFolder{{Name: "Projects"}, {Name: "Archive"}},
			})),
			want: []string{`action="/settings/folders/share"`, `name="folder"`, `aria-label=`},
		},
		{
			name: "address books",
			out: renderShare(t, BookSharePage(BookSharePageData{
				BookID: strings.Repeat("a", 32), BookName: "Clients", Email: "me@example.com",
			})),
			want: []string{`/share"`, `placeholder=`},
		},
		{
			name: "calendars",
			out: renderShare(t, CalendarSettingsPage(
				[]CalendarSettings{{Calendar: "cal1", Name: "Work", CanShare: true}}, nil, "me@example.com")),
			// The label must still point at the input.
			want: []string{`for="share-addr-cal1"`, `id="share-addr-cal1"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, want := range append([]string{
				`data-gal-share`, `role="combobox"`, `aria-autocomplete="list"`,
				`:aria-controls="listboxId()"`, `:aria-activedescendant="activeId()"`,
				`:aria-expanded="expanded()"`, `role="listbox"`, `:id="listboxId()"`,
				`:id="optionId(i)"`, `:aria-selected="isSelected(i)"`,
				`data-gal-count-one="`, `data-gal-count-many="`, `aria-live="polite"`,
				`name="share_with"`, `type="email"`, ` required`,
			}, tt.want...) {
				if !strings.Contains(tt.out, want) {
					t.Errorf("output lacks %q", want)
				}
			}
			assertCSPSafe(t, tt.name, tt.out)
		})
	}
}

func TestShareAddressInputIsKeyboardAndTranslated(t *testing.T) {
	out := renderShare(t, shareAddressInput("x", "ph", "lbl", "u-flex-1"))
	for _, want := range []string{
		`@keydown="onKeydown($event)"`, `@blur="hide()"`, `@mousedown.prevent="select(r)"`,
		`aria-label="Suggestions"`, `data-gal-count-many="%d suggestions available"`, `id="x"`, `u-flex-1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// No id means no id attribute on the input.
	if bare := renderShare(t, shareAddressInput("", "ph", "lbl", "")); strings.Contains(bare, ` id="" `) {
		t.Error("empty id rendered")
	}
}
