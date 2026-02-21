package handlers

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// LogEntry represents a single log entry for streaming.
type LogEntry struct {
	Time      time.Time
	Level     string
	Component string
	Message   string
}

// LogSink captures slog entries and fans them out to SSE subscribers.
type LogSink struct {
	mu          sync.RWMutex
	subscribers map[chan LogEntry]struct{}
}

// NewLogSink creates a new LogSink.
func NewLogSink() *LogSink {
	return &LogSink{
		subscribers: make(map[chan LogEntry]struct{}),
	}
}

// Subscribe returns a channel that receives log entries.
func (ls *LogSink) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 100)
	ls.mu.Lock()
	ls.subscribers[ch] = struct{}{}
	ls.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (ls *LogSink) Unsubscribe(ch chan LogEntry) {
	ls.mu.Lock()
	delete(ls.subscribers, ch)
	ls.mu.Unlock()
	close(ch)
}

// Emit sends a log entry to all subscribers.
func (ls *LogSink) Emit(entry LogEntry) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	for ch := range ls.subscribers {
		select {
		case ch <- entry:
		default:
			// Drop if subscriber is slow.
		}
	}
}

// SlogHandler returns a slog.Handler that sends entries to the LogSink.
func (ls *LogSink) SlogHandler(base slog.Handler) slog.Handler {
	return &logSinkHandler{sink: ls, base: base}
}

type logSinkHandler struct {
	sink      *LogSink
	base      slog.Handler
	component string
}

func (h *logSinkHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

func (h *logSinkHandler) Handle(ctx context.Context, record slog.Record) error {
	// Extract component from attributes.
	component := h.component
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "component" {
			component = a.Value.String()
			return false
		}
		return true
	})

	// Infer component from message prefix if not set.
	if component == "" {
		msg := record.Message
		for _, prefix := range []string{"smtp:", "imap:", "raft:", "web:", "queue:", "gossip:", "blob:"} {
			if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
				component = prefix[:len(prefix)-1]
				break
			}
		}
		if component == "" {
			component = "system"
		}
	}

	h.sink.Emit(LogEntry{
		Time:      record.Time,
		Level:     record.Level.String(),
		Component: component,
		Message:   record.Message,
	})

	return h.base.Handle(ctx, record)
}

func (h *logSinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newComponent := h.component
	for _, a := range attrs {
		if a.Key == "component" {
			newComponent = a.Value.String()
		}
	}
	return &logSinkHandler{
		sink:      h.sink,
		base:      h.base.WithAttrs(attrs),
		component: newComponent,
	}
}

func (h *logSinkHandler) WithGroup(name string) slog.Handler {
	return &logSinkHandler{
		sink:      h.sink,
		base:      h.base.WithGroup(name),
		component: h.component,
	}
}
