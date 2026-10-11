package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseRetention(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"0d", 0, true},
		{"-1d", 0, true},
		{"1.5d", 0, true},
		{"d", 0, true},
		{"abc", 0, true},
		{"", 0, true},
		{"999999999d", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseRetention(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseRetention(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("parseRetention(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestQuarantineRetentionDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 720 * time.Hour},
		{"12h", 12 * time.Hour},
		{"7d", 168 * time.Hour},
		{"", 720 * time.Hour},
		{"bogus", 720 * time.Hour},
		{"-5h", 720 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p := PipelineConfig{QuarantineRetention: tt.in}
			if got := p.QuarantineRetentionDuration(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateQuarantineRetention(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"0d", true}, {"-5h", true}, {"0s", true}, {"abc", true}, {"1.5d", true},
		{"", false}, {"30d", false}, {"12h", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			cfg := Defaults()
			cfg.Pipeline.QuarantineRetention = tt.in
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "pipeline.quarantine_retention") {
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
