package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const equivSecret = "bulk-equivalence-session-secret-0123456789"

// blockDNS makes every lookup fail at once, so the SPF check on the way in
// resolves nothing and no test traffic leaves the machine.
func blockDNS(t *testing.T) {
	t.Helper()
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("DNS is blocked in this test")
		},
	}
	t.Cleanup(func() { net.DefaultResolver = old })
}

// startDevAgent runs a dev-role agent on loopback ports and returns it with
// the addresses seed needs.
func startDevAgent(t *testing.T, domain string) (a *agent.Agent, control, smtpAddr, imapAddr string, stop func()) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.SessionSecret = equivSecret
	cfg.SMTP.Enabled = true
	cfg.SMTP.Hostname = "bulk.test"
	ports := testutil.FreePorts(t, 10)
	cfg.Web.Port, cfg.SMTP.Port, cfg.SMTP.SubmissionPort = ports[0], ports[1], ports[2]
	cfg.IMAP.Port, cfg.IMAP.PortTLS = ports[3], ports[4]
	cfg.POP3.Port, cfg.POP3.PortTLS = ports[5], ports[6]
	cfg.Cluster.Gossip.Port = ports[7]
	cfg.Server.ControlAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[8]))
	cfg.SMTP.SubmissionsPort = ports[9]

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() {
		select {
		case err := <-done:
			t.Fatalf("agent exited before ready: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("agent not ready in 30s")
		}
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Error("agent Run did not return after cancel")
			}
		})
	}
	t.Cleanup(stop)
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/" + domain), Value: []byte("enabled")}, 5*time.Second); err != nil {
		t.Fatalf("host domain: %v", err)
	}
	return a, cfg.Server.ControlAddr, a.SMTP.MXAddr(), net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.IMAP.Port)), stop
}

func TestBulkLoadMatchesSMTPStore(t *testing.T) {
	blockDNS(t)
	opts := bulkTestOptions()
	opts.Users, opts.MessagesPerUser, opts.Concurrency = 3, 8, 3
	spec := bulkTestSpec(t, opts)
	wantMsgs := spec.Users * spec.MPU

	a, control, smtpAddr, imapAddr, stop := startDevAgent(t, spec.Domain)

	// Fill A the normal way: seed over the control listener, SMTP and IMAP.
	seedOpts := opts
	seedOpts.ControlAddr, seedOpts.SMTPAddr, seedOpts.IMAPAddr, seedOpts.IMAPTLS = control, smtpAddr, imapAddr, imapTLSPlain
	seedOpts.StateFile = filepath.Join(t.TempDir(), "seed.state")
	var allow []Target
	for _, addr := range []string{control, smtpAddr, imapAddr} {
		tg, err := parseTarget(addr)
		if err != nil {
			t.Fatal(err)
		}
		allow = append(allow, tg)
	}
	cfg := Config{Allow: allow, TestDomain: spec.Domain, Seed: seedOpts}
	var out, errb bytes.Buffer
	deps := seedDeps{dial: defaultDial(), secret: []byte(equivSecret), newPassword: NewPassword, backoff: 10 * time.Millisecond, tries: 6}
	if code := seedMain(context.Background(), cfg, deps, &out, &errb); code != exitOK {
		t.Fatalf("seed exit %d: %s %s", code, out.String(), errb.String())
	}

	// SMTP delivery is asynchronous (spool, then the pipeline runner).
	viewA := storeView{ps: a.Store, blobs: a.Blobs, meta: a.Metadata}
	var dumpA *storeDump
	for deadline := time.Now().Add(90 * time.Second); ; {
		a.Raft.FSM.WaitIdle(30 * time.Second)
		d, err := dumpStore(viewA)
		if err != nil {
			t.Fatal(err)
		}
		keepPlanUsers(d, spec)
		if d.Messages >= wantMsgs {
			dumpA = d
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SMTP store holds %d of %d messages after 90s", d.Messages, wantMsgs)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Dumped before Shutdown: the dev role deletes its directory on exit.
	stop()
	// The dev role also creates its own admin account, which is no part of
	// the plan.
	keepPlanUsers(dumpA, spec)

	// Fill B with the loader from the same plan.
	dirB := filepath.Join(t.TempDir(), "data")
	res, err := bulkLoad(context.Background(), spec, bulkOptions{DataDir: dirB, Hostname: "bulk.test", Workers: 3},
		filepath.Join(t.TempDir(), "bulk.state"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loader: %.1f users/s, %.1f messages/s, %.2f MB/s", res.UsersPerSec, res.MessagesPerSec, res.BytesPerSec/(1<<20))
	vb, closeB := openIndexedView(t, dirB)
	defer closeB()
	dumpB, err := dumpStore(vb)
	if err != nil {
		t.Fatal(err)
	}

	// Vacuity guards: both sides hold the whole plan, with index rows.
	for name, d := range map[string]*storeDump{"SMTP store": dumpA, "loaded store": dumpB} {
		if len(d.Users) != spec.Users || d.Messages != wantMsgs {
			t.Fatalf("%s holds %d users %d messages, want %d and %d", name, len(d.Users), d.Messages, spec.Users, wantMsgs)
		}
		for email, u := range d.Users {
			if u.IndexRows != len(u.Messages) {
				t.Errorf("%s: %s has %d SQLite rows for %d messages", name, email, u.IndexRows, len(u.Messages))
			}
		}
	}

	diffs := compareDumps(dumpA, dumpB)
	t.Logf("trace header bytes: SMTP store %d, loaded store %d", traceDeltaTotal(dumpA), traceDeltaTotal(dumpB))
	if len(diffs) != 0 {
		t.Fatalf("stores differ (%d):\n  %s", len(diffs), strings.Join(diffs, "\n  "))
	}

	t.Run("flipped flag is reported", func(t *testing.T) {
		m := cloneDump(dumpB)
		flipFirst(m, func(x *msgDump) { x.Flags ^= 1 })
		if len(compareDumps(dumpA, m)) == 0 {
			t.Fatal("a flag flip went unnoticed")
		}
	})
	t.Run("dropped attachment is reported", func(t *testing.T) {
		m := cloneDump(dumpB)
		if !flipFirst(m, func(x *msgDump) {
			if len(x.Attachments) > 0 {
				x.Attachments = x.Attachments[1:]
			}
		}) {
			t.Skip("no message with an attachment")
		}
		if len(compareDumps(dumpA, m)) == 0 {
			t.Fatal("a dropped attachment went unnoticed")
		}
	})
	t.Run("wrong body size is reported", func(t *testing.T) {
		m := cloneDump(dumpB)
		flipFirst(m, func(x *msgDump) { x.Size++ })
		if len(compareDumps(dumpA, m)) == 0 {
			t.Fatal("a size change went unnoticed")
		}
	})
	t.Run("missing index rows are reported", func(t *testing.T) {
		m := cloneDump(dumpB)
		for _, u := range m.Users {
			u.IndexRows--
			break
		}
		if len(compareDumps(dumpA, m)) == 0 {
			t.Fatal("a loader that skips indexing would pass")
		}
	})
	t.Run("moved message is reported", func(t *testing.T) {
		m := cloneDump(dumpB)
		flipFirst(m, func(x *msgDump) { x.Folder = "Junk" })
		if len(compareDumps(dumpA, m)) == 0 {
			t.Fatal("Junk routing would go unnoticed")
		}
	})
}

// openIndexedView opens a stopped data directory read-only with its SQLite
// index, as the equivalence dump needs.
func openIndexedView(t *testing.T, dir string) (storeView, func()) {
	t.Helper()
	v, closeFn, err := openCheckStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.NewMetadataManager(dir)
	if err != nil {
		closeFn()
		t.Fatal(err)
	}
	v.meta = meta
	return v, func() { _ = meta.Close(); closeFn() }
}

func keepPlanUsers(d *storeDump, spec PlanSpec) {
	want := map[string]bool{}
	for i := range spec.Users {
		want[spec.email(i)] = true
	}
	for email := range d.Users {
		if !want[email] {
			delete(d.Users, email)
		}
	}
	d.Messages = 0
	for _, u := range d.Users {
		d.Messages += len(u.Messages)
	}
}

func cloneDump(d *storeDump) *storeDump {
	c := &storeDump{Users: map[string]*userDump{}, Messages: d.Messages, Problems: slices.Clone(d.Problems)}
	for email, u := range d.Users {
		cu := *u
		cu.Messages = map[string]msgDump{}
		for k, m := range u.Messages {
			m.Attachments = slices.Clone(m.Attachments)
			cu.Messages[k] = m
		}
		cu.Mailboxes = map[string]mailboxDump{}
		for k, m := range u.Mailboxes {
			cu.Mailboxes[k] = m
		}
		c.Users[email] = &cu
	}
	return c
}

// flipFirst applies mut to the first message, in key order, that mut
// changes, and reports whether one did.
func flipFirst(d *storeDump, mut func(*msgDump)) bool {
	for _, email := range sortedKeys(d.Users) {
		u := d.Users[email]
		for _, id := range sortedKeys(u.Messages) {
			m := u.Messages[id]
			before := fmt.Sprintf("%+v", m)
			mut(&m)
			if fmt.Sprintf("%+v", m) != before {
				u.Messages[id] = m
				return true
			}
		}
	}
	return false
}

func TestBulkCheckCommand(t *testing.T) {
	o := bulkTestOptions()
	o.Users, o.MessagesPerUser = 2, 6
	same := bulkTestSpec(t, o)
	o2 := o
	o2.Seed++
	other := bulkTestSpec(t, o2)
	a, _ := loadBulk(t, same)
	b, _ := loadBulk(t, same)
	c, _ := loadBulk(t, other)
	check := func(x, y string) (int, string) {
		var out, errb bytes.Buffer
		code := run([]string{"bulk", "--target-allow", "127.0.0.1", "--test-domain", "bulk.test", "--bulk-check-a", x, "--bulk-check-b", y}, &out, &errb)
		return code, out.String() + errb.String()
	}
	if code, out := check(a, b); code != exitOK || !strings.Contains(out, "stores match") {
		t.Fatalf("equal stores: code %d: %s", code, out)
	}
	code, out := check(a, c)
	if code != exitFail || !strings.Contains(out, "differences") || !strings.Contains(out, "only in") {
		t.Fatalf("different plans: code %d: %s", code, out)
	}
	if code, _ := check(a, filepath.Join(t.TempDir(), "absent")); code != exitRefuse {
		t.Errorf("a missing directory gave code %d, want a refusal", code)
	}
	if code, _ := check(a, a); code != exitRefuse {
		t.Errorf("the same directory twice gave code %d, want a refusal", code)
	}
}

func TestReportCheckRefusesEmptyStores(t *testing.T) {
	empty := &storeDump{Users: map[string]*userDump{}}
	var out, errb bytes.Buffer
	if code := reportCheck(empty, empty, 10, &out, &errb); code == exitOK {
		t.Fatal("two empty stores were reported as matching")
	}
}

func TestStripTraceHeaders(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"none", "From: a\r\n\r\nbody", "From: a\r\n\r\nbody"},
		{"stamped", "Authentication-Results: h; spf=none\r\nX-Spam-Score: 0\r\nReceived: from x\r\n by y\r\nReceived-SPF: none\r\nFrom: a\r\n\r\nbody", "From: a\r\n\r\nbody"},
		{"only a leading run", "From: a\r\nReceived: later\r\n\r\nbody", "From: a\r\nReceived: later\r\n\r\nbody"},
		{"sender header with a trace-like name inside", "X-Spamish: 1\r\nFrom: a\r\n\r\n", "X-Spamish: 1\r\nFrom: a\r\n\r\n"},
	}
	for _, tc := range tests {
		if got := string(stripTraceHeaders([]byte(tc.in))); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
