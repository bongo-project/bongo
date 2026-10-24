//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/security"
)

const internalRoutesSecret = "internal-routes-test-secret-0123456789"

// internalRoutesAgent is a running agent whose web and control listeners are
// both reachable, so a test can compare the same request on each.
type internalRoutesAgent struct {
	webURL     string
	controlURL string
	cancel     context.CancelFunc
}

// startInternalRoutesAgent brings up a real agent of the given role. This
// deliberately goes through agent.NewAgent + a.Run rather than building a
// router in the test: the invariant under test is that the PRODUCTION wiring
// keeps the control plane off the public listener and behind
// RequireInternalAuth on the control listener, and a test that constructs its
// own router cannot observe that.
//
// The server role advertises loopback with no join list, so it has no peers
// and the peer listener stays closed.
func startInternalRoutesAgent(t *testing.T, role string) internalRoutesAgent {
	t.Helper()

	cfg := config.Defaults()
	cfg.Web.SessionSecret = internalRoutesSecret
	cfg.Server.Role = role
	cfg.Server.BindAddr = "127.0.0.1"
	if role == "server" {
		cfg.Server.Name = "internal-routes-node"
		cfg.Server.AdvertiseAddr = "127.0.0.1"
		cfg.Web.PlainHTTP = true
		cfg.Storage.DataDir = t.TempDir()
		cfg.Cluster.Raft.DataDir = t.TempDir()
		cfg.Cluster.Gossip.EncryptionKey = "AAAAAAAAAAAAAAAAAAAAAA=="
	}
	sharedAssignFreePorts(t, cfg)

	_, cancelFn, _ := sharedStartAgent(t, cfg)

	return internalRoutesAgent{
		webURL:     "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port),
		controlURL: "http://" + cfg.Server.ControlAddr,
		cancel:     cancelFn,
	}
}

type signFunc func(*http.Request, []byte)

type internalRoute struct {
	method string
	path   string
	sign   signFunc
	// devOnly routes exist only in the dev role.
	devOnly bool
	// handlerMay404 marks routes whose handler legitimately answers 404 for
	// the dummy id used here, so a signed 404 on the control port is not read
	// as "route missing".
	handlerMay404 bool
}

func internalRouteTable() []internalRoute {
	blob := "/internal/blob/" + strings.Repeat("a", 64)
	ctl := security.SignInternalRequest
	return []internalRoute{
		{http.MethodPut, "/internal/kv/test%2Fkey", ctl, true, false},
		{http.MethodGet, "/internal/kv/test%2Fkey", ctl, true, true},
		{http.MethodDelete, "/internal/kv/test%2Fkey", ctl, true, false},
		{http.MethodGet, "/internal/cluster/members", ctl, false, false},
		{http.MethodPost, "/internal/cluster/snapshot", ctl, false, false},
		{http.MethodPost, "/internal/user/create", ctl, false, false},
		{http.MethodGet, "/internal/queue/stats", ctl, false, false},
		{http.MethodPost, "/internal/queue/some-id/retry", ctl, false, true},
		{http.MethodDelete, "/internal/queue/some-id", ctl, false, true},
		{http.MethodGet, "/internal/addresses/orphaned", ctl, false, false},
		{http.MethodPost, "/internal/addresses/repair", ctl, false, false},
		{http.MethodGet, "/internal/mail/dangling", ctl, false, false},
		{http.MethodPost, "/internal/mail/purge-dangling", ctl, false, false},
		// Peer routes of a node without peers: no peer listener exists, so
		// they live on the control listener.
		{http.MethodPost, "/internal/cluster/join", ctl, false, false},
		{http.MethodGet, blob, security.SignBlobSyncRequest, false, true},
		{http.MethodHead, blob, security.SignBlobSyncRequest, false, true},
	}
}

func doInternal(t *testing.T, method, url string, body string, sign signFunc) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if sign != nil {
		sign(req, []byte(internalRoutesSecret))
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// TestInternalRoutesLeavePublicListener is the wiring test for the control
// plane split. Every /internal route must answer 404 on the public web port
// even with a VALID signature (so the 404 cannot be explained by auth), must
// answer 401 on the control port unsigned, and must reach its handler on the
// control port when signed.
//
// Unlike /metrics, the control group has no PrivateOnly() IP layer behind it:
// the HMAC plus the loopback-only listener are the controls, so deleting
// either leaves the handler tests green. This table is what notices.
func TestInternalRoutesLeavePublicListener(t *testing.T) {
	for _, role := range []string{"dev", "server"} {
		t.Run(role, func(t *testing.T) {
			ag := startInternalRoutesAgent(t, role)
			defer ag.cancel()

			for _, rt := range internalRouteTable() {
				t.Run(rt.method+" "+rt.path, func(t *testing.T) {
					kvAbsent := rt.devOnly && role != "dev"
					body := ""

					if got := doInternal(t, rt.method, ag.webURL+rt.path, body, rt.sign); got != http.StatusNotFound {
						t.Errorf("web port, validly signed: status = %d, want 404 (control plane must not be on the public listener)", got)
					}
					if got := doInternal(t, rt.method, ag.webURL+rt.path, body, nil); got != http.StatusNotFound {
						t.Errorf("web port, unsigned: status = %d, want 404", got)
					}

					if kvAbsent {
						if got := doInternal(t, rt.method, ag.controlURL+rt.path, body, rt.sign); got != http.StatusNotFound {
							t.Errorf("control port, signed, role %s: status = %d, want 404 (kv is dev-only)", role, got)
						}
						return
					}

					if got := doInternal(t, rt.method, ag.controlURL+rt.path, body, nil); got != http.StatusUnauthorized {
						t.Errorf("control port, unsigned: status = %d, want 401", got)
					}
					if got := doInternal(t, rt.method, ag.controlURL+rt.path, body, func(r *http.Request, _ []byte) {
						r.Header.Set("X-Bongo-Internal-Auth", "not-a-valid-hmac")
					}); got != http.StatusUnauthorized {
						t.Errorf("control port, forged header: status = %d, want 401", got)
					}
					got := doInternal(t, rt.method, ag.controlURL+rt.path, body, rt.sign)
					if got == http.StatusUnauthorized || (got == http.StatusNotFound && !rt.handlerMay404) {
						t.Errorf("control port, signed: status = %d, want the handler to answer", got)
					}
				})
			}
		})
	}
}

// TestInternalRoutesRejectCrossSigned proves the control-plane group and the
// blob-sync group are keyed separately in the production router. A request
// signed for one surface must get 401 on the other, and each surface must
// accept its own signature (so the 401s cannot be explained by a broken route).
func TestInternalRoutesRejectCrossSigned(t *testing.T) {
	ag := startInternalRoutesAgent(t, "dev")
	defer ag.cancel()

	blobPath := "/internal/blob/" + strings.Repeat("a", 64)
	status := func(method, path string, sign signFunc) int {
		return doInternal(t, method, ag.controlURL+path, "", sign)
	}

	if got := status(http.MethodGet, blobPath, security.SignInternalRequest); got != http.StatusUnauthorized {
		t.Errorf("control-plane-signed GET blob = %d, want 401", got)
	}
	if got := status(http.MethodGet, "/internal/cluster/members", security.SignBlobSyncRequest); got != http.StatusUnauthorized {
		t.Errorf("blob-signed GET members = %d, want 401", got)
	}
	if got := status(http.MethodGet, "/internal/kv/test%2Fkey", security.SignBlobSyncRequest); got != http.StatusUnauthorized {
		t.Errorf("blob-signed GET kv = %d, want 401", got)
	}
	// Positive controls: a correctly signed request is authenticated (not 401).
	if got := status(http.MethodGet, blobPath, security.SignBlobSyncRequest); got == http.StatusUnauthorized {
		t.Errorf("blob-signed GET blob = %d, want authenticated", got)
	}
	if got := status(http.MethodGet, "/internal/cluster/members", security.SignInternalRequest); got != http.StatusOK {
		t.Errorf("control-plane-signed GET members = %d, want 200", got)
	}
}
