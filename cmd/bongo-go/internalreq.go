package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/bongo-project/bongo/internal/security"
)

// signedInternalRequest performs an HMAC-signed call to an /internal/ endpoint
// on the local node.
//
// secret is the command's --session-secret flag value; an empty one falls back
// to BONGO_WEB_SESSION_SECRET. httpAddr is used only to name the node in the
// connection error. A 401 is turned into an actionable message here because
// every caller needs the same one.
func signedInternalRequest(method, url, httpAddr, secret string) (*http.Response, error) {
	if secret == "" {
		secret = os.Getenv("BONGO_WEB_SESSION_SECRET")
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if secret != "" {
		security.SignInternalRequest(req, []byte(secret))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", httpAddr, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if err := resp.Body.Close(); err != nil {
			return nil, fmt.Errorf("closing response body: %w", err)
		}
		return nil, fmt.Errorf("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	}
	return resp, nil
}
