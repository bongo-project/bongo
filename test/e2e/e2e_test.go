//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	smtplib "net/smtp"
	"testing"
	"time"

	"github.com/bongo-go/bongo-go/internal/agent"
	"github.com/bongo-go/bongo-go/internal/config"
)

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

func TestE2EDevMode(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Web.Port = freePort(t)
	cfg.SMTP.Enabled = true

	logger := testLogger()

	a, err := agent.NewAgent(cfg, logger)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	// Wait for agent readiness.
	deadline := time.Now().Add(15 * time.Second)
	for !a.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent not ready in time")
	}

	// 1. Health endpoint returns 200.
	httpAddr := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)
	resp, err := http.Get(httpAddr + "/health/ready")
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	t.Log("health endpoint OK")

	// 2. Send an email via SMTP.
	smtpAddr := a.SMTP.MXAddr()
	msg := "From: test@example.com\r\n" +
		"To: admin@localhost\r\n" +
		"Subject: E2E Test\r\n" +
		"\r\n" +
		"Hello from the E2E test.\r\n"

	err = smtplib.SendMail(smtpAddr, nil, "test@example.com", []string{"admin@localhost"}, []byte(msg))
	if err != nil {
		t.Fatalf("SendMail: %v", err)
	}
	t.Log("email sent via SMTP")

	// 3. Verify mail landed in Pebble.
	time.Sleep(1 * time.Second)

	// Find the admin user ID.
	var adminID string
	err = a.Metadata.System.DB().QueryRow(
		"SELECT id FROM users WHERE username = 'admin@localhost'",
	).Scan(&adminID)
	if err != nil {
		t.Fatalf("lookup admin user: %v", err)
	}

	prefix := fmt.Sprintf("/user/%s/mail/", adminID)
	iter, err := a.Store.PrefixScan([]byte(prefix))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	count := 0
	for iter.Next() {
		count++
	}
	iter.Close()

	if count == 0 {
		t.Fatal("no mail found in Pebble after SMTP delivery")
	}
	t.Logf("found %d mail key(s) in Pebble", count)

	// Shutdown.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Run returned: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent did not stop in time")
	}
}
