package netx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostUpdateCheckRequestShape(t *testing.T) {
	const payload = `{"version":"0.17.0","instance_id":"0123456789abcdef0123456789abcdef"}`
	var hdr http.Header
	var gotBody, method string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hdr, gotBody, method = r.Header.Clone(), string(b), r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"latest":"0.17.1"}`))
	}))
	defer srv.Close()
	c, u := tlsOneClickClient(t, srv)

	out, err := c.PostUpdateCheck(context.Background(), u, []byte(payload))
	if err != nil {
		t.Fatalf("PostUpdateCheck: %v", err)
	}
	if string(out) != `{"latest":"0.17.1"}` {
		t.Errorf("answer = %q", out)
	}
	if method != http.MethodPost || gotBody != payload {
		t.Errorf("method %q body %q", method, gotBody)
	}
	allowed := []string{"User-Agent", "Content-Type", "Content-Length", "Accept-Encoding", "Connection"}
	hostname, _ := os.Hostname()
	for name, vals := range hdr {
		if !slices.Contains(allowed, name) {
			t.Errorf("unexpected header %s", name)
		}
		for _, v := range vals {
			if strings.Contains(v, "@") || (hostname != "" && strings.Contains(v, hostname)) {
				t.Errorf("header %s carries identifying text: %q", name, v)
			}
		}
	}
	if hdr.Get("User-Agent") != "bongo-go" || hdr.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", hdr)
	}
}

func TestPostUpdateCheckAnswers(t *testing.T) {
	big := strings.Repeat("a", updateCheckMaxBody+1)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
		wantMsg string
	}{
		{"301", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/x", http.StatusMovedPermanently) }, ErrRedirect, ""},
		{"302", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/x", http.StatusFound) }, ErrRedirect, ""},
		{"307", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/x", http.StatusTemporaryRedirect) }, ErrRedirect, ""},
		{"500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, nil, "status 500"},
		{"204", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }, nil, "status 204"},
		{"declared too large", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "20000")
			_, _ = w.Write([]byte(big))
		}, ErrBodyTooLarge, ""},
		{"chunked too large", func(w http.ResponseWriter, _ *http.Request) {
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte(big))
		}, ErrBodyTooLarge, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				tt.handler(w, r)
			}))
			defer srv.Close()
			c, u := tlsOneClickClient(t, srv)
			_, err := c.PostUpdateCheck(context.Background(), u, []byte(`{}`))
			if err == nil {
				t.Fatal("want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %v, want %q", err, tt.wantMsg)
			}
			if hits.Load() != 1 {
				t.Errorf("server hit %d times, a redirect must not be followed", hits.Load())
			}
		})
	}
}

func TestPostUpdateCheckRefusals(t *testing.T) {
	c := NewClient(Options{})
	tests := []struct{ name, raw string }{
		{"http", "http://example.com/v1/check"},
		{"user info", "https://u:p@example.com/v1/check"},
		{"port", "https://example.com:8080/v1/check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _ := url.Parse(tt.raw)
			if _, err := c.PostUpdateCheck(context.Background(), u, nil); !errors.Is(err, ErrInvalidURL) {
				t.Errorf("err = %v, want ErrInvalidURL", err)
			}
		})
	}
	if _, err := c.PostUpdateCheck(context.Background(), nil, nil); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("nil URL: %v", err)
	}
}

func TestPostUpdateCheckRefusesPrivateAddress(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a private address must not be dialled")
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := NewClient(Options{}) // AllowPrivateNetworks false
	c.extraPorts = map[string]bool{u.Port(): true}
	if _, err := c.PostUpdateCheck(context.Background(), u, nil); err == nil {
		t.Fatal("want the loopback address refused")
	}
}

func TestPostUpdateCheckTimeout(t *testing.T) {
	if updateCheckTimeout != 10*time.Second {
		t.Fatalf("default timeout = %v, want 10s", updateCheckTimeout)
	}
	old := updateCheckTimeout
	updateCheckTimeout = 150 * time.Millisecond
	t.Cleanup(func() { updateCheckTimeout = old })

	// The handlers outlive the client's patience by design.
	stall := func() { time.Sleep(2 * time.Second) }
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"no headers", func(_ http.ResponseWriter, _ *http.Request) { stall() }},
		{"stalled body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{"))
			w.(http.Flusher).Flush()
			stall()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(tt.handler)
			defer srv.Close()
			c, u := tlsOneClickClient(t, srv)
			start := time.Now()
			_, err := c.PostUpdateCheck(context.Background(), u, []byte(`{}`))
			if err == nil {
				t.Fatal("a slow server must fail the check")
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("returned after %v, the timeout did not apply", d)
			}
		})
	}
}
