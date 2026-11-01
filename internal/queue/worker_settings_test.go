package queue

import (
	"log/slog"
	"testing"
	"time"
)

func TestWorkerSettings(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want WorkerSettings
	}{
		{"defaults", nil, WorkerSettings{defaultMaxLifetime, defaultMaxRetryInterval, defaultConcurrency, defaultDelayWarning}},
		{
			"options applied",
			[]Option{WithMaxLifetime(73 * time.Hour), WithMaxRetryInterval(37 * time.Minute), WithConcurrency(7), WithDelayWarningAfter(5 * time.Hour)},
			WorkerSettings{73 * time.Hour, 37 * time.Minute, 7, 5 * time.Hour},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWorker(nil, nil, nil, nil, nil, nil, slog.Default(), tt.opts...)
			if got := w.Settings(); got != tt.want {
				t.Errorf("Settings() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
