package config

import (
	"strings"
	"testing"
)

func TestGroupFanoutPerHour(t *testing.T) {
	if got := Defaults().SMTP.Limits.GroupFanoutPerHour; got != 5000 {
		t.Errorf("default group_fanout_per_hour = %d, want 5000", got)
	}
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"default", 5000, false},
		{"zero disables", 0, false},
		{"negative refused", -1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.SMTP.Limits.GroupFanoutPerHour = tt.value
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "smtp.limits.group_fanout_per_hour") {
					t.Fatalf("Validate() = %v, want an error naming the key", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}
