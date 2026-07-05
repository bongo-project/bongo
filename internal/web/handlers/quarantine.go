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

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	quarantineDir = "quarantine"
	deliverDir    = "deliver"
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

// ReleaseMessage moves a message from quarantine/ to deliver/ so the pipeline picks it up.
func (h *QuarantineHandler) ReleaseMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if h.spool == nil {
		http.Error(w, "spool not available", http.StatusServiceUnavailable)
		return
	}

	if err := h.spool.Move(id, quarantineDir, deliverDir); err != nil {
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

	// Read envelope to get blob hash before deleting.
	env, err := h.spool.ReadEnvelope(quarantineDir, id)
	if err != nil {
		h.logger.Warn("quarantine: delete — could not read envelope, removing anyway", "id", id, "error", err)
	}

	if removeErr := h.spool.Remove(quarantineDir, id); removeErr != nil {
		h.logger.Error("quarantine: delete envelope failed", "id", id, "error", removeErr)
		http.Error(w, "failed to delete message", http.StatusInternalServerError)
		return
	}

	// Remove blob (best-effort; GC will clean it up if this fails).
	if env != nil && env.BlobHash != "" && h.blobs != nil {
		if delErr := h.blobs.Delete(env.BlobHash); delErr != nil {
			h.logger.Debug("quarantine: could not remove blob (may be referenced elsewhere)", "hash", env.BlobHash, "error", delErr)
		}
	}

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
			if err := h.spool.Move(id, quarantineDir, deliverDir); err != nil {
				h.logger.Error("quarantine: bulk release failed", "id", id, "error", err)
				failed++
				continue
			}
			succeeded++
		case "delete":
			env, _ := h.spool.ReadEnvelope(quarantineDir, id)
			if err := h.spool.Remove(quarantineDir, id); err != nil {
				h.logger.Error("quarantine: bulk delete failed", "id", id, "error", err)
				failed++
				continue
			}
			if env != nil && env.BlobHash != "" && h.blobs != nil {
				if delErr := h.blobs.Delete(env.BlobHash); delErr != nil {
					h.logger.Debug("quarantine: bulk — could not remove blob", "hash", env.BlobHash, "error", delErr)
				}
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

	flash := fmt.Sprintf("%d+message(s)+%sd", succeeded, action)
	if failed > 0 {
		flash += fmt.Sprintf("+(%d+failed)", failed)
	}
	http.Redirect(w, r, "/admin/quarantine?flash="+flash, http.StatusSeeOther) // #nosec G710 -- fixed same-origin admin path
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
			if env.BlobHash != "" && blobs != nil {
				if delErr := blobs.Delete(env.BlobHash); delErr != nil {
					logger.Debug("quarantine: auto-purge blob remove failed", "hash", env.BlobHash, "error", delErr)
				}
			}
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
