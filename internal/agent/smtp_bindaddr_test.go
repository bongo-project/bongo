package agent

import (
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
)

// TestNewSMTPServerHonoursBindAddr proves the agent hands the operator's
// server.bind_addr to the SMTP listeners; both the server and agent roles
// build their SMTP server through newSMTPServer.
func TestNewSMTPServerHonoursBindAddr(t *testing.T) {
	tests := []struct {
		bind, wantMX, wantSub string
	}{
		{"127.0.0.1", "127.0.0.1:2525", "127.0.0.1:2587"},
		{"::1", "[::1]:2525", "[::1]:2587"},
		{"0.0.0.0", ":2525", ":2587"},
	}
	for _, tt := range tests {
		t.Run(tt.bind, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.BindAddr = tt.bind
			a := &Agent{Config: cfg, logger: testLogger()}
			srv := a.newSMTPServer(bongosmtp.NewBackend(nil, nil, testLogger()), true)
			if got := srv.MXAddr(); got != tt.wantMX {
				t.Errorf("MXAddr = %q, want %q", got, tt.wantMX)
			}
			if got := srv.SubmissionAddr(); got != tt.wantSub {
				t.Errorf("SubmissionAddr = %q, want %q", got, tt.wantSub)
			}
		})
	}
}
