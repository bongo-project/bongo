package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/web/middleware"
)

// errNotFound is a sentinel error for use in test fakes.
var errNotFound = errors.New("not found")

// withChiParam injects a chi URL parameter into the request context.
func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withUserContext injects a userID and email into the request context for testing.
func withUserContext(r *http.Request, userID, email string) *http.Request {
	ctx := middleware.WithUserContext(r.Context(), userID, email)
	return r.WithContext(ctx)
}

// discardLogger returns a logger that discards all output, suitable for tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

// discardWriter implements io.Writer by discarding all bytes.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
