package config

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// LogComponents are the names telemetry.log.components accepts: the message
// prefixes the server logs with ("imap: login succeeded") and the "component"
// attribute values. TestLogComponentsMatchSource keeps the list in step with
// the source.
var LogComponents = []string{
	"address-verify", "admin", "agent", "antispam", "antivirus", "auth",
	"blob-gc", "blobsync", "bounce", "caldav", "calendar", "calendar-grants",
	"calendar-share", "calendar-subscriptions", "calendars", "carddav", "cluster",
	"compose", "control", "dav", "dedup", "deliver", "dispatch", "dkim", "dmarc",
	"dnsbl", "dry-run", "forward-verify", "fsm", "header-backfill", "i18n", "imap",
	"imaps", "index", "login", "logout", "memberlist", "mimesafe", "orphan-gc",
	"pebble", "peer", "pipeline", "pop3", "pop3s", "preview-backfill", "quarantine",
	"queue", "raft", "relay", "reminders", "rules", "rules-migration", "scan",
	"session", "shutdown", "smtp", "spamassassin", "spool", "startup", "tasks",
	"tasks-migration", "usage-recompute", "vacation", "web",
}

// componentLevels maps the accepted level names. There is no "off": a
// component is never quieter than error (operator decision).
var componentLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func (l LogConfig) validateComponents() error {
	names := make([]string, 0, len(l.Components))
	for name := range l.Components {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !slices.Contains(LogComponents, name) {
			return fmt.Errorf("telemetry.log.components: unknown component %q (see LOGGING.md for the list)", name)
		}
		if _, ok := componentLevels[strings.ToLower(l.Components[name])]; !ok {
			return fmt.Errorf("telemetry.log.components: %s: unknown level %q (valid: debug, info, warn, error)", name, l.Components[name])
		}
	}
	return nil
}

// ComponentLevels returns the configured component levels. Call it on a
// validated config; an entry that does not validate is left out.
func (l LogConfig) ComponentLevels() map[string]slog.Level {
	out := make(map[string]slog.Level, len(l.Components))
	for name, lv := range l.Components {
		if level, ok := componentLevels[strings.ToLower(lv)]; ok {
			out[name] = level
		}
	}
	return out
}
