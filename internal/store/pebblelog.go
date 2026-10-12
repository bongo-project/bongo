package store

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cockroachdb/pebble"
)

var _ pebble.Logger = (*pebbleLogger)(nil)

// pebbleLogger adapts slog to pebble.Logger (Infof and Fatalf only; Pebble
// v1.1.5 has no Errorf). Records are classified on the format string, which is
// a stable constant, rather than on the formatted text. The formatted text is
// kept in the "line" attribute.
type pebbleLogger struct {
	logger *slog.Logger
	exit   func(int)
}

func newPebbleLogger(l *slog.Logger) *pebbleLogger {
	return &pebbleLogger{logger: l, exit: os.Exit}
}

// Infof implements pebble.Logger. Pebble reports background errors through
// Infof, so those are promoted to ERROR.
func (p *pebbleLogger) Infof(format string, args ...interface{}) {
	line := strings.TrimSpace(fmt.Sprintf(format, args...))
	switch {
	case strings.HasPrefix(format, "[JOB %d] WAL file"):
		p.logger.Info("pebble.wal_replay", "line", line)
	case strings.HasPrefix(format, "background error:"):
		p.logger.Error("pebble.background_error", "line", line)
	default:
		p.logger.Info("pebble.info", "line", line)
	}
}

// Fatalf implements pebble.Logger. Pebble callers assume it never returns, so
// it exits the process after logging.
func (p *pebbleLogger) Fatalf(format string, args ...interface{}) {
	p.logger.Error("pebble.fatal", "line", strings.TrimSpace(fmt.Sprintf(format, args...)))
	p.exit(1)
}
