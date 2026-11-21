package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	gomessage "github.com/emersion/go-message/mail"

	"github.com/bongo-project/bongo/internal/i18n"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Sharing by email. A share of a calendar, folder or address book creates a pending grant and
// mails the grantee a link to /shares, where they accept or decline.
// Only an accepted grant authorises anything; the grant is authoritative and
// the email is best-effort.

// shareInviteQueueSize bounds the invitations waiting for the sender. A full
// buffer drops the invitation (logged); the grant stands, and the grantee
// still finds it on /shares.
const shareInviteQueueSize = 64

// maxInviteNameRunes bounds the owner and calendar names put into a message.
const maxInviteNameRunes = 100

// localeSource reads a user's stored preferences.
type localeSource interface {
	Get(userID string) *bongov1.UserPreferences
}

// shareInviteJob is one invitation to build and deliver. It carries no
// request, which is cancelled once the response is sent.
type shareInviteJob struct {
	ownerID      string
	ownerEmail   string
	granteeID    string
	recipient    string
	kind         string
	resourceName string
	// host is the already-checked host of the link in the message.
	host string
}

// ShareInviter delivers share invitation emails off the request path.
type ShareInviter struct {
	sender inviteSender
	prefs  localeSource
	logger *slog.Logger

	// jobs is never closed, so a late Submit cannot panic; pending counts jobs
	// submitted but not yet finished.
	jobs    chan shareInviteJob
	pending atomic.Int64
}

// NewShareInviter creates an inviter. sender is the compose handler's
// DeliverGenerated; prefs supplies the recipient's stored locale and may be
// nil (English).
func NewShareInviter(sender inviteSender, prefs localeSource, logger *slog.Logger) *ShareInviter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShareInviter{sender: sender, prefs: prefs, logger: logger, jobs: make(chan shareInviteJob, shareInviteQueueSize)}
}

// Submit hands a job to the background sender without blocking. It reports
// false when the buffer is full.
func (s *ShareInviter) Submit(j shareInviteJob) bool {
	s.pending.Add(1)
	select {
	case s.jobs <- j:
		return true
	default:
		s.pending.Add(-1)
		return false
	}
}

// Run sends queued invitations until ctx is cancelled, then drains what is
// still buffered and returns. The caller must cancel ctx only after the HTTP
// server has shut down (and must not derive it from a context that is cancelled
// earlier, such as the signal context), so every in-flight submit is already in
// the buffer when the drain starts.
func (s *ShareInviter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case j := <-s.jobs:
					s.process(j)
				default:
					return
				}
			}
		case j := <-s.jobs:
			s.process(j)
		}
	}
}

func (s *ShareInviter) process(j shareInviteJob) {
	defer s.pending.Add(-1)
	s.send(j)
}

// idle reports whether every submitted job has finished.
func (s *ShareInviter) idle() bool { return s.pending.Load() == 0 }

func (s *ShareInviter) locale(userID string) string {
	if s.prefs == nil {
		return "en"
	}
	if p := s.prefs.Get(userID); p != nil && p.Locale != "" {
		return p.Locale
	}
	return "en"
}

// send builds and delivers one invitation. A failure is logged with the user
// and owner IDs only: never the address, the link or the body.
func (s *ShareInviter) send(j shareInviteJob) {
	if s.sender == nil {
		s.logger.Warn("calendar-share: invitation not sent: no sender", "owner", j.ownerID, "grantee", j.granteeID)
		return
	}
	locale := s.locale(j.granteeID)
	owner := inviteName(j.ownerEmail)
	name := inviteName(j.resourceName)
	link := "https://" + j.host + "/shares"
	subjectKey, bodyKey := inviteKeys(j.kind)
	subject := fmt.Sprintf(i18n.T(locale, subjectKey), owner, name)
	body := fmt.Sprintf(i18n.T(locale, bodyKey), owner, name, link)
	raw, err := buildPlainMail(j.ownerEmail, j.recipient, subject, body, time.Now())
	if err != nil {
		s.logger.Warn("calendar-share: invitation not sent: building message failed", "owner", j.ownerID, "grantee", j.granteeID, "error", err)
		return
	}
	notReached, err := s.sender.DeliverGenerated(j.ownerID, j.ownerEmail, j.ownerEmail, []string{j.recipient}, raw)
	if err != nil || len(notReached) > 0 {
		s.logger.Warn("calendar-share: invitation not delivered", "owner", j.ownerID, "grantee", j.granteeID, "error", err)
	}
}

// inviteName makes a user-controlled name safe for a header or a body line:
// control characters become spaces and the length is bounded.
func inviteName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if r := []rune(s); len(r) > maxInviteNameRunes {
		s = string(r[:maxInviteNameRunes])
	}
	return s
}

// buildPlainMail returns a single-part text/plain message. Header values are
// encoded by go-message after control characters are removed, so a name cannot
// add a header.
func buildPlainMail(from, to, subject, body string, now time.Time) ([]byte, error) {
	return buildPlainMailAuto(from, to, subject, body, now, false)
}

// buildPlainMailAuto is buildPlainMail with an optional
// "Auto-Submitted: auto-generated" header, so vacation replies and other
// auto-responders do not answer the message.
func buildPlainMailAuto(from, to, subject, body string, now time.Time, autoSubmitted bool) ([]byte, error) {
	if !validMailAddress(from) || !validMailAddress(to) {
		return nil, fmt.Errorf("invalid address")
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("message id: %w", err)
	}
	domain := from[strings.LastIndex(from, "@")+1:]

	var h gomessage.Header
	h.Set("MIME-Version", "1.0")
	h.SetDate(now)
	h.SetMessageID(hex.EncodeToString(id[:]) + "@" + domain)
	h.SetSubject(inviteName(subject))
	h.SetAddressList("From", []*gomessage.Address{{Address: from}})
	h.SetAddressList("To", []*gomessage.Address{{Address: to}})
	h.Set("Content-Type", "text/plain; charset=UTF-8")
	if autoSubmitted {
		h.Set("Auto-Submitted", "auto-generated")
	}

	var out bytes.Buffer
	w, err := gomessage.CreateSingleInlineWriter(&out, h)
	if err != nil {
		return nil, fmt.Errorf("writing message: %w", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("writing message: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("writing message: %w", err)
	}
	return out.Bytes(), nil
}

// validMailAddress accepts a bare addr-spec with no control characters,
// whitespace, angle brackets or quotes.
func validMailAddress(a string) bool {
	at := strings.LastIndex(a, "@")
	if at <= 0 || at == len(a)-1 {
		return false
	}
	return !strings.ContainsFunc(a, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("<>\"(),;:\\", r)
	})
}

// SetShareInviter wires the sender of share invitation emails. nil (the
// default) sends none: the grant is still created.
func (h *CalendarHandler) SetShareInviter(s *ShareInviter) { h.inviter = s }

// ShareInviter returns the wired inviter, or nil.
func (h *CalendarHandler) ShareInviter() *ShareInviter { return h.inviter }

// linkHost returns the host to put in a link the server emails; see shareLinkHost.
func (h *CalendarHandler) linkHost(r *http.Request) string {
	return shareLinkHost(h.authservID, r)
}

// shareLinkHost returns the host to put in a link the server emails. The
// configured hostname wins: r.Host is chosen by the sender of the request, and
// a link in a message the server delivers must not point at a host the owner
// picked. Only when no hostname is configured (a dev server) does the request
// host stand in.
func shareLinkHost(configured string, r *http.Request) string {
	if validLinkHost(configured) {
		return configured
	}
	if configured == "" && validLinkHost(r.Host) {
		return r.Host
	}
	return ""
}

// validLinkHost accepts a host name or address with an optional port.
func validLinkHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == ':', r == '[', r == ']':
		default:
			return false
		}
	}
	return true
}
