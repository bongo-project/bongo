package handlers

import (
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SetInvitations wires the calendar side of iMIP invitations. nil (the
// default) shows no invitation card and refuses the answer route.
func (h *MailHandler) SetInvitations(s invitationService) { h.invitations = s }

// HasInvitations reports whether invitations are wired.
func (h *MailHandler) HasInvitations() bool { return h.invitations != nil }

// maxInviteParts bounds how many parts of a message are looked at.
const maxInviteParts = 200

// findInvitePart returns the calendar part of a message and the method
// parameter of its Content-Type. The part inside multipart/alternative has no
// file name, so the part tree is walked rather than the attachment list. An
// inline text/calendar with a method parameter wins; an .ics attachment, or a
// text/calendar without one, is the fallback.
func findInvitePart(env *enmime.Envelope) (content []byte, ctMethod string, ok bool) {
	if env == nil || env.Root == nil {
		return nil, "", false
	}
	var fallback *enmime.Part
	stack := []*enmime.Part{env.Root}
	for seen := 0; len(stack) > 0 && seen < maxInviteParts; seen++ {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p.NextSibling != nil {
			stack = append(stack, p.NextSibling)
		}
		if p.FirstChild != nil {
			stack = append(stack, p.FirstChild)
			continue
		}
		ct := strings.ToLower(p.ContentType)
		method := contentTypeMethod(p)
		switch {
		case ct == "text/calendar" && method != "":
			return p.Content, method, true
		case fallback == nil && (ct == "text/calendar" || ct == "application/ics" ||
			strings.HasSuffix(strings.ToLower(p.FileName), ".ics")):
			fallback = p
		}
	}
	if fallback != nil {
		return fallback.Content, "", true
	}
	return nil, "", false
}

// contentTypeMethod reads the method parameter of the part's own Content-Type
// header: enmime keeps only the parameters it uses, and method is not one.
func contentTypeMethod(p *enmime.Part) string {
	_, params, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["method"])
}

// inviteFor assembles the calendar side's view of a message, or false when it
// carries no calendar part.
func (h *MailHandler) inviteFor(userID, email, mailbox string, uid uint32, env *enmime.Envelope, meta *bongov1.MailMeta) (inviteMessage, bool) {
	part, ctMethod, ok := findInvitePart(env)
	if !ok {
		return inviteMessage{}, false
	}
	from := env.GetHeader("From")
	if from == "" {
		from = meta.From
	}
	if a, err := mail.ParseAddress(from); err == nil {
		from = a.Address
	} else {
		from = ""
	}
	return inviteMessage{
		UserID: userID, Email: email, Mailbox: mailbox, UID: uid,
		Part: part, CTMethod: ctMethod, From: from,
		AuthResults: env.GetHeaderValues("Authentication-Results"),
	}, true
}

// invitationCard asks the calendar side for the card of the open message.
func (h *MailHandler) invitationCard(r *http.Request, userID, email, mailbox string, uid uint32, env *enmime.Envelope, meta *bongov1.MailMeta) *templates.InvitationCard {
	msg, ok := h.inviteFor(userID, email, mailbox, uid, env, meta)
	if !ok {
		return nil
	}
	return h.invitations.InvitationCard(r, msg)
}

// AnswerInvitation handles POST /mail/{mailbox}/{uid}/invitation: the user's
// answer to, update from or removal of the invitation in a message. The message
// is resolved for the session user and the calendar part read from it again, so
// the form supplies only the action, the answer and the calendar.
func (h *MailHandler) AnswerInvitation(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}
	if h.invitations == nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

	msg, _, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil || meta.RawMessage == nil || meta.RawMessage.Hash == "" || h.blobs == nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	raw, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	env, _, err := mimesafe.ReadEnvelope(raw)
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	im, ok := h.inviteFor(userID, email, mailbox, uint32(uid), env, meta)
	if !ok {
		http.Error(w, "No invitation in this message", http.StatusNotFound)
		return
	}

	res := h.invitations.ApplyInvitation(r, im, inviteAction{
		Action:     r.PostFormValue("action"),
		Partstat:   r.PostFormValue("partstat"),
		CalendarID: r.PostFormValue("calendar_id"),
	})
	switch {
	case res.Status == http.StatusInsufficientStorage:
		refuseQuota(w, r)
		return
	case res.Status != 0:
		http.Error(w, res.Message, res.Status)
		return
	}
	dest := fmt.Sprintf("/mail/%s/%d", url.PathEscape(mailbox), uid)
	if res.ReplyFailed {
		dest += "?ir=failed"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther) // #nosec G710 -- same-origin message path built from the parsed mailbox and UID
}
