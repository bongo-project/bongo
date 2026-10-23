package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// freePort returns an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestWebDAVMethodsRegistered(t *testing.T) {
	// chi must route WebDAV methods to a wildcard handler rather than 405ing, or
	// CalDAV/CardDAV clients (which lead with PROPFIND) cannot connect.
	r := chi.NewRouter()
	r.Handle("/caldav/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, method := range []string{"PROPFIND", "REPORT", "MKCALENDAR", "PROPPATCH"} {
		req := httptest.NewRequest(method, "/caldav/user/calendars/", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s returned 405 — chi did not route the WebDAV method", method)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (reached handler)", method, rr.Code)
		}
	}
}

func TestNewAgentDev(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	if a.Store == nil {
		t.Error("expected Store to be non-nil")
	}
	if a.Blobs == nil {
		t.Error("expected Blobs to be non-nil")
	}
	if a.Metadata == nil {
		t.Error("expected Metadata to be non-nil")
	}
	if a.tmpDir == "" {
		t.Error("expected tmpDir to be set in dev mode")
	}

	if _, err := os.Stat(a.tmpDir); os.IsNotExist(err) {
		t.Error("temp dir does not exist")
	}

	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if _, err := os.Stat(a.tmpDir); !os.IsNotExist(err) {
		t.Error("temp dir should have been removed after shutdown")
	}
}

func TestAgentRunAndCancel(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Give agent a moment to start.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestSweepOrphanedUsers(t *testing.T) {
	// Wait for Ready, not just leadership: dev-mode startup keeps writing to
	// system.db after it wins the election, and a sweep DELETE racing those
	// writes fails with SQLITE_BUSY and leaves the row in place.
	a := blobGCLeaderTestAgent(t)

	uid, err := auth.CreateUser(a, a.Metadata, "ghost@localhost", "Ghost", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	rowExists := func() bool {
		var one int
		return a.Metadata.System.DB().QueryRow("SELECT 1 FROM users WHERE id = ?", uid).Scan(&one) == nil
	}
	if !rowExists() {
		t.Fatal("user row should exist after creation")
	}

	// Orphan the user the way older nodes left it: remove only the Pebble
	// profile, bypassing the FSM. A profile delete through Raft now also
	// removes the users row on every node, so it can no longer make an orphan.
	if err := a.Store.Delete([]byte("/user/" + uid + "/profile")); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	// First pass: marks the orphan but does not delete it.
	prev := a.sweepOrphanedUsers(map[string]bool{})
	if !prev[uid] {
		t.Fatal("orphan should be marked on the first pass")
	}
	if !rowExists() {
		t.Fatal("orphan must NOT be deleted on the first pass")
	}

	// Second consecutive pass: deletes the orphaned row.
	a.sweepOrphanedUsers(prev)
	if rowExists() {
		t.Error("orphaned user row should be deleted on the second pass")
	}
}

func TestEnsureConfiguredDomainSeedsOnStartup(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false
	cfg.SMTP.DKIM.Domain = "example.test"

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	// Poll until the configured domain is seeded (leadership + Raft apply are
	// async). The window is deliberately generous: Raft leadership election
	// alone has been measured at ~1.5s on a loaded machine, and this test runs
	// alongside every other package under `go test -race ./...`. A 2.5s bound
	// failed intermittently there while passing in isolation — a bound this
	// test never meant to assert.
	key := []byte("/system/domain/example.test")
	seeded := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := a.Store.Get(key); err == nil {
			seeded = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !seeded {
		cancel()
		<-done
		t.Fatal("configured domain was not seeded into Pebble on startup")
	}

	// Derived SQLite index should carry it too. Poll rather than assert
	// once: the FSM writes Pebble and *then* updates the derived indexes, so
	// the loop above can observe the key mid-Apply, before the SQLite insert
	// has run. Asserting immediately made this fail roughly one run in three.
	var name string
	var indexErr error
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		indexErr = a.Metadata.System.DB().QueryRow(
			"SELECT name FROM domains WHERE name = ?", "example.test").Scan(&name)
		if indexErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if indexErr != nil {
		t.Errorf("configured domain not in SQLite index: %v", indexErr)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestHealthEndpoints(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	port := freePort(t)
	cfg.Web.Port = port
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
	go func() {
		done <- a.Run(ctx)
	}()

	// Dev mode uses plain HTTP (TLS disabled for ephemeral temp storage).
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{}
	waitForHTTPS(t, client, baseURL+"/health/live", 5*time.Second)

	// Test /health/live
	resp, err := client.Get(baseURL + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health/live status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["status"] != "alive" {
		t.Errorf("/health/live status = %q, want %q", body["status"], "alive")
	}

	// Test /health/ready
	resp2, err := client.Get(baseURL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/health/ready status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	// net/http only treats a server connection that has not yet received a
	// request (StateNew) as idle once it is 5s old, and Server.Shutdown waits
	// for every connection to go idle. The client transport can dial a spare
	// connection while waitForHTTPS and the GETs above race for one, then park
	// it unused in its idle pool. Left open, that connection makes Shutdown
	// block ~5s - exactly this test's budget - so Run overran it under load
	// (observed 5.1-7.3s against ~9ms when the machine is quiet), and the late
	// shutdown still held the fixed IMAP/POP3 ports when the next test started.
	// Closing the client's idle connections lets Shutdown return immediately.
	client.CloseIdleConnections()

	cancelAt := time.Now()
	cancel()

	select {
	case err := <-done:
		t.Logf("Run returned %s after cancel", time.Since(cancelAt))
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		var buf bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&buf, 1)
		t.Fatalf("Run did not return after context cancellation; goroutines:\n%s", buf.String())
	}
}

func TestNewAgentServerMode(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Storage.DataDir = t.TempDir()

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	defer a.Shutdown()

	if a.tmpDir != "" {
		t.Error("tmpDir should be empty in server mode")
	}
	if a.Store == nil {
		t.Error("expected Store to be non-nil")
	}
	if a.Blobs == nil {
		t.Error("expected Blobs to be non-nil")
	}
	if a.Metadata == nil {
		t.Error("expected Metadata to be non-nil")
	}
	if a.Raft == nil {
		t.Error("expected Raft to be non-nil in server mode")
	}
}

func TestNewAgentAgentModeSkipsStorage(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "agent"
	cfg.Server.Agents = []string{"antispam"}
	cfg.AgentServer = &config.AgentConfig{ServerAddress: "127.0.0.1:8400"}

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Agent role must NOT initialize local storage.
	if a.Store != nil {
		t.Error("expected Store to be nil in agent mode")
	}
	if a.Blobs != nil {
		t.Error("expected Blobs to be nil in agent mode")
	}
	if a.Metadata != nil {
		t.Error("expected Metadata to be nil in agent mode")
	}
	if a.Search != nil {
		t.Error("expected Search to be nil in agent mode")
	}
	if a.Raft != nil {
		t.Error("expected Raft to be nil in agent mode")
	}
	if a.Auth != nil {
		t.Error("expected Auth to be nil in agent mode")
	}
	if a.tmpDir != "" {
		t.Error("expected tmpDir to be empty in agent mode")
	}
	if a.dataDir != "" {
		t.Error("expected dataDir to be empty in agent mode")
	}

	// Shutdown should work without error even with nil stores.
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestAgentRoleConfigValidation(t *testing.T) {
	// Agent role requires non-empty agents list.
	cfg := config.Defaults()
	cfg.Server.Role = "agent"
	cfg.Server.Agents = nil

	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for agent role without agents list")
	}

	// With valid agents and server address, should pass.
	cfg.Server.Agents = []string{"antispam"}
	cfg.AgentServer = &config.AgentConfig{ServerAddress: "127.0.0.1:8400"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func waitForHTTPS(t *testing.T, client *http.Client, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("HTTPS server not ready at %s after %v", url, timeout)
}

// TestSweepGhostUserTrees covers the shape sweepOrphanedUsers structurally
// cannot see: a /user/{id}/ tree with NO users row and no profile. That is what
// the ghost-mailbox incident produced -- an account was deleted while a stale
// address-index record still routed mail to the dead ID, so delivery kept
// writing fresh keys under a user nothing lists.
func TestSweepGhostUserTrees(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	defer func() { cancel(); <-done }()

	for i := 0; i < 100 && !a.IsLeader(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.IsLeader() {
		t.Fatal("agent did not acquire leadership")
	}

	// A ghost: mail keys under a user with neither profile nor users row.
	const ghost = "33333333-3333-3333-3333-333333333333"
	ghostKey := []byte("/user/" + ghost + "/mail/guid-1")
	if err := a.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: ghostKey, Value: []byte("x"),
	}, 5*time.Second); err != nil {
		t.Fatalf("seed ghost key: %v", err)
	}

	// A real user must survive every pass -- this is the guard that matters,
	// since the sweep deletes stored mail.
	liveID, err := auth.CreateUser(a, a.Metadata, "live@localhost", "Live", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// A user with a users ROW but no profile belongs to sweepOrphanedUsers,
	// which deletes only the row. This sweep must leave its mail alone, or the
	// two sweeps race and this one destroys mail the other never intended to
	// touch. Without this case the hasRow check is unfalsifiable: a healthy
	// live user is already excluded by the profile check that follows it.
	rowOnlyID, err := auth.CreateUser(a, a.Metadata, "rowonly@localhost", "RowOnly", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	rowOnlyMail := []byte("/user/" + rowOnlyID + "/mail/guid-keep")
	if err := a.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: rowOnlyMail, Value: []byte("keep"),
	}, 5*time.Second); err != nil {
		t.Fatalf("seed row-only mail: %v", err)
	}
	// Orphan the user the way older nodes left it: remove only the Pebble
	// profile, bypassing the FSM. A profile delete through Raft now also
	// removes the users row on every node, so it can no longer make an orphan.
	if err := a.Store.Delete([]byte("/user/" + rowOnlyID + "/profile")); err != nil {
		t.Fatalf("delete row-only profile: %v", err)
	}

	ghostExists := func() bool {
		_, gErr := a.Store.Get(ghostKey)
		return gErr == nil
	}
	liveExists := func() bool {
		_, gErr := a.Store.Get([]byte("/user/" + liveID + "/profile"))
		return gErr == nil
	}

	if !ghostExists() {
		t.Fatal("fixture invalid: ghost key was not written")
	}

	// First pass marks only.
	prev := a.sweepGhostUserTrees(map[string]bool{})
	if !prev[ghost] {
		t.Fatal("ghost should be marked on the first pass")
	}
	if prev[liveID] {
		t.Fatal("a live user must never be marked as a ghost")
	}
	if prev[rowOnlyID] {
		t.Fatal("a user with a users row must never be marked as a ghost - that orphan belongs to sweepOrphanedUsers")
	}
	if !ghostExists() {
		t.Fatal("ghost must NOT be deleted on the first pass")
	}

	// Second consecutive pass reclaims it.
	a.sweepGhostUserTrees(prev)
	if ghostExists() {
		t.Error("ghost user tree should be reclaimed on the second pass")
	}
	if !liveExists() {
		t.Error("BUG: the live user's profile was deleted by the ghost sweep")
	}
	if _, gErr := a.Store.Get(rowOnlyMail); gErr != nil {
		t.Error("BUG: mail belonging to a row-only orphan was deleted by the ghost sweep")
	}
}

// orphanLogLines returns the decoded JSON log lines whose msg equals msg.
func orphanLogLines(t *testing.T, buf *syncBuffer, msg string) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(buf.String())))
	var out []map[string]any
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

func TestSweepOrphanedUsersLogsReclaimedUser(t *testing.T) {
	buf := &syncBuffer{}
	a := blobGCLeaderTestAgentWithLogger(t, slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for i := 0; i < 100 && !a.IsLeader(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	uid, err := auth.CreateUser(a, a.Metadata, "ghost2@localhost", "Ghost", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Orphan the user the way older nodes left it: remove only the Pebble
	// profile, bypassing the FSM. A profile delete through Raft now also
	// removes the users row on every node, so it can no longer make an orphan.
	if err := a.Store.Delete([]byte("/user/" + uid + "/profile")); err != nil {
		t.Fatalf("delete profile: %v", err)
	}

	const msg = "orphan-gc: user row reclaimed"
	prev := a.sweepOrphanedUsers(map[string]bool{})
	if n := len(orphanLogLines(t, buf, msg)); n != 0 {
		t.Fatalf("per-item line after first pass: %d, want 0", n)
	}
	a.sweepOrphanedUsers(prev)
	lines := orphanLogLines(t, buf, msg)
	if len(lines) != 1 || lines[0]["user"] != uid {
		t.Fatalf("lines after second pass = %v, want one for user %s", lines, uid)
	}
	if lines[0]["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", lines[0]["level"])
	}
	if _, ok := lines[0]["aliases_deleted"]; !ok {
		t.Error("aliases_deleted missing")
	}
	if n := len(orphanLogLines(t, buf, "orphan-gc: reclaimed orphaned user rows")); n != 1 {
		t.Errorf("summary line count = %d, want 1 (unchanged)", n)
	}
}

func TestSweepOrphanedMailLogsReclaimedGUID(t *testing.T) {
	buf := &syncBuffer{}
	a := blobGCLeaderTestAgentWithLogger(t, slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for i := 0; i < 100 && !a.IsLeader(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	uid, err := auth.CreateUser(a, a.Metadata, "ghost3@localhost", "Ghost", "pass1234", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	const guid = "orphan-guid-1"
	key := "/user/" + uid + "/mail/" + guid
	val, err := proto.Marshal(&bongov1.MailMeta{Guid: guid})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(key), Value: val}, 5*time.Second); err != nil {
		t.Fatalf("put meta: %v", err)
	}

	const msg = "orphan-gc: mail meta reclaimed"
	prev := a.sweepOrphanedMail(map[string]bool{}, 0)
	if !prev[uid+"/"+guid] {
		t.Fatalf("orphan not detected on first pass: %v", prev)
	}
	if n := len(orphanLogLines(t, buf, msg)); n != 0 {
		t.Fatalf("per-item line after first pass: %d, want 0", n)
	}
	a.sweepOrphanedMail(prev, 0)
	lines := orphanLogLines(t, buf, msg)
	if len(lines) != 1 || lines[0]["user"] != uid || lines[0]["guid"] != guid || lines[0]["key"] != key {
		t.Fatalf("lines after second pass = %v", lines)
	}
}

// TestNewAgentReconcilesStaleUserEnabled restarts a server-role agent over a
// data directory whose Pebble profile is disabled but whose users row still
// says enabled=1, as left by a node that missed the disable.
func TestNewAgentReconcilesStaleUserEnabled(t *testing.T) {
	dir := t.TempDir()
	newCfg := func() *config.Config {
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Server.BindAddr = "127.0.0.1"
		cfg.Storage.DataDir = dir
		return cfg
	}

	a, err := NewAgent(newCfg(), testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	prof, _ := proto.Marshal(&bongov1.UserProfile{Email: "stale@example.com", Disabled: true})
	if err := a.Store.Set([]byte("/user/stale1/profile"), prof); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Metadata.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('stale1', 'stale@example.com', 'S', 1, 0)"); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	a2, err := NewAgent(newCfg(), testLogger())
	if err != nil {
		t.Fatalf("NewAgent restart: %v", err)
	}
	defer a2.Shutdown()
	var enabled int
	if err := a2.Metadata.System.DB().QueryRow("SELECT enabled FROM users WHERE id = 'stale1'").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatalf("users.enabled = %d after restart, want 0", enabled)
	}
}

// TestNewAgentReconcilesOrphanUsers restarts a follower-state server agent
// (Run is never called, so the leader-only sweep cannot be what acts) over a
// data directory holding a users row and GAL entry with no Pebble profile.
func TestNewAgentReconcilesOrphanUsers(t *testing.T) {
	dir := t.TempDir()
	newCfg := func() *config.Config {
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Server.BindAddr = "127.0.0.1"
		cfg.Storage.DataDir = dir
		return cfg
	}
	const removedMsg = "orphan users rows removed at startup"

	a, err := NewAgent(newCfg(), testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	live, _ := proto.Marshal(&bongov1.UserProfile{Email: "live@example.com"})
	if err := a.Store.Set([]byte("/user/live1/profile"), live); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][2]string{{"live1", "live@example.com"}, {"orph1", "orphan@example.com"}} {
		if _, err := a.Metadata.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, ?, 1, 0)",
			r[0], r[1], r[0]); err != nil {
			t.Fatal(err)
		}
		if err := a.Search.IndexProfile(r[0], r[1], r[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	var buf bytes.Buffer
	a2, err := NewAgent(newCfg(), slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("NewAgent restart: %v", err)
	}
	if a2.IsLeader() {
		t.Fatal("agent is leader before Run; test would not prove the follower path")
	}
	rows := func(a *Agent, id string) int {
		var c int
		if err := a.Metadata.System.DB().QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if rows(a2, "orph1") != 0 {
		t.Error("orphan users row survived startup")
	}
	if rows(a2, "live1") != 1 {
		t.Error("live users row removed")
	}
	gal, err := a2.Search.SearchGAL("orphan", 10, "x@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(gal) != 0 {
		t.Errorf("orphan GAL entry survived: %v", gal)
	}
	if gal, _ := a2.Search.SearchGAL("live", 10, "x@example.com"); len(gal) != 1 {
		t.Errorf("live GAL entry = %v, want 1", gal)
	}
	if err := auth.CheckAddressFree(a2, a2.Metadata, "orphan@example.com"); err != nil {
		t.Errorf("CheckAddressFree after reconcile = %v, want nil", err)
	}
	if n := strings.Count(buf.String(), removedMsg); n != 1 {
		t.Errorf("removal log lines = %d, want 1:\n%s", n, buf.String())
	}
	if err := a2.Shutdown(); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	a3, err := NewAgent(newCfg(), slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("NewAgent third start: %v", err)
	}
	defer a3.Shutdown()
	if strings.Contains(buf.String(), removedMsg) {
		t.Errorf("removal logged with zero orphans:\n%s", buf.String())
	}
}
