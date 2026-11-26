package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// Folder management is the owner's side of Settings > Folders: create, rename
// and delete own folders. Every route takes the folder from the session user's
// own keyspace, never from an owner parameter, and every refusal is fixed
// translated text that does not echo the posted name.
//
// Decided behaviour:
//   - A name is one full path with "/" as the delimiter. The parent need not
//     exist, as with IMAP CREATE.
//   - Renaming moves only that folder. A subfolder "A/x" keeps its name when "A"
//     becomes "B" (as IMAP RENAME here): moving subfolders would be one command
//     each, not atomic, and a composite command would diverge older nodes.
//   - Deleting removes only that folder and its messages. Subfolders stay.
//   - The standard folders and Archive can be neither renamed nor deleted.
//
// The "already exists" answers come from a pre-check, which is check-then-act
// like IMAP: a racing create or rename surfaces as a 500, and the store still
// refuses to overwrite.

// folderNameError answers a name the shared validator refused.
func folderNameError(w http.ResponseWriter, r *http.Request, err error) {
	msg := "folders.errInvalid"
	switch {
	case errors.Is(err, store.ErrMailboxNameEmpty):
		msg = "folders.errEmpty"
	case errors.Is(err, store.ErrMailboxNameTooLong):
		msg = "folders.errTooLong"
	case errors.Is(err, store.ErrMailboxNameReserved):
		msg = "folders.errReserved"
	}
	folderError(w, r, msg, http.StatusBadRequest)
}

// ownFolder resolves the form's folder name to one of the session user's own
// folders. It writes the refusal and returns ok=false when the folder is not
// theirs to manage: an empty or reserved-prefix name and an unknown name are
// 404, a protected folder 403.
func (h *FolderShareHandler) ownFolder(w http.ResponseWriter, r *http.Request, userID, name string) bool {
	if h.mailboxes == nil {
		folderError(w, r, "folders.errUnavailable", http.StatusServiceUnavailable)
		return false
	}
	if name == "" || h.namespaces.OrDefaults().Reserves(name) {
		folderError(w, r, "folders.errNotFound", http.StatusNotFound)
		return false
	}
	if store.ProtectedMailbox(name) {
		folderError(w, r, "folders.errProtected", http.StatusForbidden)
		return false
	}
	if _, err := h.mailboxes.GetMailbox(userID, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			folderError(w, r, "folders.errNotFound", http.StatusNotFound)
		} else {
			slog.Warn("folders: reading folder failed", "user", userID, "folder", name, "error", err)
			folderError(w, r, "folders.errUnavailable", http.StatusInternalServerError)
		}
		return false
	}
	return true
}

// nameTaken reports whether a folder called name exists for the user. INBOX
// counts in any case. On a read error it writes the 500 and returns done=true.
func (h *FolderShareHandler) nameTaken(w http.ResponseWriter, r *http.Request, userID, name string) (taken, done bool) {
	if strings.EqualFold(name, "INBOX") {
		return true, false
	}
	switch _, err := h.mailboxes.GetMailbox(userID, name); {
	case err == nil:
		return true, false
	case errors.Is(err, store.ErrNotFound):
		return false, false
	default:
		slog.Warn("folders: reading folder failed", "user", userID, "folder", name, "error", err)
		folderError(w, r, "folders.errUnavailable", http.StatusInternalServerError)
		return false, true
	}
}

// CreateFolder creates an own folder.
//
// POST /settings/folders/create
func (h *FolderShareHandler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if h.mailboxes == nil {
		folderError(w, r, "folders.errUnavailable", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if err := store.ValidateMailboxName(h.namespaces, name); err != nil {
		folderNameError(w, r, err)
		return
	}
	if taken, done := h.nameTaken(w, r, userID, name); done {
		return
	} else if taken {
		folderError(w, r, "folders.errExists", http.StatusConflict)
		return
	}
	if err := h.mailboxes.CreateMailbox(userID, name); err != nil {
		slog.Warn("folders: create failed", "user", userID, "folder", name, "error", err)
		folderError(w, r, "folders.errCreateFailed", http.StatusInternalServerError)
		return
	}
	redirectAfterAction(w, r, withFlash("/settings/folders", flashFolderCreated))
}

// RenameFolder renames an own folder. The folder keeps its id, so grants and
// shared subscriptions follow it. Subfolders are not moved.
//
// POST /settings/folders/rename
func (h *FolderShareHandler) RenameFolder(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	old := r.FormValue("folder")
	if !h.ownFolder(w, r, userID, old) {
		return
	}
	newName := strings.TrimSpace(r.FormValue("new_name"))
	if err := store.ValidateMailboxName(h.namespaces, newName); err != nil {
		folderNameError(w, r, err)
		return
	}
	if newName == old {
		folderError(w, r, "folders.errSameName", http.StatusBadRequest)
		return
	}
	if taken, done := h.nameTaken(w, r, userID, newName); done {
		return
	} else if taken {
		folderError(w, r, "folders.errExists", http.StatusConflict)
		return
	}
	if err := h.mailboxes.RenameMailbox(userID, old, newName); err != nil {
		slog.Warn("folders: rename failed", "user", userID, "folder", old, "error", err)
		folderError(w, r, "folders.errRenameFailed", http.StatusInternalServerError)
		return
	}
	redirectAfterAction(w, r, withFlash("/settings/folders", flashFolderRenamed))
}

// ShowDeleteFolder is the confirmation page: the folder's name, how many
// messages it holds and whether it is shared. It writes nothing. The count is
// a prefix scan, which is why it runs here for one folder and not on the list.
//
// GET /settings/folders/delete?folder=
func (h *FolderShareHandler) ShowDeleteFolder(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	name := r.URL.Query().Get("folder")
	if !h.ownFolder(w, r, userID, name) {
		return
	}
	total, _, _, err := h.mailboxes.CountMessages(userID, name)
	if err != nil {
		slog.Warn("folders: counting messages failed", "user", userID, "folder", name, "error", err)
		folderError(w, r, "folders.errUnavailable", http.StatusInternalServerError)
		return
	}
	shared := false
	if h.acl != nil {
		meta, err := h.mailboxes.GetMailbox(userID, name)
		if err == nil && meta.Id != "" {
			grants, err := h.acl.ListResourceGrants(store.ACLKindMailbox, userID, meta.Id)
			if err != nil {
				slog.Warn("folders: reading grants failed", "user", userID, "folder", name, "error", err)
				folderError(w, r, "folders.errUnavailable", http.StatusInternalServerError)
				return
			}
			shared = len(grants) > 0
		}
	}
	data := templates.FolderDeletePageData{Name: name, Messages: int(total), Shared: shared, Email: email}
	templ.Handler(templates.FolderDeletePage(data)).ServeHTTP(w, r)
}

// DeleteFolder deletes an own folder and its messages. Subfolders stay. The
// delete and the grant revoke are separate commands (see DeleteMailbox): a
// revoke failure is logged and the delete is reported as done, because the
// folder is gone and the leftover grants are inert until the orphan sweep.
// Blobs and mail records are never touched here: the sweep and the blob GC own
// them.
//
// POST /settings/folders/delete
func (h *FolderShareHandler) DeleteFolder(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	name := r.FormValue("folder")
	if !h.ownFolder(w, r, userID, name) {
		return
	}
	if err := h.mailboxes.DeleteMailbox(userID, name); err != nil {
		switch {
		case errors.Is(err, store.ErrMailboxGrantsNotRevoked):
			slog.Warn("folders: folder deleted but its grants were not revoked", "user", userID, "folder", name, "error", err)
		case errors.Is(err, store.ErrNotFound):
			// A concurrent rename or delete took the name first.
			folderError(w, r, "folders.errNotFound", http.StatusNotFound)
			return
		default:
			slog.Warn("folders: delete failed", "user", userID, "folder", name, "error", err)
			folderError(w, r, "folders.errDeleteFailed", http.StatusInternalServerError)
			return
		}
	}
	redirectAfterAction(w, r, withFlash("/settings/folders", flashFolderDeleted))
}
