package config

import "testing"

func TestQueueBounceCopyToPostmaster(t *testing.T) {
	var nilQ *QueueConfig
	tests := []struct {
		name string
		q    *QueueConfig
		want bool
	}{
		{"nil block", nilQ, false},
		{"zero value", &QueueConfig{}, false},
		{"defaults", Defaults().Queue, false},
		{"explicit true", &QueueConfig{BounceCopyPostmaster: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.BounceCopyToPostmaster(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBounceCopyPostmasterIsRestartKey(t *testing.T) {
	for _, k := range restartKeys {
		if k == "queue.bounce_copy_postmaster" {
			return
		}
	}
	t.Error("queue.bounce_copy_postmaster must be classified as a restart key")
}
