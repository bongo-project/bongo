package icalevent

import (
	"strings"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// InviteRelevantChange reports whether an edit changes what an invitation
// tells: when, where, how often, who, or what it is called and described.
// It compares structured fields only, never IcalRaw, SCHEDULE-STATUS, PARTSTAT
// or DTSTAMP, so a client echoing the server's own rewrite does not count.
func InviteRelevantChange(before, after *bongov1.Event) bool {
	if before.StartTime != after.StartTime || before.EndTime != after.EndTime || before.AllDay != after.AllDay ||
		before.Location != after.Location || before.Rrule != after.Rrule || before.Title != after.Title ||
		lineBreaks.Replace(before.Description) != lineBreaks.Replace(after.Description) {
		return true
	}
	a, b := addressSet(before), addressSet(after)
	if len(a) != len(b) {
		return true
	}
	for k := range a {
		if !b[k] {
			return true
		}
	}
	return false
}

// lineBreaks folds CRLF and lone CR to LF: a description a CalDAV client sent
// with CRLF is the same text as the stored one with LF.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n")

func addressSet(e *bongov1.Event) map[string]bool {
	out := make(map[string]bool, len(e.Attendees))
	for _, a := range e.Attendees {
		out[strings.ToLower(a.Address)] = true
	}
	return out
}
