package handlers

import (
	"log/slog"
	"net/http"

	"github.com/bongo-project/bongo/internal/store"
)

// DanglingMailHandler serves the dangling-mail control-plane endpoints: mail
// metadata whose body blob is missing from the blob store. Mounted on
// /internal/ behind RequireInternalAuth, like the queue and address-repair
// handlers -- Purge destroys metadata and must never be reachable publicly.
type DanglingMailHandler struct {
	pebble  *store.PebbleStore
	blobs   *store.BlobStore
	applier store.RaftApplier
	logger  *slog.Logger
}

func NewDanglingMailHandler(pebble *store.PebbleStore, blobs *store.BlobStore, applier store.RaftApplier, logger *slog.Logger) *DanglingMailHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DanglingMailHandler{pebble: pebble, blobs: blobs, applier: applier, logger: logger}
}

// List reports mail metadata whose body blob is gone. Read-only: this is the
// dry run, and the mailbox references it returns are what an operator needs to
// tell debris from a message still sitting in a live mailbox.
func (h *DanglingMailHandler) List(w http.ResponseWriter, r *http.Request) {
	records, err := store.FindDanglingMail(h.pebble, h.blobs)
	if err != nil {
		h.logger.Error("dangling mail: scan failed", "error", err)
		http.Error(w, "failed to scan mail metadata", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(records), "dangling": records})
}

// Purge deletes the dangling records and reports what it removed.
//
// The scan is redone here rather than accepting a list from the client: the
// caller's view can be minutes stale, and the set to destroy is decided from
// current state only. Records whose body reappeared between the scan and the
// delete are reported as skipped.
func (h *DanglingMailHandler) Purge(w http.ResponseWriter, r *http.Request) {
	records, err := store.FindDanglingMail(h.pebble, h.blobs)
	if err != nil {
		h.logger.Error("dangling mail: scan failed", "error", err)
		http.Error(w, "failed to scan mail metadata", http.StatusInternalServerError)
		return
	}

	deleted, err := store.DeleteDanglingMail(h.pebble, h.blobs, h.applier, records)
	deletedSet := make(map[string]bool, len(deleted))
	for _, guid := range deleted {
		deletedSet[guid] = true
	}
	for _, rec := range records {
		if deletedSet[rec.GUID] {
			h.logger.Warn("dangling mail: purged metadata for a message whose body was lost",
				"user", rec.UserID, "guid", rec.GUID, "hash", rec.MissingHash, "mailboxes", len(rec.Mailboxes))
		}
	}

	// Checked before the skipped set is built: on an error the delete stopped
	// early, so the untouched remainder was not skipped for the "body came
	// back" reason and must not be reported as if it were.
	if err != nil {
		h.logger.Error("dangling mail: purge failed", "deleted", len(deleted), "error", err)
		http.Error(w, "failed to delete dangling mail metadata", http.StatusInternalServerError)
		return
	}

	var skipped []string
	for _, rec := range records {
		if !deletedSet[rec.GUID] {
			skipped = append(skipped, rec.UserID+"/"+rec.GUID)
		}
	}
	if len(skipped) > 0 {
		h.logger.Info("dangling mail: skipped records whose body blob was present again", "count", len(skipped))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"found":   len(records),
		"count":   len(deleted),
		"deleted": deleted,
		"skipped": skipped,
	})
}
