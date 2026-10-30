//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	smtplib "net/smtp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/bongo-project/bongo/internal/agent"
	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
)

// reloadLogBuf is a log sink safe for concurrent writers and readers.
type reloadLogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *reloadLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// byMsg returns the captured JSON log records whose msg equals msg.
func (b *reloadLogBuf) byMsg(t *testing.T, msg string) []map[string]any {
	t.Helper()
	b.mu.Lock()
	raw := b.buf.String()
	b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

// reloadConfFile renders a complete dev-role config file using cfg's ports,
// the given SMTP MX port and spam thresholds. gohcl rejects any missing
// non-pointer block, so every one is present.
func reloadConfFile(cfg *config.Config, smtpPort int, threshold, high string) string {
	return fmt.Sprintf(`
server {
  role      = "dev"
  bind_addr = "127.0.0.1"
}
cluster {
  raft {}
  gossip {
    port = %d
  }
  tls {}
}
storage {
  pebble {}
}
smtp {
  enabled          = true
  port             = %d
  submission_port  = %d
  # The MX listener is dual-stack, so loopback may arrive as ::1.
  trusted_networks = ["127.0.0.1/32", "::1/128"]
  tls {}
  limits {}
  relay {}
  dkim {}
}
imap {
  enabled  = true
  port     = %d
  port_tls = %d
}
pop3 {
  port     = %d
  port_tls = %d
}
web {
  port = %d
  tls {}
}
caldav {}
antispam {
  builtin {
    enabled        = true
    threshold      = %s
    high_threshold = %s
  }
  rspamd {
    enabled = false
  }
}
antivirus {
  clamav {}
}
pipeline {
  poll_interval = "50ms"
}
telemetry {
  prometheus {}
  log {}
}
`, cfg.Cluster.Gossip.Port, smtpPort, cfg.SMTP.SubmissionPort,
		cfg.IMAP.Port, cfg.IMAP.PortTLS, cfg.POP3.Port, cfg.POP3.PortTLS,
		cfg.Web.Port, threshold, high)
}

// TestConfigReload_SpamThresholdAppliesRestartKeyDoesNot drives a real agent
// started from a config file: a message scoring between the old and new spam
// thresholds is delivered before and after a reload and must route to INBOX
// and Junk respectively. A restart-only key (smtp.port) edited in the same
// file must be reported as needing a restart and left unchanged.
//
// It calls Agent.Reload directly; runServe hands that same function to
// watchSignals, whose SIGHUP routing is covered in cmd/bongo
// (TestWatchSignals_HUPReloadsWithoutCancel).
func TestConfigReload_SpamThresholdAppliesRestartKeyDoesNot(t *testing.T) {
	logs := &reloadLogBuf{}
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, logs), &slog.HandlerOptions{Level: slog.LevelInfo}))

	path, err := filepath.Abs(filepath.Join(t.TempDir(), "bongo.hcl"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	// "role" stays unset in viper so it comes from the file, as with
	// `serve --config`.
	v := viper.New()
	v.Set("config", path)

	load := func() *config.Config {
		t.Helper()
		c, _, _, err := config.LoadWithDiscovery(v)
		if err != nil {
			t.Fatalf("LoadWithDiscovery: %v", err)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		return c
	}

	// Draw the ports, write the file, and run the agent on the config loaded
	// from it, as runServe does. sharedStartAgentWith redraws the ports on
	// running on a port-race retry, so the hook re-renders the file from
	// running's current ports and rebuilds the baseline from the file (not
	// from running, which NewAgent mutates in dev mode).
	seed := config.Defaults()
	sharedAssignFreePorts(t, seed)
	write := func(ports *config.Config, smtpPort int, threshold, high string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(reloadConfFile(ports, smtpPort, threshold, high)), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	write(seed, seed.SMTP.Port, "0.5", "0.9")
	running := load()
	hook := func(a *agent.Agent) error {
		write(running, running.SMTP.Port, "0.5", "0.9")
		a.EnableReload(v, path, false, load())
		return nil
	}
	a, _, _ := sharedStartAgentWith(t, running, logger, hook)
	oldMX := a.SMTP.MXAddr()
	oldPort := a.Config.SMTP.Port

	// A message whose builtin score is derived, not guessed: the trusted
	// envelope (127.0.0.1/32 and ::1/128, see the fixture) disables
	// SPF/DKIM/DMARC/DNSBL, so only content counts.
	const from = "sender@localhost"
	const body = "Just checking in about next week.\r\n"
	want := antispam.NewScorer().Score(&antispam.ScoringInput{
		Headers:    map[string]string{"From": from},
		Subject:    "",
		BodySample: body,
	})
	if want.Score < 0.25 || want.Score >= 0.5 {
		t.Fatalf("crafted score %.2f must lie in [0.25, 0.5)", want.Score)
	}
	wantReasons := strings.Join(want.Reasons, ", ")
	if wantReasons != "missing Message-ID, missing Date header, empty subject" {
		t.Fatalf("derived reasons = %q", wantReasons)
	}

	email := "reloadtest@localhost"
	userID, err := auth.CreateUser(a, a.Metadata, email, "Reload Test", "testpass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := a.Mailboxes.EnsureDefaultMailboxes(userID); err != nil {
		t.Fatalf("EnsureDefaultMailboxes: %v", err)
	}
	msg := []byte("From: " + from + "\r\nTo: " + email + "\r\n\r\n" + body)

	send := func() {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			err := smtplib.SendMail(oldMX, nil, "", []string{email}, msg)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("SendMail: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	count := func(box string) int {
		msgs, _, err := a.Mailboxes.ListMessagesPaged(userID, box, 0, 10)
		if err != nil {
			t.Fatalf("ListMessagesPaged %s: %v", box, err)
		}
		return len(msgs)
	}
	waitDelivered := func(n int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for count("INBOX")+count("Junk") < n {
			if time.Now().After(deadline) {
				t.Fatalf("only %d of %d messages delivered", count("INBOX")+count("Junk"), n)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Message 1 under 0.5/0.9. It must be fully delivered before the file is
	// rewritten or the pipeline could score it with the new thresholds.
	send()
	waitDelivered(1)

	// One edit: new thresholds plus a restart-only key. The new port is never
	// bound and differs from every configured port.
	write(running, sharedFreePorts(t, 1)[0], "0.25", "0.8")
	if err := a.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	send()
	waitDelivered(2)

	headers := func(box string) mail.Header {
		t.Helper()
		msgs, _, err := a.Mailboxes.ListMessagesPaged(userID, box, 0, 10)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("%s: got %d messages, err %v; want exactly 1", box, len(msgs), err)
		}
		meta := loadMailMeta(t, a, userID, msgs[0].MailGuid)
		if meta.SpamScore != want.Score {
			t.Errorf("%s SpamScore = %v, want %v", box, meta.SpamScore, want.Score)
		}
		rc, err := a.Blobs.Get(meta.RawMessage.Hash)
		if err != nil {
			t.Fatalf("blob get: %v", err)
		}
		defer rc.Close()
		m, err := mail.ReadMessage(rc)
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		return m.Header
	}
	inbox, junk := headers("INBOX"), headers("Junk")
	if got := inbox.Get("X-Spam-Status"); !strings.HasPrefix(got, "No") {
		t.Errorf("INBOX X-Spam-Status = %q, want No", got)
	}
	if got := junk.Get("X-Spam-Status"); !strings.HasPrefix(got, "Yes") {
		t.Errorf("Junk X-Spam-Status = %q, want Yes", got)
	}
	wantScore := fmt.Sprintf("%.2f", want.Score)
	for name, h := range map[string]mail.Header{"INBOX": inbox, "Junk": junk} {
		if got := h.Get("X-Spam-Score"); got != wantScore {
			t.Errorf("%s X-Spam-Score = %q, want %q", name, got, wantScore)
		}
		if got := h.Get("X-Spam-Reasons"); got != wantReasons {
			t.Errorf("%s X-Spam-Reasons = %q, want %q", name, got, wantReasons)
		}
	}

	// The reload summary.
	if n := len(logs.byMsg(t, "config reload failed")); n != 0 {
		t.Errorf("%d \"config reload failed\" records, want 0", n)
	}
	sums := logs.byMsg(t, "config reload complete")
	if len(sums) != 1 {
		t.Fatalf("%d \"config reload complete\" records, want 1", len(sums))
	}
	asSorted := func(v any) []string {
		var out []string
		list, _ := v.([]any)
		for _, e := range list {
			out = append(out, fmt.Sprint(e))
		}
		slices.Sort(out)
		return out
	}
	sum := sums[0]
	if got, w := asSorted(sum["applied"]), []string{"antispam.builtin.high_threshold", "antispam.builtin.threshold"}; !slices.Equal(got, w) {
		t.Errorf("applied = %v, want %v", got, w)
	}
	if got, w := asSorted(sum["needs_restart"]), []string{"smtp.port"}; !slices.Equal(got, w) {
		t.Errorf("needs_restart = %v, want %v", got, w)
	}
	if got := asSorted(sum["no_effect"]); len(got) != 0 {
		t.Errorf("no_effect = %v, want empty", got)
	}
	if got := asSorted(sum["shadowed"]); len(got) != 0 {
		t.Errorf("shadowed = %v, want empty", got)
	}
	warns := logs.byMsg(t, "config reload: key needs restart")
	if len(warns) != 1 || warns[0]["key"] != "smtp.port" || warns[0]["level"] != "WARN" {
		t.Errorf("needs-restart warnings = %v, want one WARN for smtp.port", warns)
	}

	// The restart-only key did not move: the running config and the listener
	// that accepted message 2 are still on the old port.
	if a.Config.SMTP.Port != oldPort || a.SMTP.MXAddr() != oldMX {
		t.Errorf("smtp port moved: config %d (was %d), MX %s (was %s)", a.Config.SMTP.Port, oldPort, a.SMTP.MXAddr(), oldMX)
	}
}
