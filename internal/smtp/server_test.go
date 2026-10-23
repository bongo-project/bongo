package smtp

import (
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

func TestNewServerMaxMessageSizeWiring(t *testing.T) {
	tests := []struct {
		name           string
		maxMessageSize string
		wantBytes      int64
	}{
		{"explicit 10MB", "10MB", 10 * 1024 * 1024},
		{"explicit 50MB", "50MB", 50 * 1024 * 1024},
		{"explicit 1GB", "1GB", 1024 * 1024 * 1024},
		{"empty falls back to 25MB default", "", 25 * 1024 * 1024},
		{"unparseable falls back to 25MB default", "badval", 25 * 1024 * 1024},
		{"zero falls back to 25MB default", "0", 25 * 1024 * 1024},
		{"default 25MB from config default", "25MB", 25 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.SMTPConfig{
				Port:           2525,
				SubmissionPort: 2587,
				Limits: config.SMTPLimits{
					MaxMessageSize: tt.maxMessageSize,
					MaxRecipients:  10,
					MaxConnections: 64,
				},
			}
			backend := NewBackend(nil, nil, testLogger())
			srv := NewServer(cfg, "", backend, true, testLogger())

			if srv.mx.MaxMessageBytes != tt.wantBytes {
				t.Errorf("mx.MaxMessageBytes = %d, want %d", srv.mx.MaxMessageBytes, tt.wantBytes)
			}
			if srv.submission.MaxMessageBytes != tt.wantBytes {
				t.Errorf("submission.MaxMessageBytes = %d, want %d", srv.submission.MaxMessageBytes, tt.wantBytes)
			}
		})
	}
}

func TestNewServerHonoursBindAddr(t *testing.T) {
	tests := []struct {
		bind    string
		wantMX  string
		wantSub string
	}{
		{"127.0.0.1", "127.0.0.1:2525", "127.0.0.1:2587"},
		{"::1", "[::1]:2525", "[::1]:2587"},
		{"", ":2525", ":2587"},
		{"0.0.0.0", ":2525", ":2587"},
		{"::", ":2525", ":2587"},
		{"localhost", "localhost:2525", "localhost:2587"},
	}
	for _, tt := range tests {
		t.Run("bind="+tt.bind, func(t *testing.T) {
			cfg := &config.SMTPConfig{Port: 25, SubmissionPort: 587}
			srv := NewServer(cfg, tt.bind, NewBackend(nil, nil, testLogger()), true, testLogger())
			if srv.mx.Addr != tt.wantMX {
				t.Errorf("mx.Addr = %q, want %q", srv.mx.Addr, tt.wantMX)
			}
			if srv.submission.Addr != tt.wantSub {
				t.Errorf("submission.Addr = %q, want %q", srv.submission.Addr, tt.wantSub)
			}
		})
	}
}
