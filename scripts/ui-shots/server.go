package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ports holds the ten listeners a dev server needs, all set explicitly so
// nothing falls back to a fixed default.
type ports struct {
	web, smtp, submission, submissions, imap, imapTLS, pop3, pop3TLS, gossip, control int
}

// allocPorts returns n distinct free ports. Every listener is held until all
// are open, so the kernel cannot hand back the same port twice.
func allocPorts(n int) ([]int, error) {
	ls := make([]net.Listener, 0, n)
	defer func() {
		for _, l := range ls {
			_ = l.Close()
		}
	}()
	out := make([]int, 0, n)
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("allocate port: %w", err)
		}
		ls = append(ls, l)
		out = append(out, l.Addr().(*net.TCPAddr).Port)
	}
	return out, nil
}

func newPorts() (ports, error) {
	p, err := allocPorts(10)
	if err != nil {
		return ports{}, err
	}
	return ports{p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8], p[9]}, nil
}

func (p ports) origin() string { return "http://127.0.0.1:" + strconv.Itoa(p.web) }

// hcl renders a dev-role config. The decoder treats every non-pointer block as
// mandatory, so the empty blocks below are required, not decoration; their
// attributes keep the defaults.
func (p ports) hcl() string {
	return fmt.Sprintf(`server {
  role         = "dev"
  bind_addr    = "127.0.0.1"
  control_addr = "127.0.0.1:%d"
}

antispam {
  builtin {}
  rspamd {}
}

antivirus {
  clamav {}
}

pipeline {}

telemetry {
  prometheus {}
  log {}
}

cluster {
  raft {}
  tls {}
  gossip {
    port = %d
  }
}

storage {
  pebble {}
}

smtp {
  port            = %d
  submission_port = %d
  submissions_port = %d
  tls {}
  relay {}
  dkim {}
  limits {}
}

imap {
  port     = %d
  port_tls = %d
}

pop3 {
  port     = %d
  port_tls = %d
}

web {
  port       = %d
  plain_http = true
  tls {}
}

caldav {}
`, p.control, p.gossip, p.smtp, p.submission, p.submissions, p.imap, p.imapTLS, p.pop3, p.pop3TLS, p.web)
}

// run executes a command, returning its combined output in the error.
func run(ctx context.Context, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- binaries are built into our own temp dir
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", filepath.Base(name), strings.Join(args, " "), err, tail(string(out), 4000))
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// cleanEnv returns the environment without BONGO_* overrides and with TMPDIR
// pointed at work, so the dev server's ephemeral storage is inside work and
// removed with it even if the server is killed.
func cleanEnv(work string) []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "BONGO_") || strings.HasPrefix(e, "TMPDIR=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "TMPDIR="+work)
}

// build compiles the three binaries the harness drives into work.
func build(ctx context.Context, repo, work string) error {
	for _, b := range []struct{ out, pkg string }{
		{"bongo", "./cmd/bongo"},
		{"bongo-import", "./cmd/bongo-import"},
		{"validate-server", "./scripts/validate-server"},
	} {
		if err := run(ctx, repo, nil, "go", "build", "-o", filepath.Join(work, b.out), b.pkg); err != nil {
			return err
		}
	}
	return nil
}

// server is a running dev server process.
type server struct {
	cmd    *exec.Cmd
	exited chan struct{}
	ports  ports
	log    string
}

// startServer launches the dev server and waits for /health/ready. A port race
// between releasing the held listeners and the server binding them is retried
// once on fresh ports.
func startServer(ctx context.Context, work string) (*server, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		p, err := newPorts()
		if err != nil {
			return nil, err
		}
		s, err := launch(ctx, work, p)
		if err == nil {
			return s, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

func launch(ctx context.Context, work string, p ports) (*server, error) {
	cfg := filepath.Join(work, "bongo.hcl")
	if err := os.WriteFile(cfg, []byte(p.hcl()), 0o600); err != nil {
		return nil, err
	}
	logPath := filepath.Join(work, "server.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- path in our temp dir
	if err != nil {
		return nil, err
	}
	defer func() { _ = lf.Close() }()

	cmd := exec.Command(filepath.Join(work, "bongo"), "serve", "-c", cfg) // #nosec G204 -- our own binary
	cmd.Dir = work
	cmd.Env = cleanEnv(work)
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	s := &server{cmd: cmd, exited: make(chan struct{}), ports: p, log: logPath}
	go func() { _ = cmd.Wait(); close(s.exited) }()

	if err := s.waitReady(ctx, 60*time.Second); err != nil {
		s.stop()
		data, _ := os.ReadFile(logPath) // #nosec G304
		return nil, fmt.Errorf("%w\nserver log tail:\n%s", err, tail(string(data), 3000))
	}
	return s, nil
}

func (s *server) waitReady(ctx context.Context, d time.Duration) error {
	deadline := time.Now().Add(d)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-s.exited:
			return errors.New("server exited before becoming ready")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := client.Get(s.ports.origin() + "/health/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("server not ready within %s", d)
}

// stop sends SIGTERM to the process group so Agent.Shutdown runs, then SIGKILL
// after ten seconds. Safe to call more than once.
func (s *server) stop() {
	if s == nil || s.cmd.Process == nil {
		return
	}
	pgid := s.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-s.exited
	}
}
