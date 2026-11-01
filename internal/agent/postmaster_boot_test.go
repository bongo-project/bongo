package agent

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// Run must call reportPostmasterTargets at boot: a hosted, enabled domain with
// no owner and no smtp.postmaster_account is logged once.
// TestPostmasterReportRunsAtBoot reads the log once Run reports ready. That is
// deterministic only because reportPostmasterTargets runs synchronously before
// the ready flag is set; moving it into a goroutine would make this test racy.
func TestPostmasterReportRunsAtBoot(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.SMTP.PostmasterAccount = ""

	buf := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	runWiringAgent(t, cfg, logger, func(a *Agent) {
		if err := a.Store.Set([]byte("/system/domain/nopm.wiring.test"), []byte("enabled")); err != nil {
			t.Fatal(err)
		}
	})

	out := buf.String()
	const marker = "no postmaster@ and abuse@ target"
	if n := strings.Count(out, marker); n != 1 {
		t.Fatalf("want exactly one %q warning, got %d:\n%s", marker, n, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) && !strings.Contains(line, "nopm.wiring.test") {
			t.Errorf("warning does not name the domain: %s", line)
		}
	}
}
