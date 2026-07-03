package config

import "testing"

func TestParseSize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"empty string errors", "", 0, true},
		{"plain bytes", "1024", 1024, false},
		{"bytes suffix uppercase", "512B", 512, false},
		{"bytes suffix lowercase", "512b", 512, false},
		{"KB uppercase", "1KB", 1024, false},
		{"KB lowercase", "1kb", 1024, false},
		{"KB mixed case", "4Kb", 4096, false},
		{"MB uppercase", "25MB", 25 * 1024 * 1024, false},
		{"MB lowercase", "10mb", 10 * 1024 * 1024, false},
		{"GB uppercase", "1GB", 1024 * 1024 * 1024, false},
		{"GB lowercase", "2gb", 2 * 1024 * 1024 * 1024, false},
		{"zero bytes", "0", 0, false},
		{"zero with suffix", "0MB", 0, false},
		{"leading/trailing whitespace", "  8MB  ", 8 * 1024 * 1024, false},
		{"negative errors", "-1MB", 0, true},
		{"letters only error", "abc", 0, true},
		{"partial number error", "10.5MB", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSize(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestSMTPLimitsMaxMessageSizeBytes(t *testing.T) {
	const fallback = int64(25 * 1024 * 1024)

	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"empty falls back to 25MB", "", fallback},
		{"explicit 25MB", "25MB", 25 * 1024 * 1024},
		{"10MB", "10MB", 10 * 1024 * 1024},
		{"1GB", "1GB", 1024 * 1024 * 1024},
		{"512KB", "512KB", 512 * 1024},
		{"unparseable falls back to 25MB", "badvalue", fallback},
		{"zero falls back to 25MB", "0", fallback},
		{"negative falls back to 25MB", "-5MB", fallback},
		{"raw bytes", "26214400", 26214400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &SMTPLimits{MaxMessageSize: tt.input}
			got := l.MaxMessageSizeBytes()
			if got != tt.want {
				t.Errorf("MaxMessageSizeBytes() for %q = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
