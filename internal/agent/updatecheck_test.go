package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/updatecheck"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// memKV is an in-memory stand-in for Pebble that applies plain PUTs, as the
// FSM does for these two keys. Shared by every checker of a test cluster.
type memKV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemKV() *memKV { return &memKV{m: map[string][]byte{}} }

func (k *memKV) Get(key []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[string(key)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (k *memKV) Apply(cmd *bongov1.RaftCommand, _ time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if cmd.Op != bongov1.Op_OP_PUT {
		return errors.New("unexpected op")
	}
	k.m[string(cmd.Key)] = bytes.Clone(cmd.Value)
	return nil
}

// fakePoster records every request and answers from reply.
type fakePoster struct {
	mu     sync.Mutex
	bodies [][]byte
	times  []time.Time
	now    func() time.Time
	reply  func(n int) ([]byte, error)
}

func (p *fakePoster) PostUpdateCheck(_ context.Context, _ *url.URL, body []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies = append(p.bodies, bytes.Clone(body))
	if p.now != nil {
		p.times = append(p.times, p.now())
	}
	if p.reply == nil {
		return []byte(`{"latest":"0.17.1","release_notes_url":"https://update.example.test/releases/0.17.1"}`), nil
	}
	return p.reply(len(p.bodies))
}

func (p *fakePoster) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

func debugLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

var testCheckURL = &url.URL{Scheme: "https", Host: "update.example.test", Path: "/v1/check"}

// newTestChecker builds a checker over kv that leads in term "1" and uses a
// deterministic jitter (the midpoint of the range).
func newTestChecker(kv *memKV, p updatePoster, logw io.Writer) *updateChecker {
	return &updateChecker{
		enabled:  true,
		checkURL: testCheckURL,
		version:  "v0.17.0",
		poster:   p,
		store:    kv,
		apply:    kv.Apply,
		leader:   func() (string, bool) { return "1", true },
		barrier:  func(time.Duration) error { return nil },
		jitter:   func(max time.Duration) time.Duration { return max / 2 },
		newID:    randomID,
		logger:   debugLogger(logw),
	}
}

var simStart = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

// simulate calls step once a minute for d, returning the final time.
func simulate(u *updateChecker, from time.Time, d time.Duration, onTick func(now time.Time)) {
	for t := from; t.Before(from.Add(d)); t = t.Add(time.Minute) {
		if onTick != nil {
			onTick(t)
		}
		u.step(context.Background(), t)
	}
}

// serverPoster posts to a counting httptest server, so the count is of what
// reached the wire and not of calls to a fake.
type serverPoster struct{ url string }

func (p serverPoster) PostUpdateCheck(ctx context.Context, _ *url.URL, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- loopback test server
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func TestUpdateCheckDisabledNeverRequests(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"latest":"0.17.1","release_notes_url":"https://update.example.test/r"}`))
	}))
	defer srv.Close()

	for _, tt := range []struct {
		name    string
		enabled bool
		want    int64
	}{
		{"disabled", false, 0},
		{"enabled control", true, 7}, // the server does see requests when on
	} {
		t.Run(tt.name, func(t *testing.T) {
			hits.Store(0)
			kv := newMemKV()
			u := newTestChecker(kv, serverPoster{url: srv.URL}, io.Discard)
			u.enabled = tt.enabled
			simulate(u, simStart, 7*24*time.Hour, nil)
			if got := hits.Load(); got < tt.want-1 || got > tt.want || (tt.want == 0 && got != 0) {
				t.Fatalf("server saw %d requests over a week, want %d", got, tt.want)
			}
			if _, err := kv.Get([]byte(updatecheck.KeyInstanceID)); tt.enabled == errors.Is(err, store.ErrNotFound) {
				t.Fatalf("instance id state wrong for enabled=%v: %v", tt.enabled, err)
			}
		})
	}
}

// The start-time gate: a server-role agent with update_check=false starts no
// worker, and the same agent with it on does (the control).
func TestUpdateCheckServerRoleStartGate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
	}{{"off", false}, {"on", true}} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			isolatePorts(t, cfg)
			cfg.Server.Role = "server"
			cfg.Storage.DataDir = t.TempDir()
			cfg.Server.UpdateCheck = tt.enabled
			a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatalf("NewAgent: %v", err)
			}
			defer a.Shutdown()
			a.startUpdateCheck(context.Background())
			if got := a.updateCheckCancel != nil; got != tt.enabled {
				t.Errorf("worker started = %v, want %v", got, tt.enabled)
			}
			if a.updateCheckCancel != nil {
				a.updateCheckCancel()
			}
		})
	}
}

func TestUpdateCheckDevRoleStartsNoWorker(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.Server.UpdateCheck = true
	if cfg.UpdateCheckEnabled() {
		t.Fatal("dev role must not enable the check")
	}
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()
	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent not ready")
	}
	if a.updateCheckCancel != nil {
		t.Error("dev role started the update check")
	}
	_ = a.Shutdown()
	<-runDone
}

func TestUpdateCheckRequestBody(t *testing.T) {
	kv := newMemKV()
	p := &fakePoster{}
	u := newTestChecker(kv, p, io.Discard)
	simulate(u, simStart, 2*time.Hour, nil)
	if p.count() != 1 {
		t.Fatalf("requests = %d, want 1", p.count())
	}
	var m map[string]any
	if err := json.Unmarshal(p.bodies[0], &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["version"] != "0.17.0" {
		t.Fatalf("body = %s", p.bodies[0])
	}
	id, _ := m["instance_id"].(string)
	stored, _ := kv.Get([]byte(updatecheck.KeyInstanceID))
	if !updatecheck.ValidInstanceID(id) || id != string(stored) {
		t.Fatalf("instance_id %q, stored %q", id, stored)
	}
	rec, ok := updatecheck.DecodeRecord(kv.m[updatecheck.KeyResult])
	if !ok || rec.Result != updatecheck.ResultOK || rec.Latest != "0.17.1" || rec.CheckedAt == 0 {
		t.Fatalf("record = %+v", rec)
	}
}

// TestUpdateCheckOnlyLeaderChecksOncePerDay simulates three nodes sharing one
// store and a leadership oracle that rotates mid-week. It is a worker-level
// simulation, not a real three-node Raft cluster.
func TestUpdateCheckOnlyLeaderChecksOncePerDay(t *testing.T) {
	kv := newMemKV()
	var (
		leaderIdx = 0
		term      = 1
		clock     time.Time
	)
	p := &fakePoster{now: func() time.Time { return clock }}
	nodes := make([]*updateChecker, 3)
	for i := range nodes {
		n := newTestChecker(kv, p, io.Discard)
		n.leader = func() (string, bool) {
			if i != leaderIdx {
				return "", false
			}
			return fmt.Sprint(term), true
		}
		nodes[i] = n
	}
	for tick := simStart; tick.Before(simStart.Add(7 * 24 * time.Hour)); tick = tick.Add(time.Minute) {
		clock = tick
		if tick.Sub(simStart) == 3*24*time.Hour+7*time.Hour {
			leaderIdx, term = 2, 2
		}
		for _, n := range nodes {
			n.step(context.Background(), tick)
		}
	}
	if c := p.count(); c < 6 || c > 7 {
		t.Fatalf("requests over a week = %d, want 6 or 7", c)
	}
	perDay := map[string]int{}
	for _, at := range p.times {
		perDay[at.Format("2006-01-02")]++
	}
	for day, n := range perDay {
		if n > 1 {
			t.Errorf("%d requests on %s", n, day)
		}
	}
	ids := map[string]bool{}
	for _, b := range p.bodies {
		var r updatecheck.Request
		_ = json.Unmarshal(b, &r)
		ids[r.InstanceID] = true
	}
	if len(ids) != 1 {
		t.Errorf("%d distinct instance ids, want 1", len(ids))
	}
}

func TestUpdateCheckSchedule(t *testing.T) {
	kv := newMemKV()
	var clock time.Time
	p := &fakePoster{now: func() time.Time { return clock }, reply: func(n int) ([]byte, error) {
		return nil, errors.New("boom") // every attempt fails
	}}
	u := newTestChecker(kv, p, io.Discard)
	// Real jitter, to check the bounds rather than one fixed point.
	u.jitter = randomJitter
	simulate(u, simStart, 8*24*time.Hour, func(now time.Time) { clock = now })
	if len(p.times) < 4 {
		t.Fatalf("only %d attempts in 8 days", len(p.times))
	}
	if first := p.times[0].Sub(simStart); first < 5*time.Minute || first > 60*time.Minute+time.Minute {
		t.Errorf("first check after %v, want 5m to 60m", first)
	}
	for i := 1; i < len(p.times); i++ {
		gap := p.times[i].Sub(p.times[i-1])
		if gap < 24*time.Hour || gap > 26*time.Hour+time.Minute {
			t.Errorf("gap %d = %v, want 24h to 26h after a failed attempt too", i, gap)
		}
	}
}

func TestUpdateCheckNewLeaderKeepsDailyFloor(t *testing.T) {
	kv := newMemKV()
	last := simStart.Add(-time.Hour)
	rec, _ := json.Marshal(updatecheck.Record{CheckedAt: last.Unix(), Result: updatecheck.ResultOK})
	kv.m[updatecheck.KeyResult] = rec
	p := &fakePoster{}
	u := newTestChecker(kv, p, io.Discard)
	simulate(u, simStart, 22*time.Hour, nil)
	if p.count() != 0 {
		t.Fatalf("a new leader called home %v after the last attempt", simStart.Sub(last))
	}
	simulate(u, simStart.Add(22*time.Hour), 3*time.Hour, nil)
	if p.count() != 1 {
		t.Fatalf("requests = %d, want 1 once the day has passed", p.count())
	}
}

func TestUpdateCheckInstanceIDIsWrittenOnce(t *testing.T) {
	t.Run("existing value is kept", func(t *testing.T) {
		kv := newMemKV()
		const id = "0123456789abcdef0123456789abcdef"
		kv.m[updatecheck.KeyInstanceID] = []byte(id)
		p := &fakePoster{}
		simulate(newTestChecker(kv, p, io.Discard), simStart, 2*time.Hour, nil)
		if got := string(kv.m[updatecheck.KeyInstanceID]); got != id {
			t.Fatalf("instance id replaced: %q", got)
		}
		if !strings.Contains(string(p.bodies[0]), id) {
			t.Fatalf("body %s does not carry the stored id", p.bodies[0])
		}
	})
	t.Run("malformed value is not overwritten and logged without it", func(t *testing.T) {
		kv := newMemKV()
		kv.m[updatecheck.KeyInstanceID] = []byte("SECRET-NOT-HEX")
		p := &fakePoster{}
		lb := &logBuf{}
		simulate(newTestChecker(kv, p, lb), simStart, 3*24*time.Hour, nil)
		if p.count() != 0 {
			t.Fatalf("%d requests with a malformed instance id", p.count())
		}
		if string(kv.m[updatecheck.KeyInstanceID]) != "SECRET-NOT-HEX" {
			t.Fatal("malformed id was overwritten")
		}
		if strings.Contains(lb.String(), "SECRET-NOT-HEX") {
			t.Fatal("the stored value reached the log")
		}
	})
}

func TestUpdateCheckNonReleaseVersionMakesNoRequest(t *testing.T) {
	for _, v := range []string{"dev", "v0.17.0-3-gabcdef-dirty-x_y", "", "v1.2", "v0.17.0-3-gabcdef", "v0.17.0-3-gabcdef-dirty", "v0.17.0-dirty"} {
		kv := newMemKV()
		p := &fakePoster{}
		u := newTestChecker(kv, p, io.Discard)
		u.version = v
		simulate(u, simStart, 24*time.Hour, nil)
		if p.count() != 0 {
			t.Errorf("version %q made %d requests", v, p.count())
		}
	}
}

func TestUpdateCheckResponseValidation(t *testing.T) {
	good := `"release_notes_url":"https://update.example.test/r/1"`
	tests := []struct {
		name  string
		reply func(int) ([]byte, error)
		want  string // expected stored result
	}{
		{"bad semver", func(int) ([]byte, error) { return []byte(`{"latest":"1.2.x",` + good + `}`), nil }, updatecheck.ResultInvalid},
		{"v prefix", func(int) ([]byte, error) { return []byte(`{"latest":"v1.2.3",` + good + `}`), nil }, updatecheck.ResultInvalid},
		{"dev", func(int) ([]byte, error) { return []byte(`{"latest":"dev",` + good + `}`), nil }, updatecheck.ResultInvalid},
		{"missing latest", func(int) ([]byte, error) { return []byte(`{` + good + `}`), nil }, updatecheck.ResultInvalid},
		{"trailing JSON", func(int) ([]byte, error) { return []byte(`{"latest":"9.0.0",` + good + `}{}`), nil }, updatecheck.ResultInvalid},
		{"not JSON", func(int) ([]byte, error) { return []byte(`<html>`), nil }, updatecheck.ResultInvalid},
		{"http URL", func(int) ([]byte, error) {
			return []byte(`{"latest":"9.0.0","release_notes_url":"http://update.example.test/r"}`), nil
		}, updatecheck.ResultInvalid},
		{"other host", func(int) ([]byte, error) {
			return []byte(`{"latest":"9.0.0","release_notes_url":"https://evil.example/r"}`), nil
		}, updatecheck.ResultInvalid},
		{"other port", func(int) ([]byte, error) {
			return []byte(`{"latest":"9.0.0","release_notes_url":"https://update.example.test:8443/r"}`), nil
		}, updatecheck.ResultInvalid},
		{"user information", func(int) ([]byte, error) {
			return []byte(`{"latest":"9.0.0","release_notes_url":"https://u@update.example.test/r"}`), nil
		}, updatecheck.ResultInvalid},
		{"oversized", func(int) ([]byte, error) { return nil, fmt.Errorf("x: %w", netx.ErrBodyTooLarge) }, updatecheck.ResultTooLarge},
		{"redirect", func(int) ([]byte, error) { return nil, fmt.Errorf("x: %w", netx.ErrRedirect) }, updatecheck.ResultRedirect},
		{"transport error", func(int) ([]byte, error) { return nil, errors.New("dial failed") }, updatecheck.ResultError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv := newMemKV()
			p := &fakePoster{reply: tt.reply}
			lb := &logBuf{}
			simulate(newTestChecker(kv, p, lb), simStart, 4*time.Hour, nil)
			if p.count() != 1 {
				t.Fatalf("requests = %d, want exactly 1 (no extra retry)", p.count())
			}
			rec, ok := updatecheck.DecodeRecord(kv.m[updatecheck.KeyResult])
			if !ok || rec.Result != tt.want {
				t.Fatalf("record = %+v, want result %s", rec, tt.want)
			}
			if rec.Latest != "" || rec.ReleaseNotesURL != "" {
				t.Errorf("banner data stored for a discarded answer: %+v", rec)
			}
			if n := strings.Count(lb.String(), "response discarded"); n != 1 {
				t.Errorf("%d discard lines, want 1:\n%s", n, lb.String())
			}
			if strings.Contains(lb.String(), "level=INFO") || strings.Contains(lb.String(), "level=WARN") {
				t.Errorf("discard must log at debug only:\n%s", lb.String())
			}
		})
	}
}

func TestUpdateCheckFailureKeepsKnownRelease(t *testing.T) {
	kv := newMemKV()
	p := &fakePoster{reply: func(n int) ([]byte, error) {
		if n == 1 {
			return []byte(`{"latest":"0.17.2","release_notes_url":"https://update.example.test/r","security":true}`), nil
		}
		return nil, errors.New("down")
	}}
	u := newTestChecker(kv, p, io.Discard)
	simulate(u, simStart, 3*24*time.Hour, nil)
	if p.count() < 2 {
		t.Fatalf("requests = %d", p.count())
	}
	rec, _ := updatecheck.DecodeRecord(kv.m[updatecheck.KeyResult])
	if rec.Result != updatecheck.ResultError || rec.Latest != "0.17.2" || !rec.Security {
		t.Fatalf("record = %+v, want the known security release kept after a failure", rec)
	}
}

func TestUpdateCheckerWiring(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	a := &Agent{Config: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	u := a.newUpdateChecker(netx.NewClient(netx.Options{}), testCheckURL, "v0.17.0")
	if !u.enabled || u.jitter == nil || u.newID == nil || u.apply == nil {
		t.Fatalf("checker not fully wired: %+v", u)
	}
	if _, ok := u.poster.(*netx.Client); !ok {
		t.Fatalf("poster is %T, want the netx safe client", u.poster)
	}
	cfg.Server.UpdateCheck = false
	if a.newUpdateChecker(netx.NewClient(netx.Options{}), testCheckURL, "v0.17.0").enabled {
		t.Fatal("update_check=false must disable the worker")
	}
}

// Leadership moves while the request is in flight. The new leader must see
// the attempt and keep its 24 hour floor.
func TestUpdateCheckLeaderFlapMidRequestKeepsFloor(t *testing.T) {
	kv := newMemKV()
	var (
		leaderIdx = 0
		term      = 1
		clock     time.Time
		flapped   bool
	)
	p := &fakePoster{now: func() time.Time { return clock }}
	p.reply = func(n int) ([]byte, error) {
		if n == 1 && !flapped {
			flapped = true
			leaderIdx, term = 1, 2 // the other node takes over during the request
		}
		return []byte(`{"latest":"0.17.1","release_notes_url":"https://update.example.test/r"}`), nil
	}
	nodes := make([]*updateChecker, 2)
	for i := range nodes {
		n := newTestChecker(kv, p, io.Discard)
		n.leader = func() (string, bool) {
			if i != leaderIdx {
				return "", false
			}
			return fmt.Sprint(term), true
		}
		nodes[i] = n
	}
	var firstAt time.Time
	for tick := simStart; tick.Before(simStart.Add(23 * time.Hour)); tick = tick.Add(time.Minute) {
		clock = tick
		for _, n := range nodes {
			n.step(context.Background(), tick)
		}
		if firstAt.IsZero() && p.count() == 1 {
			firstAt = tick
		}
	}
	if c := p.count(); c != 1 {
		t.Fatalf("%d requests within 23 hours of a leadership flap, want 1", c)
	}
	rec, ok := nodes[1].readRecord()
	if !ok || rec.CheckedAt != firstAt.Unix() {
		t.Fatalf("recorded attempt = %+v ok=%v, want checked_at %d", rec, ok, firstAt.Unix())
	}
}

// An attempt that cannot be recorded is not made.
func TestUpdateCheckUnrecordedAttemptMakesNoRequest(t *testing.T) {
	kv := newMemKV()
	p := &fakePoster{}
	u := newTestChecker(kv, p, io.Discard)
	u.apply = func(cmd *bongov1.RaftCommand, d time.Duration) error {
		if string(cmd.Key) == updatecheck.KeyResult {
			return errors.New("raft unavailable")
		}
		return kv.Apply(cmd, d)
	}
	simulate(u, simStart, 3*time.Hour, nil)
	if p.count() != 0 {
		t.Fatalf("%d requests although the attempt could not be recorded", p.count())
	}
}
