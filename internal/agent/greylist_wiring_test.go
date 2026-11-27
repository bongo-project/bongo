package agent

import (
	"context"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

func TestInitGreylistWiring(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		enabled bool
		smtp    bool
		wantNil bool
	}{
		{"enabled on a server", "server", true, true, false},
		{"enabled in dev", "dev", true, true, false},
		{"disabled", "server", false, true, true},
		{"smtp off", "server", true, false, true},
		{"agent role takes no table", "agent", true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.Role = tt.role
			cfg.SMTP.Enabled = tt.smtp
			cfg.SMTP.Greylisting = &config.SMTPGreylisting{Enabled: tt.enabled}
			a := &Agent{Config: cfg, logger: testLogger()}
			table := a.initGreylist(context.Background())
			t.Cleanup(func() {
				if a.greylistCancel != nil {
					a.greylistCancel()
				}
				a.bgWG.Wait()
			})
			if (table == nil) != tt.wantNil {
				t.Fatalf("table nil = %v, want %v", table == nil, tt.wantNil)
			}
			if tt.wantNil {
				return
			}
			backend, err := a.newServerSMTPBackend(nil, nil)
			if err != nil {
				t.Fatalf("newServerSMTPBackend: %v", err)
			}
			if backend.Greylister() == nil {
				t.Fatal("SMTP backend has no greylister")
			}
		})
	}
}

func TestSMTPBackendWithoutGreylistHasNone(t *testing.T) {
	a := &Agent{Config: config.Defaults(), logger: testLogger()}
	backend, err := a.newServerSMTPBackend(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if backend.Greylister() != nil {
		t.Fatal("greylisting is off by default")
	}
}
