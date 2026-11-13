// Package logfilter applies a log level per component on top of the global
// level. A record's component is its "component" attribute, or else the
// message prefix before the first ": " ("imap: login succeeded" is imap).
// Records with no component use the global level.
package logfilter

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
)

// Filter holds the global level and the per-component levels. Both can change
// while logging runs: the global level through its LevelVar, the components
// through SetComponents, which swaps an immutable map.
type Filter struct {
	global *slog.LevelVar
	state  atomic.Pointer[componentState]
}

type componentState struct {
	levels map[string]slog.Level
	// lowest is the most verbose component level, or the error level when
	// there are none, so Enabled can reject a record before it is built.
	lowest slog.Level
}

// New returns a filter over global with no component levels.
func New(global *slog.LevelVar) *Filter {
	f := &Filter{global: global}
	f.SetComponents(nil)
	return f
}

// SetComponents replaces the component levels. The map is copied; a nil or
// empty map returns every component to the global level.
func (f *Filter) SetComponents(levels map[string]slog.Level) {
	st := &componentState{levels: make(map[string]slog.Level, len(levels)), lowest: slog.LevelError}
	for name, lv := range levels {
		st.levels[name] = lv
		st.lowest = min(st.lowest, lv)
	}
	f.state.Store(st)
}

// Handler wraps next, which must accept every level: the filter decides.
func (f *Filter) Handler(next slog.Handler) slog.Handler {
	return &handler{f: f, next: next}
}

func (f *Filter) levelFor(component string) slog.Level {
	if lv, ok := f.state.Load().levels[component]; ok {
		return lv
	}
	return f.global.Level()
}

type handler struct {
	f         *Filter
	next      slog.Handler
	component string // from WithAttrs
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	floor := min(h.f.global.Level(), h.f.state.Load().lowest)
	if h.component != "" {
		floor = h.f.levelFor(h.component)
	}
	return level >= floor && h.next.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.f.levelFor(h.recordComponent(r)) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

func (h *handler) recordComponent(r slog.Record) string {
	if h.component != "" {
		return h.component
	}
	var c string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "component" {
			c = a.Value.String()
			return false
		}
		return true
	})
	if c != "" {
		return c
	}
	return Prefix(r.Message)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := h.component
	for _, a := range attrs {
		if a.Key == "component" {
			c = a.Value.String()
		}
	}
	return &handler{f: h.f, next: h.next.WithAttrs(attrs), component: c}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{f: h.f, next: h.next.WithGroup(name), component: h.component}
}

// Prefix returns the component prefix of a log message: the text before the
// first ": " when it is a short run of lower-case letters, digits, '-' or
// '_'. Otherwise it returns "".
func Prefix(msg string) string {
	i := strings.Index(msg, ": ")
	if i <= 0 || i > 32 {
		return ""
	}
	for _, c := range msg[:i] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return ""
		}
	}
	return msg[:i]
}
