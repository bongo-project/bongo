package templates

import (
	"strings"
	"testing"
)

// TestContactEditorAlpineExpressionsAreCSPSafe: the editor bound its rows with
// x-model="em.value" and friends, and the CSP build forbids property writes,
// so the model never updated and adding or removing a row could lose typed
// values. The rows now write through setRow.
func TestContactEditorAlpineExpressionsAreCSPSafe(t *testing.T) {
	fd := contactFormData{
		Guid:        "c1",
		DisplayName: "Ann",
		Emails:      []contactFormRow{{Value: "ann@example.com", Type: "work"}, {Value: `o'brien"x\y@example.com`, Type: "x-custom"}},
		Phones:      []contactFormRow{{Value: "+44 20 7946 0000", Type: "cell"}},
		Addresses:   []contactAddressRow{{Type: "home", Street: "1 High St\nFlat 2", City: "Leeds", Country: "UK"}},
	}
	for name, isNew := range map[string]bool{"new": true, "edit": false} {
		data := fd
		if isNew {
			data = contactFormData{}
		}
		out := renderToString(t, ContactEditForm(data, isNew))
		assertCSPSafe(t, "ContactEditForm("+name+")", out)
		if !strings.Contains(out, `x-data="contactEditor"`) {
			t.Errorf("%s: editor is not the contactEditor component", name)
		}
	}
}

func TestContactEditorSeedsAreJSON(t *testing.T) {
	if got := contactRowsJSON(nil); got != `[{"value":"","type":""}]` {
		t.Errorf("empty rows = %s, want one blank row", got)
	}
	got := contactRowsJSON([]contactFormRow{{Value: `a'b"c\d`, Type: "work"}})
	if want := `[{"value":"a'b\"c\\d","type":"work"}]`; got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
	if got := contactAddressesJSON(nil); got != `[]` {
		t.Errorf("empty addresses = %s, want []", got)
	}
	got = contactAddressesJSON([]contactAddressRow{{Type: "home", Street: "1 High St\nFlat 2", PostalCode: "LS1"}})
	if want := `[{"type":"home","street":"1 High St\nFlat 2","city":"","region":"","postal":"LS1","country":""}]`; got != want {
		t.Errorf("addresses = %s, want %s", got, want)
	}
}
