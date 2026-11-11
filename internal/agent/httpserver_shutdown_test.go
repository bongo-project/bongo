package agent

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// An open browser tab holds an SSE stream. Shutdown must end it at once, not
// wait out the drain: a container's 10s stop grace otherwise expires first
// and SIGKILLs the process before Raft and the stores are closed.
func TestHTTPServerShutdownEndsSSEStreams(t *testing.T) {
	sse := handlers.NewSSEHandler(bongoimap.NewNotifier(), nil, nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse.Events(w, r.WithContext(middleware.WithUserContext(r.Context(), "u1", "u1@example.test")))
	})
	srv := newHTTPServer("127.0.0.1:0", h, slog.New(slog.DiscardHandler))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if line, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || line != ": ping\n" {
		t.Fatalf("stream did not open: %q, %v", line, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown waited for the open stream: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Shutdown took %s with one SSE stream open", d)
	}
}
