package handlers

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ReminderMailer builds and delivers an event reminder email to the owner's
// own primary address. The address is local, so the message goes through the
// spool and pipeline: the owner's rules and forwarding apply.
type ReminderMailer struct {
	sender inviteSender
	prefs  localeSource
	logger *slog.Logger
}

// NewReminderMailer creates a mailer. sender is the compose handler's
// DeliverGenerated; prefs supplies the owner's stored locale and may be nil
// (English).
func NewReminderMailer(sender inviteSender, prefs localeSource, logger *slog.Logger) *ReminderMailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReminderMailer{sender: sender, prefs: prefs, logger: logger}
}

// SendReminder mails the reminder for the occurrence of ev that starts at
// start, shown in loc. to is the owner's primary address and is never taken
// from the event. The title and location are untrusted (CalDAV clients and
// invitations set them), so they pass through inviteName, which removes
// control characters. The description is left out. The error never carries the
// address or the body.
func (m *ReminderMailer) SendReminder(userID, to string, ev *bongov1.Event, start time.Time, loc *time.Location) error {
	if m.sender == nil {
		return fmt.Errorf("no sender")
	}
	locale := "en"
	if m.prefs != nil {
		if p := m.prefs.Get(userID); p != nil && p.Locale != "" {
			locale = p.Locale
		}
	}
	if loc == nil {
		loc = time.UTC
	}
	title := inviteName(ev.GetTitle())
	when := start.In(loc).Format("2006-01-02 15:04 MST")
	if ev.GetAllDay() {
		when = start.In(loc).Format("2006-01-02")
	}
	var body strings.Builder
	fmt.Fprintf(&body, i18n.T(locale, "calendar.reminderMailBody"), title, when)
	if l := inviteName(ev.GetLocation()); l != "" {
		fmt.Fprintf(&body, i18n.T(locale, "calendar.reminderMailLocation"), l)
	}
	subject := fmt.Sprintf(i18n.T(locale, "calendar.reminderMailSubject"), title)
	raw, err := buildPlainMailAuto(to, to, subject, body.String(), time.Now(), true)
	if err != nil {
		return fmt.Errorf("building reminder: %w", err)
	}
	notReached, err := m.sender.DeliverGenerated(userID, to, to, []string{to}, raw)
	if err != nil {
		return fmt.Errorf("delivering reminder: %w", err)
	}
	if len(notReached) > 0 {
		return fmt.Errorf("reminder not delivered")
	}
	return nil
}
