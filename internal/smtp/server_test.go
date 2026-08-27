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
			srv := NewServer(cfg, backend, true, testLogger())

			if srv.mx.MaxMessageBytes != tt.wantBytes {
				t.Errorf("mx.MaxMessageBytes = %d, want %d", srv.mx.MaxMessageBytes, tt.wantBytes)
			}
			if srv.submission.MaxMessageBytes != tt.wantBytes {
				t.Errorf("submission.MaxMessageBytes = %d, want %d", srv.submission.MaxMessageBytes, tt.wantBytes)
			}
		})
	}
}
