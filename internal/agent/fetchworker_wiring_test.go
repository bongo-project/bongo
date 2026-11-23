package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/netx"
)

func fetchWiringConfig(t *testing.T, role, key string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = role
	cfg.SMTP.Enabled = false
	if role == "server" {
		cfg.Storage.DataDir = t.TempDir()
	}
	if key != "" {
		cfg.Fetch = &config.FetchConfig{CredentialsKey: key}
	}
	return cfg
}

func randomFetchKey(t *testing.T) (b64 string) {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func fetchWorkersLive(a *Agent) int {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	return a.bgLive["fetch"]
}

// Outside dev, no key means fetch is off: no keyring, no client, one info
// line. The dev role is the only one that invents a key, so the test builds a
// dev agent (it has Raft and a store) and asks for the behaviour of a server.
func TestFetchNotStartedWithoutKey(t *testing.T) {
	logs := &lockedBuffer{}
	cfg := fetchWiringConfig(t, "dev", "")
	isolatePorts(t, cfg)
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown() })
	a.Config.Server.Role = "server"
	if err := a.initFetch(); err != nil {
		t.Fatal(err)
	}
	if a.FetchKeyring != nil || a.fetchClient != nil || a.fetchCancel != nil {
		t.Fatal("fetch state exists without a key")
	}
	if n := fetchWorkersLive(a); n != 0 {
		t.Fatalf("fetch workers = %d, want 0 without a key", n)
	}
	if !strings.Contains(logs.String(), "fetch: off, no fetch.credentials_key") {
		t.Fatalf("no off line in the log:\n%s", logs.String())
	}
}

// Deleting the line in Run that starts the worker, or swapping the safe
// dialer for a plain one, turns this red.
func TestFetchWiredWithSafeDialer(t *testing.T) {
	logs := &lockedBuffer{}
	key := randomFetchKey(t)
	cfg := fetchWiringConfig(t, "dev", key)
	isolatePorts(t, cfg)
	a := runWiringAgent(t, cfg, slog.New(slog.NewTextHandler(logs, nil)), nil)
	if n := fetchWorkersLive(a); n != 1 {
		t.Fatalf("fetch workers = %d, want 1", n)
	}
	if a.fetchCancel == nil {
		t.Fatal("worker has no cancel: Shutdown cannot stop it")
	}
	if a.FetchKeyring == nil || a.Fetch == nil {
		t.Fatal("keyring or store missing")
	}
	d, ok := a.fetchClient.Dialer.(*netx.MailDialer)
	if !ok {
		t.Fatalf("dialer is %T, want *netx.MailDialer", a.fetchClient.Dialer)
	}
	if d.AllowsPrivateNetworks() {
		t.Fatal("private networks allowed by default")
	}
	if a.fetchClient.RootCAs != nil {
		t.Fatal("RootCAs set in production")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.fetchClient.DialIMAP(ctx, fetch.Params{Host: "127.0.0.1", Port: 993, Security: fetch.SecurityTLS, Username: "u", Password: "p"})
	if !errors.Is(err, netx.ErrForbiddenAddress) {
		t.Fatalf("dial 127.0.0.1:993 = %v, want ErrForbiddenAddress", err)
	}
	if got := fetch.Code(err); got != fetch.CodeConnectFailed {
		t.Fatalf("code = %q", got)
	}
	out := logs.String()
	if !strings.Contains(out, "fetch: credentials key loaded") || !strings.Contains(out, a.FetchKeyring.CurrentID()) {
		t.Fatalf("no key-id line:\n%s", out)
	}
	if strings.Contains(out, key) {
		t.Fatal("the key is in the log")
	}
}

func TestFetchAllowPrivateNetworksReachesDialer(t *testing.T) {
	cfg := fetchWiringConfig(t, "dev", randomFetchKey(t))
	isolatePorts(t, cfg)
	cfg.Fetch.AllowPrivateNetworks = true
	a := runWiringAgent(t, cfg, slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)), nil)
	d, ok := a.fetchClient.Dialer.(*netx.MailDialer)
	if !ok || !d.AllowsPrivateNetworks() {
		t.Fatal("fetch.allow_private_networks did not reach the dialer")
	}
}

func TestFetchDevEphemeralKey(t *testing.T) {
	logs := &lockedBuffer{}
	cfg := fetchWiringConfig(t, "dev", "")
	isolatePorts(t, cfg)
	a := runWiringAgent(t, cfg, slog.New(slog.NewTextHandler(logs, nil)), nil)
	if a.FetchKeyring == nil {
		t.Fatal("dev has no keyring")
	}
	if n := fetchWorkersLive(a); n != 1 {
		t.Fatalf("fetch workers = %d, want 1", n)
	}
	out := logs.String()
	if !strings.Contains(out, "fetch: dev mode, generated an ephemeral credentials key") {
		t.Fatalf("no ephemeral warning:\n%s", out)
	}
	if !strings.Contains(out, "ephemeral=true") {
		t.Fatal("key loaded line does not say ephemeral")
	}
}
