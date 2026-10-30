package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/security"
)

// TestClusterStatusSignsItsRequest pins that "cluster status" authenticates.
//
// It used to call http.Get with no signature, while
// /internal/cluster/members sits behind RequireInternalAuth. On any node with
// web.session_secret set -- which is every production node -- the command
// returned "unexpected status 401: Unauthorized" and was simply unusable
// there. It went unnoticed because a dev node with no secret answers happily.
//
// The server here rejects an unsigned request exactly as the real middleware
// does, so reverting the fix fails this test rather than passing quietly.
func TestClusterStatusSignsItsRequest(t *testing.T) {
	const secret = "test-session-secret"

	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(security.InternalAuthHeader) == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Unauthorized"))
			return
		}
		sawAuth = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}

	// The command reads package-level flag vars; restore them for other tests.
	prevAddr, prevSecret := clusterHTTPAddr, clusterSessionSecret
	t.Cleanup(func() { clusterHTTPAddr, clusterSessionSecret = prevAddr, prevSecret })
	clusterHTTPAddr = u.Host
	clusterSessionSecret = secret

	if err := runClusterStatus(nil, nil); err != nil {
		t.Fatalf("runClusterStatus: %v", err)
	}
	if !sawAuth {
		t.Error("the request carried no internal-auth header, so it would 401 against any " +
			"node with web.session_secret set")
	}
}

// TestClusterStatusReportsAuthFailureUsefully: without a secret the command
// must say what to do, not just surface a bare 401. An operator hitting this
// has no way to guess that a flag exists.
func TestClusterStatusReportsAuthFailureUsefully(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Unauthorized"))
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}

	prevAddr, prevSecret := clusterHTTPAddr, clusterSessionSecret
	t.Cleanup(func() { clusterHTTPAddr, clusterSessionSecret = prevAddr, prevSecret })
	clusterHTTPAddr = u.Host
	clusterSessionSecret = ""
	t.Setenv("BONGO_WEB_SESSION_SECRET", "")

	err = runClusterStatus(nil, nil)
	if err == nil {
		t.Fatal("expected an error when the node rejects the request")
	}
	if !strings.Contains(err.Error(), "session-secret") {
		t.Errorf("error does not tell the operator how to authenticate: %v", err)
	}
}
