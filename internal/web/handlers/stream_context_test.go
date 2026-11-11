package handlers

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

func TestStreamContextEndsOnServerShutdown(t *testing.T) {
	shutdown := make(chan struct{})
	req := httptest.NewRequest("GET", "/events", nil)
	req = req.WithContext(middleware.WithServerShutdown(req.Context(), shutdown))
	ctx, cancel := streamContext(req)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("stream context ended before shutdown")
	case <-time.After(50 * time.Millisecond):
	}
	close(shutdown)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stream context still open after server shutdown began")
	}
}

func TestStreamContextWithoutServerSignalFollowsRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "/events", nil)
	ctx, cancel := streamContext(req)
	select {
	case <-ctx.Done():
		t.Fatal("stream context ended with no shutdown signal")
	default:
	}
	cancel()
	<-ctx.Done()
}
