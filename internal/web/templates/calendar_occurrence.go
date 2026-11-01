package templates

import (
	"context"
	"net/url"

	"github.com/a-h/templ"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type occurrenceKey struct{}

// WithOccurrence records the occurrence of a recurring event the page was
// opened for, so the event form can say which instance it came from. The form
// itself stays bound to the series.
func WithOccurrence(ctx context.Context, occ *bongov1.Event) context.Context {
	return context.WithValue(ctx, occurrenceKey{}, occ)
}

// occurrenceFromCtx returns the occurrence recorded by WithOccurrence, or nil.
func occurrenceFromCtx(ctx context.Context) *bongov1.Event {
	occ, _ := ctx.Value(occurrenceKey{}).(*bongov1.Event)
	return occ
}

// EventHref is the link to an event. An expanded instance keeps its series
// guid and names the occurrence in the query, so the link is a real key.
func EventHref(e *bongov1.Event) templ.SafeURL {
	u := "/calendar/event/" + url.PathEscape(e.Guid)
	q := url.Values{}
	// A shared event is addressed in its owner's key space.
	if owner := eventOwner(e); owner != "" {
		q.Set("owner", owner)
	}
	if e.RecurrenceId != "" {
		q.Set("occurrence", e.RecurrenceId)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return templ.SafeURL(u)
}

// occurrenceWhen is the date, and for a timed occurrence the time, of an
// occurrence in the viewer's zone.
func occurrenceWhen(ctx context.Context, occ *bongov1.Event) string {
	if occ.AllDay {
		return formatEventListDate(ctx, occ)
	}
	return formatEventDate(ctx, occ.StartTime) + " " + formatEventTime(ctx, occ.StartTime)
}

// formTimes is the event whose date and times the event form shows: the
// occurrence when the form was opened from one, otherwise the event itself.
// The form still posts to the series; the server decides what the times mean
// for the chosen scope.
func formTimes(ctx context.Context, e *bongov1.Event) *bongov1.Event {
	if occ := occurrenceFromCtx(ctx); occ != nil && e != nil {
		return occ
	}
	return e
}
