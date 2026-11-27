package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/bongo-project/bongo/internal/security"
)

// certPollInterval is how often the files behind each certificate holder are
// checked for a change. Fixed: there is no config key for it.
const certPollInterval = 60 * time.Second

// certReloader keeps the file-backed certificate holders of the listeners and
// reloads their contents on SIGHUP and when a file changes on disk. The paths
// themselves stay restart keys. The zero value is ready to use.
type certReloader struct {
	mu      sync.Mutex
	holders []*certWatch
}

// certWatch is one holder with the file stamp seen at the last attempt.
type certWatch struct {
	h     *security.CertHolder
	stamp certStamp
}

// certStamp identifies the state of the two files by mtime and size.
type certStamp struct {
	certMod, keyMod   int64
	certSize, keySize int64
	certOK, keyOK     bool
}

func statStamp(h *security.CertHolder) certStamp {
	var s certStamp
	cf, kf := h.Files()
	if fi, err := os.Stat(cf); err == nil {
		s.certMod, s.certSize, s.certOK = fi.ModTime().UnixNano(), fi.Size(), true
	}
	if fi, err := os.Stat(kf); err == nil {
		s.keyMod, s.keySize, s.keyOK = fi.ModTime().UnixNano(), fi.Size(), true
	}
	return s
}

// register adds a holder to the watch set. Nil and static holders are ignored,
// as is a holder already registered.
func (c *certReloader) register(h *security.CertHolder) {
	if h == nil || !h.FileBacked() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.holders {
		if w.h == h {
			return
		}
	}
	c.holders = append(c.holders, &certWatch{h: h, stamp: statStamp(h)})
}

// reload runs one holder's reload and logs the outcome.
func (c *certReloader) reload(logger *slog.Logger, w *certWatch, why string) {
	changed, err := w.h.Reload()
	cf, _ := w.h.Files()
	if err != nil {
		var re *security.CertReloadError
		reason, files := "unknown", []string{cf}
		if errors.As(err, &re) {
			reason, files = re.Reason, re.Files
		}
		logger.Error("tls: certificate reload failed, keeping current certificate",
			"listener", w.h.Name(), "files", files, "reason", reason, "trigger", why)
		return
	}
	cur := w.h.Current()
	if changed && cur != nil && cur.Leaf != nil {
		logger.Info("tls: certificate reloaded", "listener", w.h.Name(), "cert_file", cf,
			"serial", cur.Leaf.SerialNumber.Text(16), "not_after", cur.Leaf.NotAfter, "trigger", why)
		return
	}
	logger.Debug("tls: certificate unchanged", "listener", w.h.Name(), "trigger", why)
}

// reloadAll reloads every holder regardless of file stamps (SIGHUP).
func (c *certReloader) reloadAll(logger *slog.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.holders {
		w.stamp = statStamp(w.h)
		c.reload(logger, w, "signal")
	}
}

// pollOnce reloads each holder whose files changed since the last attempt.
// The stamp is recorded whether or not the reload succeeds, so a bad pair is
// reported once per change and not every interval.
func (c *certReloader) pollOnce(logger *slog.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.holders {
		now := statStamp(w.h)
		if now == w.stamp {
			continue
		}
		w.stamp = now
		c.reload(logger, w, "poll")
	}
}

// run polls until ctx ends.
func (c *certReloader) run(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.pollOnce(logger)
		}
	}
}
