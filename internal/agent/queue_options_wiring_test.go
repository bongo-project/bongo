package agent

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/queue"
)

// The four queue.* tuning keys must reach the queue worker. The values are
// distinct from both the config and the queue defaults (5d, 1h, 4h, 5) and
// in range, so deleting any queue.With* line in agent.go leaves the worker on
// a default and turns this red.
func TestQueueOptionsReachWorker(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.Queue.MaxLifetime = "73h"
	cfg.Queue.MaxRetryInterval = "37m"
	cfg.Queue.DelayWarningAfter = "5h"
	cfg.Queue.Concurrency = 7

	a := runWiringAgent(t, cfg, testLogger(), nil)
	if a.QueueWorker == nil {
		t.Fatal("queue worker not created")
	}
	want := queue.WorkerSettings{
		MaxLifetime:       73 * time.Hour,
		MaxRetryInterval:  37 * time.Minute,
		Concurrency:       7,
		DelayWarningAfter: 5 * time.Hour,
	}
	if got := a.QueueWorker.Settings(); got != want {
		t.Errorf("worker settings = %+v, want %+v", got, want)
	}
}
