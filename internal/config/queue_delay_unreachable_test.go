package config

import "testing"

// A delay report is due only before the message would be bounced; with the
// 4h default and a lifetime of 4h or less none can ever be sent.
func TestQueueDelayWarningUnreachable(t *testing.T) {
	tests := []struct {
		name string
		q    *QueueConfig
		want bool
	}{
		{"nil block", nil, false},
		{"defaults", &QueueConfig{}, false},
		{"short lifetime, default delay", &QueueConfig{MaxLifetime: "2h"}, true},
		{"lifetime equals default delay", &QueueConfig{MaxLifetime: "4h"}, true},
		{"short lifetime, shorter delay", &QueueConfig{MaxLifetime: "2h", DelayWarningAfter: "1h"}, false},
		{"just over default delay", &QueueConfig{MaxLifetime: "5h"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.DelayWarningUnreachable(); got != tt.want {
				t.Errorf("DelayWarningUnreachable() = %v, want %v", got, tt.want)
			}
		})
	}
}
