package config

import (
	"strings"
	"testing"
)

func TestPebbleCacheBytes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"256MB", "256MB", 256 << 20, false},
		{"1GB", "1GB", 1 << 30, false},
		{"unset", "", 0, false},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, true},
		{"garbage", "abc", 0, true},
		// ParseSize has no binary-suffix support (KiB), so this is rejected.
		{"KiB unsupported", "512KiB", 0, true},
		{"overflow", "9999999999GB", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PebbleConfig{CacheSize: tt.in}
			got, err := p.CacheBytes()
			if (err != nil) != tt.wantErr {
				t.Fatalf("CacheBytes(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("CacheBytes(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateRejectsBadPebbleCacheSize(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", "512KiB", "9999999999GB"} {
		cfg := Defaults()
		cfg.Storage.Pebble.CacheSize = bad
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "storage.pebble.cache_size") {
			t.Errorf("Validate with cache_size %q: err = %v, want error naming storage.pebble.cache_size", bad, err)
		}
	}
	cfg := Defaults()
	cfg.Storage.Pebble.CacheSize = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate with empty cache_size: %v", err)
	}
}
