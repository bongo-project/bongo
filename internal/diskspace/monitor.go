// Package diskspace watches free space on the data directory and tells the
// write paths to refuse new work while it is nearly exhausted.
//
// Only paths that add data are gated: SMTP MAIL, IMAP APPEND, web compose and
// import, and CalDAV/CardDAV PUT. Raft apply, the queue worker, blob GC, the
// inbound deliver stage and every read are never gated: they free space, or
// refusing them would make replicas diverge or lose mail already acknowledged.
package diskspace

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bongo-project/bongo/internal/observability"
)

// DefaultInterval is how often free space is sampled.
const DefaultInterval = 10 * time.Second

// Monitor samples free space and caches the verdict. A nil *Monitor is valid
// and never low.
type Monitor struct {
	dirs     []string
	minFree  uint64
	interval time.Duration
	freeFn   func(dir string) (uint64, error)
	logger   *slog.Logger

	low atomic.Bool

	// mu guards the transition state used only to log once per change.
	mu        sync.Mutex
	errActive bool
}

// New returns a monitor for dirs that reports low when the smallest free figure
// is below minFreeBytes. Callers do not create one when the check is disabled.
func New(dirs []string, minFreeBytes uint64, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		dirs:     dirs,
		minFree:  minFreeBytes,
		interval: DefaultInterval,
		freeFn:   FreeBytes,
		logger:   logger,
	}
}

// Low reports whether new work should be refused. It is lock-free and safe on
// a nil receiver.
func (m *Monitor) Low() bool {
	return m != nil && m.low.Load()
}

// Run samples immediately, so a node that starts with a full disk refuses at
// once, then on every interval until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.sample()
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample()
		}
	}
}

// sample takes the smallest free figure across the directories. A statfs error
// fails open: that directory is skipped, and a failing statfs never stops mail.
func (m *Monitor) sample() {
	var (
		minFreeSeen uint64
		minDir      string
		have        bool
		sawErr      bool
	)
	for _, dir := range m.dirs {
		free, err := m.freeFn(dir)
		if err != nil {
			sawErr = true
			m.mu.Lock()
			first := !m.errActive
			m.errActive = true
			m.mu.Unlock()
			if first {
				m.logger.Warn("storage free-space check failed, not refusing work", "dir", dir, "error", err)
			}
			continue
		}
		if !have || free < minFreeSeen {
			minFreeSeen, minDir, have = free, dir, true
		}
	}
	if !sawErr {
		m.mu.Lock()
		m.errActive = false
		m.mu.Unlock()
	}

	nowLow := have && minFreeSeen < m.minFree
	was := m.low.Swap(nowLow)
	if nowLow {
		observability.StorageLow.Set(1)
	} else {
		observability.StorageLow.Set(0)
	}
	switch {
	case nowLow && !was:
		m.logger.Warn("storage low",
			"free_mb", minFreeSeen>>20, "threshold_mb", m.minFree>>20, "dir", minDir)
	case !nowLow && was && have:
		m.logger.Info("storage recovered")
	case !nowLow && was:
		m.logger.Warn("storage free-space check failed while low, no longer refusing work")
	}
}

// SetFreeFunc replaces the statfs call. It exists so tests in other packages
// can inject free-space figures; call it before Run.
func (m *Monitor) SetFreeFunc(fn func(dir string) (uint64, error)) {
	m.freeFn = fn
}
