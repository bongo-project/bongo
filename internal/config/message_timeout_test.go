package config

import (
	"strings"
	"testing"
	"time"
)

func TestMessageTimeoutDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 2 * time.Minute},
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
		{"0s", 2 * time.Minute},
		{"-1m", 2 * time.Minute},
		{"junk", 2 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p := PipelineConfig{MessageTimeout: tt.in}
			if got := p.MessageTimeoutDuration(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateMessageTimeout(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"0s", true}, {"-5m", true}, {"abc", true}, {"2", true},
		{"", false}, {"2m", false}, {"90s", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			cfg := Defaults()
			cfg.Pipeline.MessageTimeout = tt.in
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "pipeline.message_timeout") {
					t.Fatalf("want error naming key, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
