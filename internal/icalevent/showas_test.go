package icalevent

import (
	"testing"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestSynthVEventTransp(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	base := func(transparent bool) *bongov1.Event {
		return &bongov1.Event{
			Guid: "g", Title: "x", Transparent: transparent,
			StartTime: now.Unix(), EndTime: now.Add(time.Hour).Unix(),
		}
	}
	for _, tc := range []struct {
		name        string
		transparent bool
		want        string
	}{
		{"free", true, "TRANSPARENT"},
		{"busy", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ve := SynthVEvent(base(tc.transparent), time.UTC, now)
			got := ""
			if p := ve.Props.Get(ical.PropTransparency); p != nil {
				got = p.Value
			}
			if got != tc.want {
				t.Errorf("TRANSP = %q, want %q", got, tc.want)
			}
		})
	}
}
