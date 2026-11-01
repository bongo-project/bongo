package netx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// A redirect to a reachable server must still be refused when the target
// carries user information: the dialer would allow it, so only the per-hop
// URL validation stops it.
func TestRedirectWithUserinfoRefused(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fine"))
	}))
	defer ok.Close()
	u, _ := url.Parse(ok.URL)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://user:pw@"+u.Host+"/x")
		w.WriteHeader(http.StatusFound)
	}))
	defer redir.Close()
	ru, _ := url.Parse(redir.URL)
	c := NewClient(Options{AllowPrivateNetworks: true})
	c.extraPorts = map[string]bool{u.Port(): true, ru.Port(): true}
	if _, err := c.Fetch(context.Background(), redir.URL+"/a.ics", Validators{}); err == nil {
		t.Fatal("redirect carrying user information was followed")
	}
}
