package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// TestNewJoinRequestSignsWithEffectiveSecret: with web.session_secret empty (dev
// auto-generates one) the join request must still verify against
// RequireInternalAuth built with the effective secret, and must not verify
// against an empty one. Each verification gets its own freshly built request,
// because the internal-auth nonce cache is process-global and a reused request
// would be refused as a replay for the wrong reason.
func TestNewJoinRequestSignsWithEffectiveSecret(t *testing.T) {
	effective := make([]byte, 32)
	if _, err := rand.Read(effective); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Web.SessionSecret = ""
	a := &Agent{Config: cfg, logger: testLogger(), internalSecret: effective}

	serve := func(secret []byte) (*httptest.Server, *bool) {
		reached := new(bool)
		h := middleware.RequireInternalAuth(secret)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			*reached = true
			w.WriteHeader(http.StatusOK)
		}))
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv, reached
	}
	do := func(srv *httptest.Server) int {
		t.Helper()
		req, err := a.newJoinRequest(cluster.MemberInfo{Name: "peer", HTTPAddr: srv.Listener.Addr().String()}, "node-b", "10.0.0.2:7000")
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	good, reached := serve(effective)
	if code := do(good); code != http.StatusOK || !*reached {
		t.Errorf("effective secret: status %d reached=%v, want 200 and reached", code, *reached)
	}
	bad, reachedBad := serve(nil)
	if code := do(bad); code != http.StatusUnauthorized || *reachedBad {
		t.Errorf("empty secret: status %d reached=%v, want 401 and not reached", code, *reachedBad)
	}
}

// TestNothingSignsWithConfiguredSessionSecret keeps the dev-mode bug from
// returning: in non-test code nothing may sign internal requests with
// Config.Web.SessionSecret, and agent.go may mention it only where the
// configured value is validated or read to choose the effective one.
func TestNothingSignsWithConfiguredSessionSecret(t *testing.T) {
	signing := regexp.MustCompile(`(SignInternalRequest|SignBlobSyncRequest|SetSharedSecret)\([^)]*SessionSecret`)
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "worktrees" || n == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := signing.FindIndex(b); loc != nil {
			t.Errorf("%s signs with a configured session secret: %q", path, b[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "Config.Web.SessionSecret") {
			continue
		}
		if strings.Contains(line, "validateSessionSecret(") || strings.Contains(line, "sessionSecret := []byte(") ||
			strings.Contains(line, "a.initSRS([]byte(") || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		t.Errorf("agent.go:%d reads Config.Web.SessionSecret outside startup selection: %s", i+1, strings.TrimSpace(line))
	}
}

// TestInitInternalSecret pins that startup itself fills internalSecret (and the
// SRS key) with the effective secret, not merely that newJoinRequest reads it.
func TestInitInternalSecret(t *testing.T) {
	t.Run("dev empty generates 32 bytes shared with SRS", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Server.Role = "dev"
		cfg.Web.SessionSecret = ""
		a := &Agent{Config: cfg, logger: testLogger()}
		got, err := a.initInternalSecret()
		if err != nil {
			t.Fatal(err)
		}
		if len(a.internalSecret) != 32 || string(got) != string(a.internalSecret) {
			t.Fatalf("internalSecret len %d, returned equal=%v", len(a.internalSecret), string(got) == string(a.internalSecret))
		}
		if a.SRS == nil {
			t.Fatal("SRS not initialised from the effective secret")
		}
	})
	t.Run("server configured secret is used verbatim", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Web.SessionSecret = strings.Repeat("k", 40)
		a := &Agent{Config: cfg, logger: testLogger()}
		if _, err := a.initInternalSecret(); err != nil {
			t.Fatal(err)
		}
		if string(a.internalSecret) != cfg.Web.SessionSecret {
			t.Fatalf("internalSecret = %q, want configured secret", a.internalSecret)
		}
	})
	t.Run("server empty secret is rejected", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Web.SessionSecret = ""
		a := &Agent{Config: cfg, logger: testLogger()}
		if _, err := a.initInternalSecret(); err == nil {
			t.Fatal("want error")
		}
	})
}

// TestRunSetsInternalSecret: Run itself must store the effective secret, not
// just pick it. Without this a dev node keeps internalSecret empty and every
// join it sends goes out unsigned. The field is read only after Run has
// returned, so the read is ordered after the write.
func TestRunSetsInternalSecret(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.SessionSecret = ""
	port := freePort(t)
	cfg.Web.Port = port
	cfg.Server.ControlAddr = freeControlAddr(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false
	cfg.Cluster.Gossip.Port = freePort(t)

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	waitForHTTPS(t, &http.Client{}, fmt.Sprintf("http://127.0.0.1:%d/health/live", port), 5*time.Second)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if len(a.internalSecret) != 32 {
		t.Fatalf("internalSecret len %d after Run, want 32 (auto-generated)", len(a.internalSecret))
	}
	if a.SRS == nil {
		t.Fatal("SRS not initialised by Run")
	}
}
