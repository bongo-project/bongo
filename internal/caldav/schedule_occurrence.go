package caldav

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Per-occurrence scheduling (RFC 6638 section 3.2.1, RFC 5546 section 3.2.2):
// an organiser PUT that adds or changes an override sends a REQUEST for that
// occurrence alone (its RECURRENCE-ID, the series UID, no RRULE), and one that
// excludes an occurrence, by EXDATE or by an override with STATUS:CANCELLED,
// sends a CANCEL for it. The series message of Scheduler.Organise carries the
// master only, so the exceptions are told separately.
//
// What counts as a change is what the override itself contributes: the fields
// in which the occurrence differs from the one the rule alone makes. A change
// to the master that an override inherits is the series message's, and a
// client's upload of an override only to acknowledge an alarm contributes
// nothing, so neither sends a second message.

// maxOccurrenceMessagesPerPut bounds the occurrence messages one PUT sends:
// each can name up to MaxAttendees recipients and the budget is the
// organiser's, so a PUT of a long series with many overrides cannot spend it
// all at once. The rest are logged and not sent; the next change to them is.
const maxOccurrenceMessagesPerPut = 20

// occurrenceJob is one message about one occurrence: a CANCEL when instance is
// nil, otherwise a REQUEST. original is the occurrence as attendees last saw it.
type occurrenceJob struct {
	rid                string
	original, instance *bongov1.Event
}

// seriesMoved reports whether the master's own schedule changed, which makes
// the RECURRENCE-IDs of before and after incomparable; the series message
// covers it.
func seriesMoved(before, after *bongov1.Event) bool {
	return before.StartTime != after.StartTime || before.Rrule != after.Rrule ||
		before.AllDay != after.AllDay || before.Timezone != after.Timezone
}

// occurrenceJobs lists the occurrence messages a save calls for, by
// RECURRENCE-ID. existing is nil for a create, where only an override naming
// someone the master does not is told: the others have the series message.
func occurrenceJobs(existing, saved *bongov1.Event) []occurrenceJob {
	after, ok := store.OpenSeries(saved)
	if !ok {
		return nil
	}
	cancelled, overridden := after.Exceptions()

	if existing == nil {
		master := addressSet(saved.Attendees)
		var jobs []occurrenceJob
		for _, rid := range overridden {
			_, eff := after.Occurrence(rid)
			if eff == nil {
				continue
			}
			for _, a := range eff.Attendees {
				if !master[strings.ToLower(a.Address)] {
					jobs = append(jobs, occurrenceJob{rid: rid, instance: eff})
					break
				}
			}
		}
		return jobs
	}

	before, ok := store.OpenSeries(existing)
	if !ok || seriesMoved(existing, saved) {
		return nil
	}
	bc, bo := before.Exceptions()
	rids := slices.Concat(cancelled, overridden, bc, bo)
	sort.Strings(rids)
	rids = slices.Compact(rids)

	var jobs []occurrenceJob
	for _, rid := range rids {
		bp, be := before.Occurrence(rid)
		ap, ae := after.Occurrence(rid)
		if overrideContribution(bp, be) == overrideContribution(ap, ae) {
			continue
		}
		jobs = append(jobs, occurrenceJob{rid: rid, original: be, instance: ae})
	}
	return jobs
}

// overrideContribution describes what the exception on an occurrence changes
// of the plain one, in the fields an invitation tells. It is "" when the
// occurrence is as the rule makes it, so two states compare equal when their
// exceptions tell attendees the same.
func overrideContribution(plain, effective *bongov1.Event) string {
	switch {
	case plain == nil && effective == nil:
		return ""
	case effective == nil:
		return "cancelled"
	case plain == nil:
		plain = &bongov1.Event{} // an override off the rule: all of it is the override's
	}
	var parts []string
	add := func(name string, differs bool, v any) {
		if differs {
			parts = append(parts, fmt.Sprintf("%s=%v", name, v))
		}
	}
	add("start", effective.StartTime != plain.StartTime, effective.StartTime)
	add("end", effective.EndTime != plain.EndTime, effective.EndTime)
	add("allday", effective.AllDay != plain.AllDay, effective.AllDay)
	add("title", effective.Title != plain.Title, effective.Title)
	add("location", effective.Location != plain.Location, effective.Location)
	add("description", icalevent.InviteRelevantChange(
		&bongov1.Event{Description: plain.Description}, &bongov1.Event{Description: effective.Description}), effective.Description)
	if !maps.Equal(addressSet(plain.Attendees), addressSet(effective.Attendees)) {
		var addrs []string
		for a := range addressSet(effective.Attendees) {
			addrs = append(addrs, a)
		}
		sort.Strings(addrs)
		parts = append(parts, "attendees="+strings.Join(addrs, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ";")
}

func addressSet(attendees []*bongov1.Attendee) map[string]bool {
	out := make(map[string]bool, len(attendees))
	for _, a := range attendees {
		out[strings.ToLower(a.Address)] = true
	}
	return out
}

// organiseOccurrences sends the occurrence messages of a saved PUT and returns
// the SCHEDULE-STATUS by RECURRENCE-ID and address.
func (b *Backend) organiseOccurrences(userID string, saved, existing *bongov1.Event, skip map[string]bool) map[string]map[string]string {
	jobs := occurrenceJobs(existing, saved)
	if len(jobs) > maxOccurrenceMessagesPerPut {
		b.logger.Warn("caldav: occurrence messages beyond the limit were not sent",
			"user", userID, "guid", saved.Guid, "limit", maxOccurrenceMessagesPerPut, "dropped", len(jobs)-maxOccurrenceMessagesPerPut)
		jobs = jobs[:maxOccurrenceMessagesPerPut]
	}
	var out map[string]map[string]string
	for _, j := range jobs {
		st := b.scheduler.OrganiseOccurrence(userID, existing, saved, j.rid, j.original, j.instance, skip)
		if out == nil {
			out = map[string]map[string]string{}
		}
		out[j.rid] = st
	}
	return out
}

// reviseOverrides raises the SEQUENCE of each override the PUT changes to above
// the series' and the stored override's, so its REQUEST outranks what
// attendees hold, as a web edit of one occurrence does. It reports whether it
// changed anything.
func reviseOverrides(calendar *ical.Calendar, event, existing *bongov1.Event) bool {
	master := masterOf(calendar)
	if master == nil {
		return false
	}
	floor := icalevent.Sequence(master.Props)
	changed := false
	for _, j := range occurrenceJobs(existing, event) {
		if j.instance == nil {
			continue
		}
		want := floor
		if j.original != nil {
			want = max(want, int(j.original.Sequence))
		}
		want = min(want+1, 1<<30)
		for _, ve := range calendar.Children {
			if ve.Name != ical.CompEvent || recurrenceKey(ve) != j.rid {
				continue
			}
			if icalevent.Sequence(ve.Props) < want {
				icalevent.SetSequence(ve.Props, want)
				changed = true
			}
		}
	}
	return changed
}
