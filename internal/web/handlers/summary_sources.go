package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Limits on what a summary row shows. Mail headers and iCalendar text are
// written by other people.
const (
	maxSummaryTitle = 120
	maxSummarySub   = 100
)

// metaReader is the part of the Pebble store the mail sources read MailMeta
// with.
type metaReader interface {
	Get(key []byte) ([]byte, error)
}

// toMeQuerier lists the guids of mail addressed to the user, newest first.
type toMeQuerier interface {
	QueryToMe(userID string) ([]string, error)
}

// loadMeta reads a message's MailMeta, or nil when it cannot be read.
func loadMeta(p metaReader, userID, guid string) *bongov1.MailMeta {
	data, err := p.Get([]byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)))
	if err != nil {
		return nil
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

// messageHref is the link to a message. The mailbox is its real one: UIDs are
// only unique within a mailbox.
func messageHref(mailbox string, uid uint32) string {
	return fmt.Sprintf("/mail/%s/%d", url.PathEscape(mailbox), uid)
}

// mailSummary is the summary page's source for the two mail sections.
type mailSummary struct {
	mailboxes *store.MailboxStore
	metadata  toMeQuerier
	pebble    metaReader
	zone      func(userID string) *time.Location
}

// NewMailSummary returns the source of the unread-to-me and starred sections.
// metadata may be nil, which leaves the to-me section empty. zone gives the
// user's time zone; nil means UTC.
func NewMailSummary(mailboxes *store.MailboxStore, metadata toMeQuerier, pebble metaReader, zone func(userID string) *time.Location) summaryMail {
	return &mailSummary{mailboxes: mailboxes, metadata: metadata, pebble: pebble, zone: zone}
}

// UnreadToMe lists the newest unread messages addressed to the user. The guids
// come from the SQLite index, which has no per-mailbox flag, so the unread
// flag is tested on the Pebble record before the page is cut.
func (s *mailSummary) UnreadToMe(userID string, limit int) ([]templates.SummaryRow, error) {
	if s.metadata == nil {
		return nil, nil
	}
	guids, err := s.metadata.QueryToMe(userID)
	if err != nil {
		return nil, fmt.Errorf("querying mail to me: %w", err)
	}
	if len(guids) == 0 {
		return nil, nil
	}
	set := make(map[string]bool, len(guids))
	for _, g := range guids {
		set[g] = true
	}
	unread := func(m *bongov1.MailboxMessage) bool { return m.Flags&store.FlagSeen == 0 }
	msgs, boxes, _, err := s.mailboxes.ListViewMessagesFiltered(userID, set, unread, 0, limit)
	if err != nil {
		return nil, fmt.Errorf("listing unread mail to me: %w", err)
	}
	return s.rows(userID, msgs, boxes), nil
}

// Starred lists the newest starred messages across the user's mailboxes, the
// same set as the starred view.
func (s *mailSummary) Starred(userID string, limit int) ([]templates.SummaryRow, error) {
	msgs, boxes, _, err := s.mailboxes.ListVirtualMessages(userID, "starred", 0, limit)
	if err != nil {
		return nil, fmt.Errorf("listing starred mail: %w", err)
	}
	return s.rows(userID, msgs, boxes), nil
}

func (s *mailSummary) rows(userID string, msgs []*bongov1.MailboxMessage, boxes []string) []templates.SummaryRow {
	loc := time.UTC
	if s.zone != nil {
		loc = s.zone(userID)
	}
	out := make([]templates.SummaryRow, 0, len(msgs))
	for i, m := range msgs {
		row := templates.SummaryRow{
			Fallback: "quarantine.noSubject",
			Href:     messageHref(boxes[i], m.Uid),
			Unread:   m.Flags&store.FlagSeen == 0,
			When:     time.Unix(m.InternalDate, 0).In(loc),
		}
		if meta := loadMeta(s.pebble, userID, m.MailGuid); meta != nil {
			if subject := cleanDisplay(meta.Subject, maxSummaryTitle, false); subject != "" {
				row.Title = subject
			}
			row.Sub = cleanDisplay(meta.From, maxSummarySub, false)
		}
		out = append(out, row)
	}
	return out
}

// scheduleInboxLister lists a user's schedule inbox.
type scheduleInboxLister interface {
	List(userID string) ([]store.ScheduleItem, error)
}

// calendarSummary is the summary page's source for the two calendar sections.
type calendarSummary struct {
	cal       *CalendarHandler
	inbox     scheduleInboxLister
	mailboxes *store.MailboxStore
	pebble    metaReader
}

// NewCalendarSummary returns the source of the invitations and upcoming
// sections. mailboxes and pebble find the message an invitation came in; nil
// leaves every link on the calendar.
func NewCalendarSummary(cal *CalendarHandler, mailboxes *store.MailboxStore, pebble metaReader) *calendarSummary {
	return &calendarSummary{cal: cal, mailboxes: mailboxes, pebble: pebble}
}

// SetScheduleInbox wires the schedule inbox. Without it only the user's own
// calendar feeds the invitations section.
func (s *calendarSummary) SetScheduleInbox(i scheduleInboxLister) { s.inbox = i }

// Upcoming lists events and due tasks over the next days, soonest first. It
// gathers them through CalendarHandler.agendaDays, which the agenda uses too.
func (s *calendarSummary) Upcoming(r *http.Request, userID, email string, days, limit int) (*http.Request, []templates.SummaryRow, error) {
	now := s.cal.nowIn(userID)
	loc := now.Location()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	r, agenda, _, err := s.cal.agendaDays(r, userID, email, from, days, loc)
	if err != nil {
		return r, nil, fmt.Errorf("gathering upcoming items: %w", err)
	}
	var rows []templates.SummaryRow
	for _, day := range agenda {
		for _, it := range day.Items {
			if len(rows) >= limit {
				return r, rows, nil
			}
			row := templates.SummaryRow{Fallback: summaryNoTitleKey, When: day.Date, TimeLabel: it.TimeLabel, AllDay: it.TimeLabel == ""}
			switch {
			case it.Task != nil:
				row.Task = true
				row.Title = cleanDisplay(it.Task.Summary, maxSummaryTitle, false)
				row.Href = "/tasks/" + url.PathEscape(it.Task.Guid)
			case it.Event != nil:
				row.Title = cleanDisplay(it.Event.Title, maxSummaryTitle, false)
				row.Sub = cleanDisplay(it.Event.Location, maxSummarySub, false)
				row.Href = string(templates.EventHref(it.Event))
			default:
				continue
			}
			rows = append(rows, row)
		}
	}
	return r, rows, nil
}

// inviteCandidate is the newest word on one invitation: the item of the
// schedule inbox with the highest SEQUENCE for its UID and instance.
type inviteCandidate struct {
	item store.ScheduleItem
	inv  *imip.Invite
}

// Awaiting lists the invitations the user has not answered.
//
// The source is the RFC 6638 schedule inbox, which the delivery stage fills
// with every REQUEST addressed to a recipient whose copy was stored: small
// sanitised iCalendar objects, at most store.MaxScheduleInboxItems, read with
// no blob or MIME work. The user's own calendar is a second source, for an
// event a CalDAV client created from an inbox item and then deleted the item
// of. The decision about each inbox item is CalendarHandler.assess, the one
// the invitation card makes, so the page and the card never disagree.
//
// Limits: invitations delivered before the schedule inbox existed are not
// listed; with no inbox wired only the second source remains; a CalDAV client
// that deletes inbox items removes them unless the event is in the calendar
// with PARTSTAT NEEDS-ACTION; an item holds no reference to its message, so
// the link to it is found by delivery time and sender, and falls back to the
// event's day in the calendar.
func (s *calendarSummary) Awaiting(userID, email string, limit int) ([]templates.SummaryRow, error) {
	now := s.cal.nowIn(userID)
	loc := now.Location()
	var rows []templates.SummaryRow
	seen := map[string]bool{}

	fromInbox, err := s.fromInbox(userID, email, now, limit)
	if err != nil {
		return nil, err
	}
	for _, c := range fromInbox {
		seen[c.inv.UID] = true
		rows = append(rows, s.inboxRow(c, loc))
	}
	s.linkMessages(userID, fromInbox, rows)

	if len(rows) < limit {
		own, err := s.fromCalendar(userID, email, now, seen, limit-len(rows))
		if err != nil {
			return nil, err
		}
		rows = append(rows, own...)
	}
	return rows, nil
}

// fromInbox returns up to limit unanswered REQUESTs of the schedule inbox,
// newest first.
func (s *calendarSummary) fromInbox(userID, email string, now time.Time, limit int) ([]inviteCandidate, error) {
	if s.inbox == nil {
		return nil, nil
	}
	items, err := s.inbox.List(userID)
	if err != nil {
		return nil, fmt.Errorf("listing the schedule inbox: %w", err)
	}

	// One entry per UID and instance: the highest SEQUENCE, the later item on
	// a tie. A CANCEL that wins removes the invitation from the list below.
	best := map[string]inviteCandidate{}
	for _, it := range items {
		inv, err := imip.Parse(it.Data, "")
		if err != nil {
			continue
		}
		key := inv.UID + "\x00" + inv.RecurrenceID
		cur, ok := best[key]
		if !ok || inv.Sequence > cur.inv.Sequence || (inv.Sequence == cur.inv.Sequence && it.Name > cur.item.Name) {
			best[key] = inviteCandidate{item: it, inv: inv}
		}
	}
	cands := make([]inviteCandidate, 0, len(best))
	for _, c := range best {
		if c.inv.Method == imip.MethodRequest {
			cands = append(cands, c)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].item.Name > cands[j].item.Name })

	var out []inviteCandidate
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		if ended(c.inv.Event, now) {
			continue
		}
		st, err := s.cal.assess(inviteMessage{UserID: userID, Email: email, Part: c.item.Data})
		if err != nil {
			if errors.Is(err, errNotInvitation) {
				continue
			}
			return nil, fmt.Errorf("assessing an invitation: %w", err)
		}
		if st.inv.Method != imip.MethodRequest || st.me == nil || st.self || !st.canAnswer {
			continue
		}
		// A known event the user already answered. A reinstated one (higher
		// SEQUENCE after a cancellation) is a fresh question: decide allows
		// it, and it is listed.
		if st.known != nil {
			if p := myPartstat(st.known, st.mine); p != "" && p != "NEEDS-ACTION" {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// ended reports whether an event is over. A repeating event is never over.
func ended(e *bongov1.Event, now time.Time) bool {
	if e.Rrule != "" {
		return false
	}
	end := e.EndTime
	if end == 0 {
		end = e.StartTime
	}
	return end < now.Unix()
}

// fromCalendar lists the user's own events in the coming agenda range that
// someone else organised and the user has not answered, once per UID.
func (s *calendarSummary) fromCalendar(userID, email string, now time.Time, seen map[string]bool, limit int) ([]templates.SummaryRow, error) {
	loc := now.Location()
	events, err := s.cal.events.ListByDateRange(userID, now.Unix(), now.AddDate(0, 0, agendaRangeDays).Unix(), "")
	if err != nil {
		return nil, fmt.Errorf("listing own events: %w", err)
	}
	mine := s.cal.myAddresses(userID, email)
	var rows []templates.SummaryRow
	for _, e := range events {
		if len(rows) >= limit {
			break
		}
		if e.Status == "CANCELLED" || e.Organizer == "" || mine[store.NormaliseAddress(e.Organizer)] {
			continue
		}
		if myPartstat(e, mine) != "NEEDS-ACTION" {
			continue
		}
		uid := seriesUID(e)
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		rows = append(rows, templates.SummaryRow{
			Title:     cleanDisplay(e.Title, maxSummaryTitle, false),
			Fallback:  summaryNoTitleKey,
			Sub:       cleanDisplay(e.Organizer, maxSummarySub, false),
			Href:      string(templates.EventHref(e)),
			When:      eventDay(e, loc),
			TimeLabel: timeLabel(e, loc),
			AllDay:    e.AllDay,
		})
	}
	return rows, nil
}

// summaryNoTitleKey is the message shown for a row whose title is empty.
const summaryNoTitleKey = "summary.noTitle"

// eventDay is the date an event shows on: an all-day event keeps its UTC date.
func eventDay(e *bongov1.Event, loc *time.Location) time.Time {
	if e.AllDay {
		d := time.Unix(e.StartTime, 0).UTC()
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	}
	return time.Unix(e.StartTime, 0).In(loc)
}

func timeLabel(e *bongov1.Event, loc *time.Location) string {
	if e.AllDay {
		return ""
	}
	return time.Unix(e.StartTime, 0).In(loc).Format("15:04")
}

// inboxRow is the row of an inbox item. Every field comes from the invitation,
// which anyone can send, so it is cleaned and capped. The link is the event's
// day until linkMessages finds the message.
func (s *calendarSummary) inboxRow(c inviteCandidate, loc *time.Location) templates.SummaryRow {
	ev := c.inv.Event
	day := eventDay(ev, loc)
	return templates.SummaryRow{
		Title:     cleanDisplay(ev.Title, maxSummaryTitle, false),
		Fallback:  summaryNoTitleKey,
		Sub:       cleanDisplay(c.inv.Organizer, maxSummarySub, false),
		Href:      fmt.Sprintf("/calendar/day/%d/%d/%d", day.Year(), int(day.Month()), day.Day()),
		When:      day,
		TimeLabel: timeLabel(ev, loc),
		AllDay:    ev.AllDay,
	}
}

// inviteMatchWindow is how far an INBOX message's delivery time may be from
// the time its invitation was placed in the schedule inbox: both happen in one
// delivery.
const inviteMatchWindow = 2 * time.Minute

// linkMessages points rows at the INBOX message each invitation arrived in,
// where the card lets the user answer. A message matches when it was delivered
// within inviteMatchWindow of the item and its From header holds the
// organiser's address; with no single match the row keeps its calendar link.
func (s *calendarSummary) linkMessages(userID string, cands []inviteCandidate, rows []templates.SummaryRow) {
	if len(cands) == 0 || s.mailboxes == nil || s.pebble == nil {
		return
	}
	msgs, err := s.mailboxes.ListMessages(userID, "INBOX")
	if err != nil {
		return
	}
	for i, c := range cands {
		organiser := strings.ToLower(c.inv.Organizer)
		if organiser == "" {
			continue
		}
		var match *bongov1.MailboxMessage
		matches := 0
		for _, m := range msgs {
			d := time.Unix(m.InternalDate, 0).Sub(c.item.Received)
			if d < -inviteMatchWindow || d > inviteMatchWindow {
				continue
			}
			meta := loadMeta(s.pebble, userID, m.MailGuid)
			if meta == nil || !strings.Contains(strings.ToLower(meta.From), organiser) {
				continue
			}
			match = m
			if matches++; matches > 1 {
				break
			}
		}
		if matches == 1 {
			rows[i].Href = messageHref("INBOX", match.Uid)
		}
	}
}
