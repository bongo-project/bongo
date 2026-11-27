package netx

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestACMEClientRefusesPrivateAndWrongPorts(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	c := NewACMEClient()
	tests := []struct {
		name string
		url  string
		want error
	}{
		{"loopback on the test port", srv.URL, ErrForbiddenPort},
		{"loopback on 443", "https://127.0.0.1:443/directory", ErrForbiddenAddress},
		{"private on 443", "https://10.0.0.1:443/directory", ErrForbiddenAddress},
		{"public address, wrong port", "https://93.184.216.34:8443/directory", ErrForbiddenPort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Get(tt.url)
			if err == nil || !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestACMEClientSettings(t *testing.T) {
	c := NewACMEClient()
	if c.Timeout <= 0 || c.Timeout > time.Minute {
		t.Errorf("client timeout = %v", c.Timeout)
	}
	lt, ok := c.Transport.(limitTransport)
	if !ok {
		t.Fatalf("transport is %T, want the body-limiting wrapper", c.Transport)
	}
	tr, ok := lt.inner.(*http.Transport)
	if !ok {
		t.Fatalf("inner transport is %T", lt.inner)
	}
	if tr.Proxy != nil {
		t.Error("the ACME client must not use a proxy")
	}
	if tr.TLSHandshakeTimeout <= 0 || tr.ResponseHeaderTimeout <= 0 {
		t.Error("handshake and response-header timeouts must be set")
	}
	if c.CheckRedirect == nil {
		t.Fatal("redirects must be handled explicitly")
	}
	if err := c.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect = %v, want ErrUseLastResponse", err)
	}
}

func TestACMEClientCapsResponseBodies(t *testing.T) {
	big := strings.Repeat("a", maxACMEBody+10)
	small := strings.Repeat("b", 1000)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			_, _ = io.WriteString(w, small)
		case "/chunked":
			// No Content-Length: the cap must hold while streaming.
			f := w.(http.Flusher)
			for range 20 {
				_, _ = io.WriteString(w, strings.Repeat("c", 100<<10))
				f.Flush()
			}
		default:
			_, _ = io.WriteString(w, big)
		}
	}))
	defer srv.Close()

	// The production wrapper over a transport that may reach the loopback test
	// server (the real dialer refuses it).
	inner := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- test server
	c := &http.Client{Transport: limitTransport{inner: inner, max: maxACMEBody}}

	resp, err := c.Get(srv.URL + "/small")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(got) != small {
		t.Fatalf("small body: %d bytes, %v", len(got), err)
	}

	for _, path := range []string{"/big", "/chunked"} {
		t.Run(path, func(t *testing.T) {
			resp, err := c.Get(srv.URL + path)
			if err != nil {
				if !errors.Is(err, ErrACMEResponseTooLarge) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			defer resp.Body.Close()
			n, err := io.Copy(io.Discard, resp.Body)
			if !errors.Is(err, ErrACMEResponseTooLarge) {
				t.Fatalf("read %d bytes, err = %v, want ErrACMEResponseTooLarge", n, err)
			}
			if n > maxACMEBody {
				t.Errorf("delivered %d bytes, over the cap", n)
			}
		})
	}
}
