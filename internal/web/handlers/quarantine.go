package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	quarantineDir = "quarantine"
	incomingDir   = "incoming"
)

// QuarantineHandler handles admin quarantine management endpoints.
type QuarantineHandler struct {
	spool  *pipeline.SpoolStore
	blobs  *store.BlobStore
	logger *slog.Logger
}

// NewQuarantineHandler creates a QuarantineHandler.
func NewQuarantineHandler(spool *pipeline.SpoolStore, blobs *store.BlobStore, logger *slog.Logger) *QuarantineHandler {
	return &QuarantineHandler{
		spool:  spool,
		blobs:  blobs,
		logger: logger,
	}
}

// ListQuarantine renders the quarantine management page.
func (h *QuarantineHandler) ListQuarantine(w http.ResponseWriter, r *http.Request) {
	_, email := middleware.UserFromContext(r.Context())
	flash := r.URL.Query().Get("flash")

	msgs := h.loadQuarantine()
	templ.Handler(templates.AdminQuarantine(msgs, email, flash)).ServeHTTP(w, r)
}

// ReleaseMessage moves a message from quarantine/ to incoming/ to be delivered.
// Unlike rescan, release does NOT clear the scanning checked flags or
// re-run scanning stages. The admin has reviewed the message and overridden
// the verdict. The message is re-queued to be delivered without re-scanning.
func (h *QuarantineHandler) ReleaseMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if h.spool == nil {
		http.Error(w, "spool not available", http.StatusServiceUnavailable)
		return
	}

	// Checked here as well as inside requeueFromQuarantine so a malformed id
	// is reported as the client error it is, rather than as a server failure.
	if err := validateSpoolID(id); err != nil {
		h.logger.Warn("quarantine: release rejected invalid id", "id", id, "error", err)
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}

	if err := h.releaseMessageImpl(id); err != nil {
		h.logger.Error("quarantine: release failed", "id", id, "error", err)
		http.Error(w, "failed to release message", http.StatusInternalServerError)
		return
	}

	h.logger.Info("quarantine: message released", "id", id)
	http.Redirect(w, r, "/admin/quarantine?flash=Message+released", http.StatusSeeOther)
}

// DeleteMessage removes the envelope from quarantine/ and optionally the blob.
func (h *QuarantineHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if h.spool == nil {
		http.Error(w, "spool not available", http.StatusServiceUnavailable)
		return
	}

	// The id reaches spool.Remove, which joins it onto a directory path, so it
	// must be checked here for the same reason the requeue paths check it.
	if err := validateSpoolID(id); err != nil {
		h.logger.Warn("quarantine: delete rejected invalid id", "id", id, "error", err)
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}

	if removeErr := h.spool.Remove(quarantineDir, id); removeErr != nil {
		h.logger.Error("quarantine: delete envelope failed", "id", id, "error", removeErr)
		http.Error(w, "failed to delete message", http.StatusInternalServerError)
		return
	}

	// The blob is deliberately NOT deleted here. The blob store is
	// content-addressed and shared across all users (see CLAUDE.md), so the
	// hash for this message is the same hash as any byte-identical copy
	// delivered to somebody else -- and for quarantined spam, which is sent to
	// many recipients at once, that is the common case rather than the corner
	// case. Deleting it here destroys those other users' mail bodies while
	// their metadata survives, leaving them an unreadable message.
	//
	// Reclamation belongs to the blob GC, which is the only component that
	// knows the full reference set (Pebble mail, the outbound queue, and the
	// spool). Removing the envelope above already dropped this message's
	// reference; the GC sweeps the blob once nothing else points at it. That
	// is exactly how ordinary mail deletion behaves -- it never deletes blobs
	// either -- so this is the consistent path, not a special case.
	//
	// With storage.blob_gc unset ("off") nothing is reclaimed, which is the
	// operator having opted out of reclamation entirely; it is not a licence
	// to delete a shared blob from here.

	h.logger.Info("quarantine: message deleted", "id", id)
	http.Redirect(w, r, "/admin/quarantine?flash=Message+deleted", http.StatusSeeOther)
}

// BulkAction processes a bulk release or delete of selected message IDs.
func (h *QuarantineHandler) BulkAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")
	ids := r.Form["ids"]

	if h.spool == nil {
		http.Error(w, "spool not available", http.StatusServiceUnavailable)
		return
	}

	if len(ids) == 0 {
		http.Redirect(w, r, "/admin/quarantine?flash=No+messages+selected", http.StatusSeeOther)
		return
	}

	var succeeded, failed int
	for _, id := range ids {
		switch action {
		case "release":
			if err := h.releaseMessageImpl(id); err != nil {
				h.logger.Error("quarantine: bulk release failed", "id", id, "error", err)
				failed++
				continue
			}
			succeeded++
		case "delete":
			// Same guard as the single-message delete route: the id is
			// user-supplied and reaches spool.Remove's path join.
			if err := validateSpoolID(id); err != nil {
				h.logger.Warn("quarantine: bulk delete rejected invalid id", "id", id, "error", err)
				failed++
				continue
			}
			if err := h.spool.Remove(quarantineDir, id); err != nil {
				h.logger.Error("quarantine: bulk delete failed", "id", id, "error", err)
				failed++
				continue
			}
			// Blob left to the GC: it is shared and content-addressed, so
			// deleting it here can destroy a byte-identical message held by
			// another user. See the note in the single-delete handler.
			succeeded++
		case "rescan":
			// Requeue quarantine/ -> incoming/ so the message runs back through
			// the full pipeline. Unlike release, this must clear the scanning
			// stages' checked flags (via pipeline.PrepareForRescan) before the
			// move, or the runner's idempotency check (runner.go) skips the
			// stage that quarantined it in the first place — see the doc
			// comment on PrepareForRescan for the full rationale.
			if err := h.requeueFromQuarantine(id, pipeline.PrepareForRescan); err != nil {
				h.logger.Error("quarantine: bulk rescan failed", "id", id, "error", err)
				failed++
				continue
			}
			succeeded++
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
			return
		}
	}

	h.logger.Info("quarantine: bulk action complete",
		"action", action,
		"succeeded", succeeded,
		"failed", failed,
	)

	flash := fmt.Sprintf("%d+message(s)+%s", succeeded, bulkActionPastTense(action))
	if failed > 0 {
		flash += fmt.Sprintf("+(%d+failed)", failed)
	}
	http.Redirect(w, r, "/admin/quarantine?flash="+flash, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
}

// bulkActionPastTense maps a bulk action name to its past-tense flash text.
// action has already passed the BulkAction switch by the time this is
// called, so the set of inputs is closed to "release", "delete", "rescan".
func bulkActionPastTense(action string) string {
	switch action {
	case "release":
		return "released"
	case "delete":
		return "deleted"
	case "rescan":
		return "rescanned"
	default:
		// Fixed fallback rather than echoing the input: this string is
		// interpolated into the redirect Location header, so a future fourth
		// case added to the BulkAction switch without updating this mapper
		// must not put unvalidated form input there.
		return "processed"
	}
}

// loadQuarantine reads all envelopes from spool/quarantine/ and extracts display info.
func (h *QuarantineHandler) loadQuarantine() []templates.QuarantineEntry {
	if h.spool == nil {
		return nil
	}

	envs, err := h.spool.List(quarantineDir)
	if err != nil {
		h.logger.Error("quarantine: list failed", "error", err)
		return nil
	}

	entries := make([]templates.QuarantineEntry, 0, len(envs))
	for _, env := range envs {
		entries = append(entries, h.envToEntry(env))
	}
	return entries
}

// envToEntry converts a QueueEnvelope into a QuarantineEntry for display.
func (h *QuarantineHandler) envToEntry(env *bongov1.QueueEnvelope) templates.QuarantineEntry {
	entry := templates.QuarantineEntry{
		ID:         env.Id,
		Sender:     env.Sender,
		Recipients: strings.Join(env.Recipients, ", "),
		ReceivedAt: time.Unix(env.ReceivedAt, 0),
	}

	// Extract reason and score from the last stage that resulted in quarantine.
	for _, sr := range env.Stages {
		if sr.Action == "quarantine" {
			entry.Reason = strings.Join(sr.Reasons, "; ")
			entry.Score = sr.Score
			entry.Stage = sr.Stage
		}
	}

	// Parse subject from blob if available.
	if env.BlobHash != "" && h.blobs != nil {
		entry.Subject = h.extractSubject(env.BlobHash)
	}

	return entry
}

// extractSubject reads the blob and parses the Subject header.
// Returns an empty string on any failure to keep loading fast.
func (h *QuarantineHandler) extractSubject(hash string) string {
	rc, err := h.blobs.Get(hash)
	if err != nil {
		return ""
	}
	defer rc.Close()

	msg, err := mail.ReadMessage(rc)
	if err != nil {
		return ""
	}

	return msg.Header.Get("Subject")
}

// RunAutoPurge periodically removes quarantined messages older than retention.
// It blocks until ctx is cancelled; intended to run as a goroutine.
func RunAutoPurge(ctx context.Context, spool *pipeline.SpoolStore, blobs *store.BlobStore, retention time.Duration, interval time.Duration, logger *slog.Logger) {
	if spool == nil {
		return
	}
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	if interval <= 0 {
		interval = time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purgeQuarantine(spool, blobs, retention, logger)
		}
	}
}

// purgeQuarantine does one pass: removes envelopes older than retention.
func purgeQuarantine(spool *pipeline.SpoolStore, blobs *store.BlobStore, retention time.Duration, logger *slog.Logger) {
	envs, err := spool.List(quarantineDir)
	if err != nil {
		logger.Error("quarantine: auto-purge list failed", "error", err)
		return
	}

	cutoff := time.Now().Add(-retention)
	purged := 0

	for _, env := range envs {
		if env.ReceivedAt == 0 {
			continue
		}
		if time.Unix(env.ReceivedAt, 0).Before(cutoff) {
			if removeErr := spool.Remove(quarantineDir, env.Id); removeErr != nil {
				logger.Error("quarantine: auto-purge remove failed", "id", env.Id, "error", removeErr)
				continue
			}
			// Blob left to the GC (shared, content-addressed). Auto-purge
			// runs unattended on a timer, so this was the most damaging of
			// the three delete sites: it silently destroyed other users'
			// copies of the same message with nobody watching.
			purged++
		}
	}

	if purged > 0 {
		logger.Info("quarantine: auto-purge complete", "purged", purged, "retention", retention)
	}
}

// truncate returns s truncated to at most n runes. If truncated, "..." is appended.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// releaseMessageImpl is the shared implementation for releasing a message
// from quarantine to delivery. The message retains its scanning checked
// flags (MsgFlagSpamChecked, etc.), so the pipeline will skip re-running
// scanning stages. This is correct behaviour: the admin has reviewed and
// overridden the verdict, so we do not re-scan. The message will proceed
// directly to delivery (see PrepareForRelease for the full rationale,
// including why the rules stage still runs and why TargetMailbox is forced
// to INBOX).
func (h *QuarantineHandler) releaseMessageImpl(id string) error {
	return h.requeueFromQuarantine(id, pipeline.PrepareForRelease)
}

// requeueFromQuarantine reads the envelope for id from quarantine/, applies
// prep (which mutates the envelope in place — pipeline.PrepareForRelease or
// pipeline.PrepareForRescan), writes it back, and moves it to incoming/ so
// the runner picks it up. This is the single place that implements the
// read -> prepare -> write -> move sequence shared by release and rescan,
// so the two code paths cannot drift.
func (h *QuarantineHandler) requeueFromQuarantine(id string, prep func(*bongov1.QueueEnvelope)) error {
	// id is user-supplied (URL param or bulk form field). Reject anything
	// that looks like a path segment escape before it reaches the
	// filesystem: ReadEnvelope/WriteEnvelope/Move all join it directly onto
	// the spool base directory (see the #nosec G304 on ReadEnvelope, which
	// only holds for genuinely internal spool names).
	if err := validateSpoolID(id); err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}

	env, err := h.spool.ReadEnvelope(quarantineDir, id)
	if err != nil {
		return fmt.Errorf("read envelope: %w", err)
	}

	// WriteEnvelope derives its filename from env.Id, while Move (below)
	// operates on the URL/form id. A legacy or corrupt envelope whose Id
	// does not match the requested id would otherwise write to a different
	// file than the one that gets moved — silently leaving the real target
	// untouched (or, for an empty Id, writing a phantom quarantine/.env
	// that List() would later pick up). Fail loudly instead.
	if env.Id != id {
		return fmt.Errorf("envelope id %q does not match requested id %q", env.Id, id)
	}

	prep(env)

	// Write-then-move: a crash between these two calls leaves the envelope
	// in quarantine/ with the prepared flags, still quarantined and still
	// actionable by an admin. The reverse order would open a window where
	// the envelope exists in both incoming/ and quarantine/ at once —
	// deliverable by the runner and still releasable/rescannable by an
	// admin at the same time.
	if err := h.spool.WriteEnvelope(quarantineDir, env); err != nil {
		return fmt.Errorf("write envelope: %w", err)
	}

	// Move to incoming/ so the runner picks it up for delivery (unlike
	// deliver/ which is a terminal archive, incoming/ is where the runner
	// dequeues messages to process).
	if err := h.spool.Move(id, quarantineDir, incomingDir); err != nil {
		return fmt.Errorf("move to incoming: %w", err)
	}

	return nil
}

// validateSpoolID rejects ids that are empty or contain path-traversal
// characters. id is always derived from user-supplied input (a chi URL
// param or a bulk-action form field) before being joined onto spool
// directory paths, so it must not be trusted as an internal spool name.
func validateSpoolID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("id %q contains invalid path characters", id)
	}
	return nil
}

// quarantineScoreClass returns a CSS badge class for a spam score.
// Scores >= 0.8 get the disabled (red) badge, >= 0.5 the pending (yellow) badge,
// and lower scores get the default badge.
func quarantineScoreClass(score float32) string {
	switch {
	case score >= 0.8:
		return "badge badge-disabled"
	case score >= 0.5:
		return "badge badge-pending"
	default:
		return "badge"
	}
}
