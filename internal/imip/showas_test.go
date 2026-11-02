package imip

import (
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

// An invitation shows the organiser's Show as choice: from the structured
// fields when there is no stored object, from the stored object otherwise.
func TestBuildCalendarCarriesTransp(t *testing.T) {
	transp := func(t *testing.T, free bool, raw string) string {
		t.Helper()
		e := structured()
		e.Transparent = free
		if raw != "" {
			e.IcalRaw = []byte(raw)
		}
		cal, err := BuildCalendar(MethodRequest, e, "", testNow)
		if err != nil {
			t.Fatal(err)
		}
		if p := vevent(t, cal).Props.Get(ical.PropTransparency); p != nil {
			return p.Value
		}
		return ""
	}
	if got := transp(t, true, ""); got != "TRANSPARENT" {
		t.Errorf("structured free: TRANSP = %q", got)
	}
	if got := transp(t, false, ""); got != "" {
		t.Errorf("structured busy: TRANSP = %q, want none", got)
	}
	withTransp := strings.Replace(rawICS, "SUMMARY:", "TRANSP:TRANSPARENT\r\nSUMMARY:", 1)
	if withTransp == rawICS {
		t.Fatal("fixture has no SUMMARY line to anchor on")
	}
	if got := transp(t, true, withTransp); got != "TRANSPARENT" {
		t.Errorf("raw free: TRANSP = %q", got)
	}
	if got := transp(t, false, rawICS); got != "" {
		t.Errorf("raw busy: TRANSP = %q, want none", got)
	}
}
