package netx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsForbiddenAddr(t *testing.T) {
	forbidden := []string{
		"127.0.0.1", "127.255.255.255", "::1",
		"10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"fc00::1", "fd12:3456::1",
		"169.254.169.254", "fe80::1",
		"100.64.0.1", "100.127.255.255",
		"224.0.0.1", "239.255.255.255", "ff02::1",
		"0.0.0.0", "::", "0.1.2.3", "0.255.255.255",
		"198.18.0.1", "198.19.255.255", "2001:2::1",
		"192.0.2.1", "198.51.100.7", "203.0.113.9", "2001:db8::1",
		"255.255.255.255",
		"64:ff9b::7f00:1", "2002:7f00:1::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:192.168.0.1", "::ffff:169.254.169.254",
		"::ffff:100.64.0.1", "::ffff:224.0.0.1", "::ffff:0.0.0.0", "::ffff:198.18.0.1", "::ffff:192.0.2.1",
		"::ffff:172.16.0.1", "::ffff:172.31.255.255", "::ffff:198.51.100.7", "::ffff:203.0.113.9",
		"240.0.0.1", "255.255.255.254", "::ffff:240.0.0.1",
		"192.0.0.1", "192.0.0.255", "::ffff:192.0.0.1",
	}
	for _, s := range forbidden {
		if !IsForbiddenAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be forbidden", s)
		}
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "9.255.255.255", "11.0.0.1", "172.15.255.255", "172.32.0.1",
		"192.167.255.255", "192.169.0.1", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1",
		"223.255.255.255", "2606:4700:4700::1111", "::ffff:8.8.8.8", "2001:4860:4860::8888",
	}
	for _, s := range allowed {
		if IsForbiddenAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://example.com/a.ics", "https://example.com/a.ics", false},
		{"http://example.com/a.ics", "http://example.com/a.ics", false},
		{"webcal://example.com/a.ics", "https://example.com/a.ics", false},
		{"WEBCAL://example.com/a.ics", "https://example.com/a.ics", false},
		{"https://example.com:8443/a.ics", "https://example.com:8443/a.ics", false},
		{"http://example.com:80/a.ics", "http://example.com:80/a.ics", false},
		{"https://example.com:443/a.ics", "https://example.com:443/a.ics", false},
		{"https://example.com:22/a.ics", "", true},
		{"https://example.com:8080/a.ics", "", true},
		{"ftp://example.com/a.ics", "", true},
		{"file:///etc/passwd", "", true},
		{"https://user@example.com/a.ics", "", true},
		{"https://user:pw@example.com/a.ics", "", true},
		{"https:///a.ics", "", true},
		{"example.com/a.ics", "", true},
		{"", "", true},
		{"https://exa\r\nmple.com/", "", true},
		{"https://example.com/a\x00.ics", "", true},
	}
	for _, tc := range tests {
		got, err := ValidateURL(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateURL(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if err == nil && got.String() != tc.want {
			t.Errorf("ValidateURL(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestValidateURLErrorHasNoSecret(t *testing.T) {
	_, err := ValidateURL("ftp://example.com/s3cretpath?key=hunter2")
	assertNoSecret(t, err)
	_, err = ValidateURL("https://u:pw@example.com/s3cretpath?key=hunter2")
	assertNoSecret(t, err)
}

// testClient returns a client whose port allowlist admits the httptest port.
func testClient(t *testing.T, opts Options, srv *httptest.Server) *Client {
	t.Helper()
	c := NewClient(opts)
	u, _ := url.Parse(srv.URL)
	c.extraPorts = map[string]bool{u.Port(): true}
	return c
}

func TestDialerRefusesLoopbackServer(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c := testClient(t, Options{}, srv)
	_, err := c.Fetch(context.Background(), srv.URL+"/secret/path?token=abc", Validators{})
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("error not from the dialer: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("server was reached")
	}
}

// A DNS name that resolves to 127.0.0.1 must be refused by the dialer, after
// resolution, not by a check on the URL text.
func TestDialerRefusesNameResolvingToLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := testClient(t, Options{}, srv)
	_, err := c.Fetch(context.Background(), "http://localhost:"+u.Port()+"/a.ics", Validators{})
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("want ErrForbiddenAddress, got %v", err)
	}
}

func TestAllowPrivateNetworks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"))
	}))
	defer srv.Close()
	c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
	res, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ETag != `"v1"` || !strings.Contains(string(res.Body), "VCALENDAR") {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// allow_private_networks lifts the address rule only, never the port rule.
func TestAllowPrivateDoesNotLiftPortRule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := NewClient(Options{AllowPrivateNetworks: true}) // no test port seam
	_, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{})
	if err == nil {
		t.Fatal("port outside the allowlist was fetched")
	}
}

// The dialer re-checks the port, so a hop that URL validation missed still
// cannot connect.
func TestDialerRefusesForbiddenPort(t *testing.T) {
	c := NewClient(Options{AllowPrivateNetworks: true})
	_, err := c.dialer().DialContext(context.Background(), "tcp", "127.0.0.1:22")
	if !errors.Is(err, ErrForbiddenPort) {
		t.Fatalf("want ErrForbiddenPort, got %v", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	var n atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()
	c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
	_, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("want redirect limit error, got %v", err)
	}
	if got := n.Load(); got != 4 { // the original plus 3 followed; the 4th hop is refused
		t.Fatalf("hits = %d, want 4", got)
	}
}

func TestRedirectTargetsAreRevalidated(t *testing.T) {
	for _, target := range []string{
		"ftp://example.com/x",
		"https://user:pw@example.com/x",
		"https://example.com:22/x",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target)
			w.WriteHeader(http.StatusFound)
		}))
		c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
		_, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{})
		srv.Close()
		if err == nil {
			t.Errorf("redirect to %s followed", target)
		}
	}
}

func TestBodyOverLimitIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1025)))
	}))
	defer srv.Close()
	c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
	c.maxBody = 1024
	_, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("want ErrBodyTooLarge, got %v", err)
	}
	// Chunked: no Content-Length to reject up front, so the read limit is
	// what stops it.
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 600)))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(strings.Repeat("x", 600)))
	}))
	defer chunked.Close()
	cc := testClient(t, Options{AllowPrivateNetworks: true}, chunked)
	cc.maxBody = 1024
	if _, err := cc.Fetch(context.Background(), chunked.URL+"/a.ics", Validators{}); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("chunked: want ErrBodyTooLarge, got %v", err)
	}
	// Exactly at the limit is accepted.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
	}))
	defer srv2.Close()
	c2 := testClient(t, Options{AllowPrivateNetworks: true}, srv2)
	c2.maxBody = 1024
	res, err := c2.Fetch(context.Background(), srv2.URL+"/a.ics", Validators{})
	if err != nil || len(res.Body) != 1024 {
		t.Fatalf("at-limit body: %v %v", err, res)
	}
}

func TestConditionalGet(t *testing.T) {
	var gotINM, gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM, gotIMS = r.Header.Get("If-None-Match"), r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()
	c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
	res, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{ETag: `"abc"`, LastModified: "Mon, 02 Jan 2006 15:04:05 GMT"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.NotModified {
		t.Fatal("304 not reported")
	}
	if gotINM != `"abc"` || gotIMS != "Mon, 02 Jan 2006 15:04:05 GMT" {
		t.Fatalf("validators not sent: %q %q", gotINM, gotIMS)
	}
}

func TestNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
	if _, err := c.Fetch(context.Background(), srv.URL+"/a.ics", Validators{}); err == nil {
		t.Fatal("403 accepted")
	}
}

// Neither the path nor the query of the secret URL may reach an error.
func TestErrorsCarryHostOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	secret := "/s3cretpath/tok3nvalue?key=hunter2"
	// Dialer refusal.
	c := testClient(t, Options{}, srv)
	_, err := c.Fetch(context.Background(), srv.URL+secret, Validators{})
	assertNoSecret(t, err)
	// Redirect to a secret-bearing target that fails validation.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "ftp://example.com"+secret)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv2.Close()
	c2 := testClient(t, Options{AllowPrivateNetworks: true}, srv2)
	_, err = c2.Fetch(context.Background(), srv2.URL+secret, Validators{})
	assertNoSecret(t, err)
	// Validation failure.
	_, err = c.Fetch(context.Background(), "https://example.com:22"+secret, Validators{})
	assertNoSecret(t, err)
	// Closed port: connection refused.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	c3 := NewClient(Options{AllowPrivateNetworks: true})
	_, p, _ := net.SplitHostPort(addr)
	c3.extraPorts = map[string]bool{p: true}
	_, err = c3.Fetch(context.Background(), "http://"+addr+secret, Validators{})
	assertNoSecret(t, err)
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, s := range []string{"s3cretpath", "tok3nvalue", "hunter2"} {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("error leaks the URL: %v", err)
		}
	}
}

func TestContextCancelStopsFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := testClient(t, Options{AllowPrivateNetworks: true}, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Fetch(ctx, srv.URL+"/a.ics", Validators{}); err == nil {
		t.Fatal("expected error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("fetch ignored the context")
	}
}

func TestProxyEnvironmentIgnored(t *testing.T) {
	c := NewClient(Options{})
	if c.http.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("transport must not use a proxy: it would bypass the dialer check")
	}
}
