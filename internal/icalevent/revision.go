package icalevent

import (
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Sequence reads SEQUENCE from props. An absent, unreadable or negative value
// is 0; surrounding white space is ignored.
func Sequence(props ical.Props) int {
	p := props.Get(ical.PropSequence)
	if p == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(p.Value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// BumpRevision records an edit on a component: SEQUENCE becomes one more than
// it was (see Sequence), and DTSTAMP and LAST-MODIFIED become now in UTC. It
// returns the new SEQUENCE. A value near the int range is capped before the
// increment so it cannot wrap negative.
func BumpRevision(props ical.Props, now time.Time) int {
	n := min(Sequence(props), 1<<30) + 1
	SetRevision(props, n, now)
	return n
}

// SetSequence sets SEQUENCE to seq and leaves DTSTAMP and LAST-MODIFIED as
// they are, for a caller that keeps a client's object otherwise untouched.
func SetSequence(props ical.Props, seq int) {
	setInt(props, ical.PropSequence, seq)
}

// SetRevision sets SEQUENCE to seq exactly, and DTSTAMP and LAST-MODIFIED to
// now in UTC. Use BumpRevision for an edit; this is for a caller that already
// knows the value to carry.
func SetRevision(props ical.Props, seq int, now time.Time) {
	SetSequence(props, seq)
	props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	props.SetDateTime(ical.PropLastModified, now.UTC())
}

// ClampSequence converts a SEQUENCE to the structured int32 field, within
// 0 to 1<<30.
func ClampSequence(n int) int32 {
	n = max(0, min(n, 1<<30))
	return int32(n)
}
