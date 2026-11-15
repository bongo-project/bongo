package agent

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// The production IMAP server must advertise QUOTA, or clients never ask for
// the user's limit.
func TestIMAPQuotaAdvertised(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	waitFor(t, "IMAP listener", func() bool { return a.IMAP != nil && a.IMAP.PlainAddr() != "" })

	conn, err := net.DialTimeout("tcp", a.IMAP.PlainAddr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial IMAP: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	r := bufio.NewReader(conn)
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	var caps string
	for i, cmd := range []string{"LOGIN admin@localhost admin", "CAPABILITY"} {
		tag := fmt.Sprintf("a%d", i)
		_, _ = fmt.Fprintf(conn, "%s %s\r\n", tag, cmd)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: %v", cmd, err)
			}
			if strings.HasPrefix(line, "* CAPABILITY") {
				caps = line
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					t.Fatalf("%s: %s", cmd, line)
				}
				break
			}
		}
	}
	for _, want := range []string{" QUOTA", " QUOTA=RES-STORAGE"} {
		if !strings.Contains(caps, want) {
			t.Errorf("CAPABILITY = %q, want%s", caps, want)
		}
	}
}
