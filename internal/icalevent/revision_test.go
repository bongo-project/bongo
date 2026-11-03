package icalevent

import (
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-ical"
)

func TestBumpRevision(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.FixedZone("x", 3600))
	tests := []struct {
		name string
		seq  string
		set  bool
		want int
	}{
		{"absent", "", false, 1},
		{"padded", " 4 ", true, 5},
		{"negative", "-3", true, 1},
		{"junk", "junk", true, 1},
		{"huge", "9223372036854775807", true, 1<<30 + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := ical.Props{}
			if tt.set {
				p := ical.NewProp(ical.PropSequence)
				p.Value = tt.seq
				props.Set(p)
			}
			if got := BumpRevision(props, now); got != tt.want {
				t.Fatalf("BumpRevision = %d, want %d", got, tt.want)
			}
			sp := props.Get(ical.PropSequence)
			if sp == nil || sp.Value != strconv.Itoa(tt.want) || sp.Params.Get(ical.ParamValue) != "" {
				t.Errorf("SEQUENCE prop = %+v", sp)
			}
			for _, name := range []string{ical.PropDateTimeStamp, ical.PropLastModified} {
				p := props.Get(name)
				if p == nil || p.Value != "20261103T090000Z" {
					t.Errorf("%s = %+v", name, p)
				}
			}
		})
	}
}
