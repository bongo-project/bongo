package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	// spamThreshold is the score at or above which a message is moved to Junk.
	spamThreshold float32 = 0.5

	// sseProgressInterval controls how often SSE updates are emitted (every N messages).
	sseProgressInterval = 10
)

// ScanProgress holds the progress state for an active scan.
type ScanProgress struct {
	Scanned int  `json:"scanned"`
	Spam    int  `json:"spam"`
	Total   int  `json:"total"`
	Done    bool `json:"done"`
}

// scanJob tracks the state of a running mailbox scan.
type scanJob struct {
	cancel   context.CancelFunc
	progress chan ScanProgress
}

// ScanHandler handles retroactive spam scanning of mailboxes.
type ScanHandler struct {
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	applier   store.RaftApplier
	scorer    *antispam.Scorer
	logger    *slog.Logger

	// mu guards activeScans.
	mu          sync.Mutex
	activeScans map[string]*scanJob // key: "userID:mailbox"
}

// NewScanHandler creates a ScanHandler.
func NewScanHandler(
	pebble *store.PebbleStore,
	blobs *store.BlobStore,
	mailboxes *store.MailboxStore,
	applier store.RaftApplier,
	logger *slog.Logger,
) *ScanHandler {
	return &ScanHandler{
		pebble:      pebble,
		blobs:       blobs,
		mailboxes:   mailboxes,
		applier:     applier,
		scorer:      antispam.NewScorer(),
		logger:      logger,
		activeScans: make(map[string]*scanJob),
	}
}

// scanKey returns the sync.Map key for a user+mailbox combination.
func scanKey(userID, mailbox string) string {
	return userID + ":" + mailbox
}

// StartScan starts a background spam scan of the named mailbox.
// Returns 202 Accepted on success, 409 Conflict if a scan is already running,
// or 404 if the mailbox does not exist.
// Optional query param: force=true rescans messages that already have a spam score.
func (h *ScanHandler) StartScan(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	force := r.URL.Query().Get("force") == "true"

	key := scanKey(userID, mailbox)

	// Check for a running scan before the mailbox existence check so the 409
	// path is reachable without real storage.
	h.mu.Lock()
	if _, running := h.activeScans[key]; running {
		h.mu.Unlock()
		http.Error(w, "Scan already running", http.StatusConflict)
		return
	}
	h.mu.Unlock()

	// Verify the mailbox exists.
	if h.mailboxes != nil {
		if _, err := h.mailboxes.GetMailbox(userID, mailbox); err != nil {
			http.Error(w, "Mailbox not found", http.StatusNotFound)
			return
		}
	}

	// Create a buffered channel so the goroutine does not block on slow consumers.
	progressCh := make(chan ScanProgress, 64)
	ctx, cancel := context.WithCancel(context.Background())
	job := &scanJob{
		cancel:   cancel,
		progress: progressCh,
	}

	h.mu.Lock()
	// Re-check under lock in case a concurrent StartScan raced through.
	if _, running := h.activeScans[key]; running {
		h.mu.Unlock()
		cancel()
		http.Error(w, "Scan already running", http.StatusConflict)
		return
	}
	h.activeScans[key] = job
	h.mu.Unlock()

	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.activeScans, key)
			h.mu.Unlock()
			close(progressCh)
		}()
		h.runScan(ctx, userID, mailbox, force, progressCh)
	}()

	h.logger.Info("scan: started", "user", userID, "mailbox", mailbox, "force", force)
	w.WriteHeader(http.StatusAccepted)
}

// ScanProgress streams SSE progress events for a running scan.
// Events are JSON objects: {"scanned":N,"spam":N,"total":N,"done":false}
func (h *ScanHandler) ScanProgress(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))

	key := scanKey(userID, mailbox)

	h.mu.Lock()
	job, running := h.activeScans[key]
	h.mu.Unlock()

	if !running {
		http.Error(w, "No scan running for this mailbox", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-job.progress:
			if !ok {
				// Channel closed — emit final done event if not already sent.
				fmt.Fprintf(w, "data: {\"scanned\":0,\"spam\":0,\"total\":0,\"done\":true}\n\n")
				flusher.Flush()
				return
			}
			b, _ := json.Marshal(p)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			if p.Done {
				return
			}
		}
	}
}

// CancelScan cancels a running scan for the named mailbox.
// Returns 204 No Content on success, 404 if no scan is running.
func (h *ScanHandler) CancelScan(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))

	key := scanKey(userID, mailbox)

	h.mu.Lock()
	job, running := h.activeScans[key]
	h.mu.Unlock()

	if !running {
		http.Error(w, "No scan running for this mailbox", http.StatusNotFound)
		return
	}

	job.cancel()
	h.logger.Info("scan: cancelled", "user", userID, "mailbox", mailbox)
	w.WriteHeader(http.StatusNoContent)
}

// runScan performs the actual scan work in a goroutine.
func (h *ScanHandler) runScan(ctx context.Context, userID, mailbox string, force bool, progress chan<- ScanProgress) {
	msgs, err := h.mailboxes.ListMessages(userID, mailbox)
	if err != nil {
		h.logger.Error("scan: listing messages failed", "user", userID, "mailbox", mailbox, "error", err)
		return
	}

	total := len(msgs)
	scanned := 0
	spam := 0

	emit := func(done bool) {
		p := ScanProgress{
			Scanned: scanned,
			Spam:    spam,
			Total:   total,
			Done:    done,
		}
		select {
		case progress <- p:
		default:
			// Drop the update if the consumer is slow; the next one will be accurate.
		}
	}

	for _, msg := range msgs {
		// Check for context cancellation between messages.
		select {
		case <-ctx.Done():
			h.logger.Info("scan: cancelled by context", "user", userID, "mailbox", mailbox)
			return
		default:
		}

		// Load mail metadata.
		meta := h.loadScanMeta(userID, msg.MailGuid)
		if meta == nil {
			scanned++
			continue
		}

		// Skip already-scored messages unless force=true.
		if !force && meta.SpamScore > 0 {
			scanned++
			if meta.SpamScore >= spamThreshold {
				spam++
			}
			if scanned%sseProgressInterval == 0 {
				emit(false)
			}
			continue
		}

		// Score the message.
		score := h.scoreMessage(meta)
		if score >= spamThreshold {
			// Move to Junk if not already there.
			if mailbox != "Junk" {
				if err := h.mailboxes.MoveMessage(userID, mailbox, msg.Uid, "Junk"); err != nil {
					h.logger.Warn("scan: move to Junk failed",
						"user", userID, "uid", msg.Uid, "error", err)
				}
			}
			// Persist the score.
			if h.applier != nil {
				_ = antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, score)
			}
			spam++
		}

		scanned++
		if scanned%sseProgressInterval == 0 {
			emit(false)
		}
	}

	h.logger.Info("scan: completed",
		"user", userID, "mailbox", mailbox,
		"scanned", scanned, "spam", spam, "total", total)

	emit(true)
}

// scoreMessage reads the raw blob and returns a heuristic spam score [0,1].
func (h *ScanHandler) scoreMessage(meta *bongov1.MailMeta) float32 {
	if meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		return 0
	}

	rc, err := h.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		return 0
	}
	raw, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return 0
	}

	var subject, bodySample string
	headers := make(map[string]string)

	env, parseErr := enmime.ReadEnvelope(bytes.NewReader(raw))
	if parseErr == nil {
		subject = env.GetHeader("Subject")
		for _, key := range []string{"Message-Id", "Message-ID", "Date", "From"} {
			if v := env.GetHeader(key); v != "" {
				headers[key] = v
			}
		}
		if len(env.Text) > 0 {
			bodySample = env.Text
			if len(bodySample) > 4096 {
				bodySample = bodySample[:4096]
			}
		}
	}

	input := &antispam.ScoringInput{
		Headers:    headers,
		Subject:    subject,
		BodySample: bodySample,
	}

	result := h.scorer.Score(input)
	return result.Score
}

// loadScanMeta reads MailMeta from Pebble for a given GUID.
func (h *ScanHandler) loadScanMeta(userID, guid string) *bongov1.MailMeta {
	key := fmt.Sprintf("/user/%s/mail/%s", userID, guid)
	data, err := h.pebble.Get([]byte(key))
	if err != nil {
		return nil
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}
