package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
)

func TestStartQuarantinePurgePassesConfiguredRetention(t *testing.T) {
	tests := []struct {
		name string
		set  string
		want time.Duration
	}{
		{"configured 12h", "12h", 12 * time.Hour},
		{"configured 7d", "7d", 7 * 24 * time.Hour},
		{"unset defaults to 30d", "", 30 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Pipeline.QuarantineRetention = tt.set
			got := make(chan time.Duration, 1)
			run := func(_ context.Context, _ *pipeline.SpoolStore, _ *store.BlobStore, retention, _ time.Duration, _ *slog.Logger) {
				got <- retention
			}
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			startQuarantinePurge(context.Background(), nil, nil, cfg, logger, run)
			select {
			case r := <-got:
				if r != tt.want {
					t.Errorf("retention = %v, want %v", r, tt.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("run func not called")
			}
		})
	}
}
