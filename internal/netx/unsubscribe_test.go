package netx

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tlsOneClickClient trusts the httptest certificate, admits its port and lifts
// the address rule (the server is on loopback). Tests of the address rule use
// NewClient(Options{}) directly.
func tlsOneClickClient(t *testing.T, srv *httptest.Server) (*Client, *url.URL) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(Options{AllowPrivateNetworks: true})
	c.extraPorts = map[string]bool{u.Port(): true}
	c.tlsConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- test server certificate
	return c, u
}

func TestPostOneClickRequestShape(t *testing.T) {
	var got struct {
		method, ctype, ua, body string
		cookie, referer, auth   string
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.ctype, got.ua, got.body = r.Method, r.Header.Get("Content-Type"), r.UserAgent(), string(b)
		got.cookie, got.referer, got.auth = r.Header.Get("Cookie"), r.Header.Get("Referer"), r.Header.Get("Authorization")
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, u := tlsOneClickClient(t, srv)
	u.Path = "/unsub/TOKEN123"
	u.RawQuery = "t=SECRET"

	if err := c.PostOneClick(context.Background(), u); err != nil {
		t.Fatalf("PostOneClick: %v", err)
	}
	if got.method != http.MethodPost || got.body != "List-Unsubscribe=One-Click" || got.ctype != "application/x-www-form-urlencoded" {
		t.Errorf("request = %+v", got)
	}
	if got.ua != "Bongo-Unsubscribe/1" {
		t.Errorf("User-Agent = %q", got.ua)
	}
	if got.cookie != "" || got.referer != "" || got.auth != "" {
		t.Errorf("identifying headers sent: %+v", got)
	}
	// A second call must not carry the cookie the first answer set.
	if err := c.PostOneClick(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if got.cookie != "" {
		t.Errorf("cookie replayed: %q", got.cookie)
	}
}

func TestPostOneClickStatus(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1) }))
	defer target.Close()

	tests := []struct {
		name    string
		status  int
		wantErr bool
		redir   bool
	}{
		{"ok", 200, false, false},
		{"accepted", 202, false, false},
		{"redirect", 302, true, true},
		{"permanent redirect", 308, true, true},
		{"not found", 404, true, false},
		{"server error", 500, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.redir {
					w.Header().Set("Location", target.URL+"/landed")
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			c, u := tlsOneClickClient(t, srv)
			c.extraPorts[mustPort(t, target.URL)] = true
			err := c.PostOneClick(context.Background(), u)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.redir && !errors.Is(err, ErrRedirect) {
				t.Errorf("err = %v, want ErrRedirect", err)
			}
		})
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("redirect target was contacted %d times", n)
	}
}

func mustPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func TestPostOneClickRefusals(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	port := mustPort(t, srv.URL)

	t.Run("http scheme", func(t *testing.T) {
		c, _ := tlsOneClickClient(t, srv)
		err := c.PostOneClick(context.Background(), &url.URL{Scheme: "http", Host: "127.0.0.1:" + port, Path: "/"})
		if !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v, want ErrInvalidURL", err)
		}
	})
	t.Run("user info", func(t *testing.T) {
		c, u := tlsOneClickClient(t, srv)
		u.User = url.UserPassword("a", "b")
		if err := c.PostOneClick(context.Background(), u); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v, want ErrInvalidURL", err)
		}
	})
	t.Run("port outside the allowlist", func(t *testing.T) {
		c := NewClient(Options{})
		err := c.PostOneClick(context.Background(), &url.URL{Scheme: "https", Host: "list.test:8080", Path: "/"})
		if !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v, want ErrInvalidURL", err)
		}
	})
	t.Run("nil URL", func(t *testing.T) {
		if err := NewClient(Options{}).PostOneClick(context.Background(), nil); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("loopback refused by default", func(t *testing.T) {
		c, u := tlsOneClickClient(t, srv)
		strict := NewClient(Options{})
		strict.extraPorts = c.extraPorts
		strict.tlsConfig = c.tlsConfig
		err := strict.PostOneClick(context.Background(), u)
		if !errors.Is(err, ErrForbiddenAddress) {
			t.Fatalf("err = %v, want ErrForbiddenAddress", err)
		}
	})
	if n := hits.Load(); n != 0 {
		t.Errorf("server was contacted %d times by refused requests", n)
	}
}

func TestPostOneClickCapsBodyAndTimes(t *testing.T) {
	t.Run("large body is capped", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chunk := []byte(strings.Repeat("x", 4096))
			for range 100 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}))
		defer srv.Close()
		c, u := tlsOneClickClient(t, srv)
		if err := c.PostOneClick(context.Background(), u); err != nil {
			t.Fatalf("PostOneClick: %v", err)
		}
	})
	t.Run("context cancel stops a slow server", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		c, u := tlsOneClickClient(t, srv)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := c.PostOneClick(ctx, u); err == nil {
			t.Fatal("want an error")
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("slow server was not cut off")
		}
	})
}

func TestPostOneClickErrorsNameOnlyTheHost(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, u := tlsOneClickClient(t, srv)
	u.Path = "/path/SECRETPATH"
	u.RawQuery = "token=SECRETQUERY"
	err := c.PostOneClick(context.Background(), u)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), u.Hostname()) {
		t.Errorf("error %q lacks the host", err)
	}
	for _, secret := range []string{"SECRETPATH", "SECRETQUERY"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks %s", err, secret)
		}
	}

	// A transport failure, where net/http wraps the URL in *url.Error.
	closed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cu, _ := url.Parse(closed.URL)
	closed.Close()
	cu.Path, cu.RawQuery = "/path/SECRETPATH", "token=SECRETQUERY"
	c2 := NewClient(Options{AllowPrivateNetworks: true})
	c2.extraPorts = map[string]bool{cu.Port(): true}
	err = c2.PostOneClick(context.Background(), cu)
	if err == nil {
		t.Fatal("want a dial error")
	}
	for _, secret := range []string{"SECRETPATH", "SECRETQUERY"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("transport error %q leaks %s", err, secret)
		}
	}
}
