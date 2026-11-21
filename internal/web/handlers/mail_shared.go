package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Shared folders on the web have their own narrow route tree,
// /shared/mail/{owner}/{folder}, and one resolver. The own-folder handlers in
// mail.go read the session user's keyspace everywhere and are left alone, so no
// own-folder path can be turned into a shared-folder path. {owner} is the
// owner's user id and {folder} is MailboxMeta.id: ids survive a rename and a
// name never has to be resolved from the URL.
//
// Every handler here asks resolveSharedFolder first, on every request, and
// caches nothing: a revoked or downgraded grant, a group the user left or a
// folder that was renamed or deleted takes effect on the next request. Rights
// are the owner's folder rights (RFC 4314) in the union ACLStore.MailboxRights
// computes. A shared \Seen or \Flagged is the owner's flag (Bongo keeps no
// per-user seen), so a grantee reading a message marks it read for the owner.

const (
	sharedNotFoundBody  = "folder not found"
	sharedForbiddenBody = "permission denied"
)

// sharedMsg is a shared message with its metadata, both read from the owner.
type sharedMsg struct {
	msg  *bongov1.MailboxMessage
	meta *bongov1.MailMeta
}

// sharedFolder is a resolved, authorised shared folder.
type sharedFolder struct {
	Owner      string
	ID         string
	Name       string
	Rights     string
	OwnerEmail string
}

func (f sharedFolder) view() templates.SharedFolderView {
	return templates.SharedFolderView{OwnerID: f.Owner, OwnerEmail: f.OwnerEmail, ID: f.ID, Name: f.Name, Rights: f.Rights}
}

func (f sharedFolder) baseURL() string { return templates.SharedFolderURL(f.Owner, f.ID) }

// SetACL wires the grant store the shared-folder routes authorise with. Without
// it every shared route answers as a missing folder.
func (h *MailHandler) SetACL(a *store.ACLStore) { h.acl = a }

// resolveSharedFolder authorises the session user for the folder named by the
// URL and returns it, or the status to answer with. need lists RFC 4314 letters
// the route requires on top of the lookup right ("" for none).
//
// Every failure before the lookup right is held answers 404 with one constant
// body: no grant, a pending or expired grant, a grant without "l", an unknown,
// disabled, resource or own owner, a malformed or unknown id and a folder that
// was deleted are indistinguishable, so the route is no oracle for which
// accounts or folders exist. Only a user who holds "l" can get 403, as with
// IMAP NO [NOPERM]. An unexpected store error is 500.
func (h *MailHandler) resolveSharedFolder(r *http.Request, need string) (sharedFolder, int) {
	userID, _ := middleware.UserFromContext(r.Context())
	owner := chi.URLParam(r, "owner")
	id := chi.URLParam(r, "folder")
	if h.acl == nil || h.mailboxes == nil || userID == "" || !validOwnerID(owner) || owner == userID || !store.ValidMailboxID(id) {
		return sharedFolder{}, http.StatusNotFound
	}
	email, err := h.acl.PrimaryAddressOf(owner)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return sharedFolder{}, http.StatusNotFound
	case err != nil:
		return sharedFolder{}, http.StatusInternalServerError
	}
	rights, err := h.acl.MailboxRights(owner, id, userID, time.Now())
	if err != nil {
		return sharedFolder{}, http.StatusInternalServerError
	}
	if !strings.ContainsRune(rights, 'l') {
		return sharedFolder{}, http.StatusNotFound
	}
	name, err := h.mailboxes.MailboxNameByID(owner, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return sharedFolder{}, http.StatusNotFound
	case err != nil:
		return sharedFolder{}, http.StatusInternalServerError
	}
	f := sharedFolder{Owner: owner, ID: id, Name: name, Rights: rights, OwnerEmail: email}
	for _, letter := range need {
		if !strings.ContainsRune(rights, letter) {
			return f, http.StatusForbidden
		}
	}
	return f, 0
}

func writeSharedStatus(w http.ResponseWriter, status int) {
	switch status {
	case http.StatusNotFound:
		http.Error(w, sharedNotFoundBody, status)
	case http.StatusForbidden:
		http.Error(w, sharedForbiddenBody, status)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// sharedFolderFor resolves and writes the refusal itself.
func (h *MailHandler) sharedFolderFor(w http.ResponseWriter, r *http.Request, need string) (sharedFolder, bool) {
	f, status := h.resolveSharedFolder(r, need)
	if status != 0 {
		writeSharedStatus(w, status)
		return sharedFolder{}, false
	}
	return f, true
}

// sharedMessageFor resolves the folder and the message named by the URL. A
// message that is not there is the same 404 as a folder that is not.
func (h *MailHandler) sharedMessageFor(w http.ResponseWriter, r *http.Request, need string) (sharedFolder, uint32, bool) {
	f, ok := h.sharedFolderFor(w, r, need)
	if !ok {
		return sharedFolder{}, 0, false
	}
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil {
		http.Error(w, sharedNotFoundBody, http.StatusNotFound)
		return sharedFolder{}, 0, false
	}
	return f, uint32(uid), true
}

// sharedMessage reads the message from the owner's folder and its metadata
// from the owner's keyspace.
func (h *MailHandler) sharedMessage(w http.ResponseWriter, f sharedFolder, uid uint32) (*sharedMsg, bool) {
	msg, err := h.mailboxes.GetMessage(f.Owner, f.Name, uid)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, sharedNotFoundBody, http.StatusNotFound)
		return nil, false
	case err != nil:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	meta := h.loadMailMeta(f.Owner, msg.MailGuid)
	if meta == nil {
		http.Error(w, sharedNotFoundBody, http.StatusNotFound)
		return nil, false
	}
	return &sharedMsg{msg: msg, meta: meta}, true
}

// SharedList lists a shared folder, newest first.
func (h *MailHandler) SharedList(w http.ResponseWriter, r *http.Request) {
	f, ok := h.sharedFolderFor(w, r, "r")
	if !ok {
		return
	}
	userID, email := middleware.UserFromContext(r.Context())

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage := defaultMessagesPerPage
	if h.prefs != nil {
		if up := h.prefs.Get(userID); up.MessagesPerPage > 0 {
			perPage = int(up.MessagesPerPage)
		}
	}
	msgs, total, err := h.mailboxes.ListMessagesPaged(f.Owner, f.Name, (page-1)*perPage, perPage)
	if err != nil {
		http.Error(w, "Error loading messages", http.StatusInternalServerError)
		return
	}
	summaries := make([]templates.MessageSummary, 0, len(msgs))
	for _, msg := range msgs {
		s := templates.MessageSummary{
			UID: msg.Uid, MailGUID: msg.MailGuid, Date: time.Unix(msg.InternalDate, 0),
			Seen: msg.Flags&store.FlagSeen != 0, Flagged: msg.Flags&store.FlagFlagged != 0,
		}
		if meta := h.loadMailMeta(f.Owner, msg.MailGuid); meta != nil {
			s.From, s.Subject = meta.From, meta.Subject
			s.HasAttachments = len(meta.Attachments) > 0
		}
		if s.Subject == "" {
			s.Subject = "(no subject)"
		}
		summaries = append(summaries, s)
	}
	_, _, unseen, err := h.mailboxes.CountMessages(f.Owner, f.Name)
	if err != nil {
		http.Error(w, "Error loading messages", http.StatusInternalServerError)
		return
	}
	totalPages := max((total+perPage-1)/perPage, 1)
	counts := templates.ListCounts{Unread: int(unseen), Total: total}

	r = r.WithContext(templates.WithSharedSelected(r.Context(), f.Owner, f.ID))
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.SharedMailboxContent(f.view(), counts, summaries, page, totalPages)).ServeHTTP(w, r)
		return
	}
	infos := h.loadMailboxInfos(userID, "")
	tags := h.loadSidebarTags(userID, "")
	templ.Handler(templates.SharedMailboxPage(f.view(), infos, tags, counts, summaries, page, totalPages, email)).ServeHTTP(w, r)
}

// SharedShow shows one message. It marks it \Seen only for a user who holds
// "s", and never backfills threading fields, which would write the owner's
// MailMeta.
func (h *MailHandler) SharedShow(w http.ResponseWriter, r *http.Request) {
	f, uid, ok := h.sharedMessageFor(w, r, "r")
	if !ok {
		return
	}
	sm, ok := h.sharedMessage(w, f, uid)
	if !ok {
		return
	}
	userID, email := middleware.UserFromContext(r.Context())
	seen := sm.msg.Flags&store.FlagSeen != 0
	if !seen && strings.ContainsRune(f.Rights, 's') {
		if err := h.mailboxes.AddFlags(f.Owner, f.Name, uid, store.FlagSeen); err != nil {
			h.logger.Warn("web: failed to auto-mark shared message seen", "owner", f.Owner, "folder", f.ID, "uid", uid, "user", userID, "error", err)
		} else {
			seen = true
		}
	}
	detail := templates.MessageDetail{
		UID: uid, Mailbox: f.Name, RealMailbox: f.Name,
		Subject: sm.meta.Subject, From: sm.meta.From, To: sm.meta.To,
		Date: time.Unix(sm.meta.ReceivedAt, 0),
		Seen: seen, Flagged: sm.msg.Flags&store.FlagFlagged != 0,
		AttachmentBase: f.baseURL(),
	}
	h.applyEnvelopeBody(&detail, sm.meta)
	detail.Attachments = slices.DeleteFunc(detail.Attachments, func(a templates.Attachment) bool { return a.URL == "" })
	// A hash would let the templates build /mail/blob links, which authorise
	// against the session user's own mail and are never valid here.
	for i := range detail.Attachments {
		detail.Attachments[i].Hash = ""
	}

	page := templates.SharedMessagePage{Folder: f.view(), Message: detail}
	r = r.WithContext(templates.WithSharedSelected(r.Context(), f.Owner, f.ID))
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.SharedMessageContent(page)).ServeHTTP(w, r)
		return
	}
	infos := h.loadMailboxInfos(userID, "")
	tags := h.loadSidebarTags(userID, "")
	templ.Handler(templates.SharedMessageFullPage(page, infos, tags, email)).ServeHTTP(w, r)
}

// SharedAttachment serves one attachment of a shared message by index.
func (h *MailHandler) SharedAttachment(w http.ResponseWriter, r *http.Request) {
	f, uid, ok := h.sharedMessageFor(w, r, "r")
	if !ok {
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 {
		http.Error(w, "Invalid attachment", http.StatusBadRequest)
		return
	}
	sm, ok := h.sharedMessage(w, f, uid)
	if !ok {
		return
	}
	h.writeAttachmentPart(w, f.Owner, sm.msg.MailGuid, idx, r.URL.Query().Get("inline") == "1")
}

// SharedExport downloads a shared message as .eml.
func (h *MailHandler) SharedExport(w http.ResponseWriter, r *http.Request) {
	f, uid, ok := h.sharedMessageFor(w, r, "r")
	if !ok {
		return
	}
	sm, ok := h.sharedMessage(w, f, uid)
	if !ok {
		return
	}
	h.writeEML(w, f.Owner, sm.msg.MailGuid)
}

// SharedSource shows the raw source of a shared message.
func (h *MailHandler) SharedSource(w http.ResponseWriter, r *http.Request) {
	f, uid, ok := h.sharedMessageFor(w, r, "r")
	if !ok {
		return
	}
	sm, ok := h.sharedMessage(w, f, uid)
	if !ok {
		return
	}
	if sm.meta.RawMessage == nil || sm.meta.RawMessage.Hash == "" || h.blobs == nil {
		http.Error(w, sharedNotFoundBody, http.StatusNotFound)
		return
	}
	raw, err := store.RawMessageBytes(h.blobs, sm.meta)
	if err != nil {
		http.Error(w, sharedNotFoundBody, http.StatusNotFound)
		return
	}
	src, truncated := truncateMessageSource(raw, messageSourceMaxBytes)
	_, email := middleware.UserFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	templ.Handler(templates.SharedSourceFullPage(f.view(), uid, sm.meta.Subject, src, truncated, email)).ServeHTTP(w, r)
}

// sharedFlagRights maps a flag to the right that changes it, as IMAP STORE
// does: \Seen needs s, \Deleted needs t and every other flag needs w.
var sharedFlagRights = map[string]struct {
	bit    uint32
	rights string
}{
	"seen":     {store.FlagSeen, "s"},
	"flagged":  {store.FlagFlagged, "w"},
	"answered": {store.FlagAnswered, "w"},
	"deleted":  {store.FlagDeleted, "t"},
}

// sharedRedirect sends the browser on after a change: HTMX gets HX-Redirect, a
// plain form a 303.
func sharedRedirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther) // #nosec G710 -- built from validated ids
}

// SharedFlags sets or clears one flag on a shared message. An unknown flag or
// action is 400 (stricter than ToggleFlags); the right is checked after the
// folder resolves and before anything is written.
func (h *MailHandler) SharedFlags(w http.ResponseWriter, r *http.Request) {
	f, uid, ok := h.sharedMessageFor(w, r, "")
	if !ok {
		return
	}
	spec, known := sharedFlagRights[r.FormValue("flag")]
	action := r.FormValue("action")
	if !known || (action != "add" && action != "remove") {
		http.Error(w, "Unknown flag or action", http.StatusBadRequest)
		return
	}
	if !strings.ContainsRune(f.Rights, rune(spec.rights[0])) {
		writeSharedStatus(w, http.StatusForbidden)
		return
	}
	if _, ok := h.sharedMessage(w, f, uid); !ok {
		return
	}
	var err error
	if action == "add" {
		err = h.mailboxes.AddFlags(f.Owner, f.Name, uid, spec.bit)
	} else {
		err = h.mailboxes.RemoveFlags(f.Owner, f.Name, uid, spec.bit)
	}
	if err != nil {
		http.Error(w, "Failed to update flag", http.StatusInternalServerError)
		return
	}
	userID, _ := middleware.UserFromContext(r.Context())
	h.logger.Info("web: shared flag changed", "flag", r.FormValue("flag"), "action", action, "owner", f.Owner, "folder", f.ID, "uid", uid, "user", userID)
	sharedRedirect(w, r, fmt.Sprintf("%s/%d", f.baseURL(), uid))
}

// SharedDelete deletes a shared message permanently. It needs "t" and "e", sets
// \Deleted and expunges the folder, which removes every message of the folder
// already marked \Deleted (an IMAP client's too), exactly as IMAP EXPUNGE
// would. There is no Trash move: it would need the right to insert into the
// owner's Trash.
func (h *MailHandler) SharedDelete(w http.ResponseWriter, r *http.Request) {
	f, uid, ok := h.sharedMessageFor(w, r, "te")
	if !ok {
		return
	}
	if _, ok := h.sharedMessage(w, f, uid); !ok {
		return
	}
	if err := h.mailboxes.AddFlags(f.Owner, f.Name, uid, store.FlagDeleted); err != nil {
		http.Error(w, "failed to mark message deleted", http.StatusInternalServerError)
		return
	}
	if _, err := h.mailboxes.Expunge(f.Owner, f.Name); err != nil {
		http.Error(w, "failed to delete message", http.StatusInternalServerError)
		return
	}
	userID, _ := middleware.UserFromContext(r.Context())
	h.logger.Info("web: shared message permanently deleted", "owner", f.Owner, "folder", f.ID, "uid", uid, "user", userID)
	sharedRedirect(w, r, f.baseURL())
}

// sharedMailSidebar builds the "Shared with me" section for one user. It reads
// accepted grants that carry the lookup right, drops an owner that no longer
// resolves (disabled, resource, missing) and a folder that is gone, and counts
// unread only where the user holds the read right. It is computed for the
// request and never cached. It costs one count scan per shared folder, the
// same as the own-folder sidebar.
func sharedMailSidebar(acl *store.ACLStore, mailboxes *store.MailboxStore, userID string, now time.Time) templates.SharedSidebarData {
	var data templates.SharedSidebarData
	held, err := acl.MailboxesSharedWith(userID, now)
	if err != nil {
		slog.Warn("web: shared sidebar: listing shared folders failed", "user", userID, "error", err)
	}
	labels := map[string]string{}
	for _, h := range held {
		label, seen := labels[h.Owner]
		if !seen {
			label, err = acl.PrimaryAddressOf(h.Owner)
			if err != nil {
				label = ""
			}
			labels[h.Owner] = label
		}
		if label == "" {
			continue
		}
		name, err := mailboxes.MailboxNameByID(h.Owner, h.Resource)
		if err != nil {
			continue
		}
		f := templates.SharedMailFolder{OwnerLabel: label, OwnerID: h.Owner, ID: h.Resource, Name: name}
		if strings.ContainsRune(h.Rights, 'r') {
			f.Counted = true
			f.Unread = mailboxes.UnreadCount(h.Owner, name)
		}
		data.Folders = append(data.Folders, f)
	}
	slices.SortFunc(data.Folders, func(a, b templates.SharedMailFolder) int {
		return strings.Compare(a.OwnerLabel+"\x00"+a.Name+"\x00"+a.ID, b.OwnerLabel+"\x00"+b.Name+"\x00"+b.ID)
	})
	if n, err := acl.CountPendingGrantsHeld(userID, now); err == nil {
		data.PendingInvitations = n
	}
	return data
}

// SharedMailSidebar is middleware that gives every page of the session a lazy
// loader for the sidebar's "Shared with me" section. The section is computed
// when a sidebar renders, once per request, and no part of it outlives the
// request.
func SharedMailSidebar(acl *store.ACLStore, mailboxes *store.MailboxStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, _ := middleware.UserFromContext(r.Context())
			if userID == "" || acl == nil || mailboxes == nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := templates.WithSharedSidebar(r.Context(), func() templates.SharedSidebarData {
				return sharedMailSidebar(acl, mailboxes, userID, time.Now())
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
