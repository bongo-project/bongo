package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// The summary page: unread mail to me, starred mail, invitations awaiting an
// answer and what is coming up in the next seven days, each at most
// summaryLimit rows with a link to its full view.
//
// Each section has its own source behind a small interface, and one that fails
// shows an error in its place while the others still render. The sections run
// one after another: a request that reads a few small lists is quick, and the
// sources share request state (withShared returns a new request).

const (
	// summaryLimit is the most rows one section shows.
	summaryLimit = 10
	// summaryUpcomingDays is the length of the upcoming section.
	summaryUpcomingDays = 7
)

// summaryMail lists the two mail sections. Rows are newest first and carry
// the real mailbox of each message in their links.
type summaryMail interface {
	UnreadToMe(userID string, limit int) ([]templates.SummaryRow, error)
	Starred(userID string, limit int) ([]templates.SummaryRow, error)
}

// summaryInvitations lists the invitations the user has not answered.
type summaryInvitations interface {
	Awaiting(userID, email string, limit int) ([]templates.SummaryRow, error)
}

// summaryUpcoming lists events and due tasks in the next days. It returns the
// request it was given, possibly carrying context the rows need to render.
type summaryUpcoming interface {
	Upcoming(r *http.Request, userID, email string, days, limit int) (*http.Request, []templates.SummaryRow, error)
}

// SummaryHandler serves the summary page.
type SummaryHandler struct {
	mail        summaryMail
	invitations summaryInvitations
	upcoming    summaryUpcoming
	// mailboxes and tags fill the sidebar. nil leaves it empty.
	mailboxes *store.MailboxStore
	tags      *store.TagStore
	logger    *slog.Logger
}

// NewSummaryHandler creates a SummaryHandler with no sources: every section
// then renders its empty state.
func NewSummaryHandler(logger *slog.Logger) *SummaryHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SummaryHandler{logger: logger}
}

// SetMailSource wires the mail sections.
func (h *SummaryHandler) SetMailSource(m summaryMail) { h.mail = m }

// SetInvitations wires the invitations section.
func (h *SummaryHandler) SetInvitations(i summaryInvitations) { h.invitations = i }

// SetUpcoming wires the upcoming section.
func (h *SummaryHandler) SetUpcoming(u summaryUpcoming) { h.upcoming = u }

// SetSidebar wires the stores the sidebar is drawn from.
func (h *SummaryHandler) SetSidebar(mailboxes *store.MailboxStore, tags *store.TagStore) {
	h.mailboxes = mailboxes
	h.tags = tags
}

// section runs one section's source. A failure is logged with its context and
// shown as a generic message: the error text can carry paths or keys.
func (h *SummaryHandler) section(userID, name, more string, load func() ([]templates.SummaryRow, error)) templates.SummarySection {
	sec := templates.SummarySection{MoreHref: more}
	rows, err := load()
	if err != nil {
		h.logger.Warn("web: loading a summary section", "section", name, "user", userID, "error", err)
		sec.Err = true
		return sec
	}
	if len(rows) > summaryLimit {
		rows = rows[:summaryLimit]
	}
	sec.Items = rows
	return sec
}

// Summary renders the page. GET /summary
func (h *SummaryHandler) Summary(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	var data templates.SummaryData
	data.Warning = recoveryWarning(r)
	data.ToMe = h.section(userID, "to-me", "/mail/view/to-me?unread=1", func() ([]templates.SummaryRow, error) {
		if h.mail == nil {
			return nil, nil
		}
		return h.mail.UnreadToMe(userID, summaryLimit)
	})
	data.Starred = h.section(userID, "starred", "/mail/starred", func() ([]templates.SummaryRow, error) {
		if h.mail == nil {
			return nil, nil
		}
		return h.mail.Starred(userID, summaryLimit)
	})
	// No view lists invitations on their own, so the link goes to the INBOX
	// where the message with its card is.
	data.Invitations = h.section(userID, "invitations", "/mail/INBOX", func() ([]templates.SummaryRow, error) {
		if h.invitations == nil {
			return nil, nil
		}
		return h.invitations.Awaiting(userID, email, summaryLimit)
	})
	data.Invitations.MoreKey = "summary.openInbox"
	data.Upcoming = h.section(userID, "upcoming", "/calendar/agenda", func() ([]templates.SummaryRow, error) {
		if h.upcoming == nil {
			return nil, nil
		}
		req, rows, err := h.upcoming.Upcoming(r, userID, email, summaryUpcomingDays, summaryLimit)
		if req != nil {
			r = req
		}
		return rows, err
	})

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.SummaryContent(data)).ServeHTTP(w, r)
		return
	}
	var infos []templates.MailboxInfo
	if h.mailboxes != nil {
		infos = LoadMailboxInfos(h.mailboxes, userID, "summary")
	}
	templ.Handler(templates.SummaryPage(infos, LoadSidebarTags(h.tags, userID, "summary"), data, email)).ServeHTTP(w, r)
}

// recoveryWarningRe matches the only warning the login flow sends here.
var recoveryWarningRe = regexp.MustCompile(`^Recovery code used\. You have (\d{1,3}) code\(s\) remaining\.$`)

// recoveryWarning turns the ?warning of a recovery-code sign-in into the
// message in the request's language. Any other value is dropped: the query
// string is not trusted, and showing it as written would let a crafted link
// put arbitrary text in a warning banner.
func recoveryWarning(r *http.Request) string {
	m := recoveryWarningRe.FindStringSubmatch(r.URL.Query().Get("warning"))
	if m == nil {
		return ""
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return ""
	}
	return fmt.Sprintf(i18n.T(middleware.LocaleFromContext(r.Context()), "summary.recoveryUsed"), n)
}
