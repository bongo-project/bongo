package imip

import (
	"fmt"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Describe returns the subject and the plain-text body for an iTIP message
// about e. tr translates a message ID into the reader's language; the IDs are
// imip.subject*, imip.intro*, imip.when, imip.where and imip.organiser. updated
// selects the wording for a REQUEST that changes an event already sent. For a
// REPLY, e.Attendees holds the replying attendee, whose PARTSTAT picks the
// wording.
// Times are shown in loc (UTC when nil), with the zone abbreviation.
func Describe(method string, updated bool, e *bongov1.Event, loc *time.Location, tr func(id string) string) (subject, body string) {
	kind := "Request"
	switch {
	case method == MethodCancel:
		kind = "Cancel"
	case updated:
		kind = "Update"
	}
	// The sender of intro is the organiser, except in a REPLY, where it is the
	// attendee who answered.
	who := e.Organizer
	if method == MethodReply {
		kind = "ReplyTentative"
		who = ""
		if len(e.Attendees) > 0 {
			who = e.Attendees[0].Address
			switch e.Attendees[0].Partstat {
			case "ACCEPTED":
				kind = "ReplyAccepted"
			case "DECLINED":
				kind = "ReplyDeclined"
			}
		}
	}
	subject = fmt.Sprintf(tr("imip.subject"+kind), e.Title)

	var b strings.Builder
	fmt.Fprintf(&b, tr("imip.intro"+kind), who)
	b.WriteString("\r\n\r\n")
	b.WriteString(e.Title + "\r\n")
	b.WriteString(tr("imip.when") + ": " + whenText(e, loc) + "\r\n")
	if e.Location != "" {
		b.WriteString(tr("imip.where") + ": " + e.Location + "\r\n")
	}
	b.WriteString(tr("imip.organiser") + ": " + e.Organizer + "\r\n")
	if e.Description != "" && method != MethodReply {
		b.WriteString("\r\n" + strings.ReplaceAll(strings.ReplaceAll(e.Description, "\r\n", "\n"), "\n", "\r\n") + "\r\n")
	}
	return subject, b.String()
}

// When renders the span of e in loc (UTC when nil), the way Describe writes it:
// ISO dates, 24-hour times and a zone abbreviation, whatever the locale.
func When(e *bongov1.Event, loc *time.Location) string { return whenText(e, loc) }

// whenText renders the span of e in a form that does not depend on the locale.
func whenText(e *bongov1.Event, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	if e.AllDay {
		start, end := icalevent.AllDayDate(e.StartTime), icalevent.AllDayDate(e.EndTime)
		// DTEND of an all-day event is exclusive.
		if last := end.AddDate(0, 0, -1); last.After(start) {
			return start.Format("2006-01-02") + " - " + last.Format("2006-01-02")
		}
		return start.Format("2006-01-02")
	}
	start, end := time.Unix(e.StartTime, 0).In(loc), time.Unix(e.EndTime, 0).In(loc)
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return start.Format("2006-01-02 15:04") + " - " + end.Format("15:04 MST")
	}
	return start.Format("2006-01-02 15:04") + " - " + end.Format("2006-01-02 15:04 MST")
}
