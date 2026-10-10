package observability

import (
	"context"
	"log"
	"log/slog"
	"strings"
)

// LibraryRule maps library log lines starting with Prefix to a slog level and
// a stable message key.
type LibraryRule struct {
	Prefix string
	Level  slog.Level
	Msg    string
}

// LibraryLogger returns a *log.Logger for libraries that only accept a stdlib
// logger (or a Printf/Println interface). Each line becomes one slog record:
// the first matching rule picks the level and message key, otherwise the line
// is logged at WARN under fallbackMsg. The library's text is kept in "line".
func LibraryLogger(logger *slog.Logger, fallbackMsg string, rules ...LibraryRule) *log.Logger {
	return log.New(&libraryWriter{logger: logger, fallback: fallbackMsg, rules: rules}, "", 0)
}

type libraryWriter struct {
	logger   *slog.Logger
	fallback string
	rules    []LibraryRule
}

func (w *libraryWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return len(p), nil
	}
	level, msg := slog.LevelWarn, w.fallback
	for _, r := range w.rules {
		if strings.HasPrefix(line, r.Prefix) {
			level, msg = r.Level, r.Msg
			break
		}
	}
	w.logger.Log(context.Background(), level, msg, "line", line)
	return len(p), nil
}

// IMAPLibraryLogger routes go-imap imapserver logs. Per-client protocol and
// I/O errors are client noise (DEBUG); panics are ERROR.
func IMAPLibraryLogger(logger *slog.Logger) *log.Logger {
	return LibraryLogger(logger, "imap.server_error",
		LibraryRule{Prefix: "panic ", Level: slog.LevelError, Msg: "imap.panic"},
		LibraryRule{Prefix: "handling ", Level: slog.LevelDebug, Msg: "imap.command_error"},
		LibraryRule{Prefix: "failed to read command", Level: slog.LevelDebug, Msg: "imap.read_error"},
		LibraryRule{Prefix: "failed to write greeting", Level: slog.LevelDebug, Msg: "imap.greeting_error"},
		LibraryRule{Prefix: "accept error", Level: slog.LevelWarn, Msg: "imap.accept_error"},
	)
}

// SMTPLibraryLogger routes go-smtp Server.ErrorLog. Per-connection errors are
// DEBUG; panics are ERROR.
func SMTPLibraryLogger(logger *slog.Logger) *log.Logger {
	return LibraryLogger(logger, "smtp.server_error",
		LibraryRule{Prefix: "panic serving", Level: slog.LevelError, Msg: "smtp.panic"},
		LibraryRule{Prefix: "error handling", Level: slog.LevelDebug, Msg: "smtp.conn_error"},
		LibraryRule{Prefix: "accept error", Level: slog.LevelWarn, Msg: "smtp.accept_error"},
	)
}

// HTTPLibraryLogger routes net/http Server.ErrorLog. TLS handshake failures
// (scanners, bad clients) are DEBUG; handler panics are ERROR.
func HTTPLibraryLogger(logger *slog.Logger) *log.Logger {
	return LibraryLogger(logger, "http.server_error",
		LibraryRule{Prefix: "http: TLS handshake error", Level: slog.LevelDebug, Msg: "http.tls_handshake_error"},
		LibraryRule{Prefix: "http: panic serving", Level: slog.LevelError, Msg: "http.panic"},
		LibraryRule{Prefix: "http: Accept error", Level: slog.LevelWarn, Msg: "http.accept_error"},
	)
}
