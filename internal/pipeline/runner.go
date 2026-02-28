package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	defaultWorkers      = 5
	defaultPollInterval = time.Second
)

// Runner polls the spool incoming/ directory, dispatches messages to a worker pool,
// and routes each message through the configured pipeline stages.
type Runner struct {
	spool        *SpoolStore
	stages       []Processor
	blobs        BlobReader
	logger       *slog.Logger
	workers      int
	pollInterval time.Duration
}

// RunnerOption is a functional option for Runner.
type RunnerOption func(*Runner)

// WithWorkers sets the number of concurrent message-processing workers.
func WithWorkers(n int) RunnerOption {
	return func(r *Runner) {
		if n > 0 {
			r.workers = n
		}
	}
}

// WithPollInterval sets how often the runner checks incoming/ for new messages.
func WithPollInterval(d time.Duration) RunnerOption {
	return func(r *Runner) {
		if d > 0 {
			r.pollInterval = d
		}
	}
}

// NewRunner creates a Runner with the given spool, blob reader, stages, and options.
func NewRunner(spool *SpoolStore, blobs BlobReader, stages []Processor, logger *slog.Logger, opts ...RunnerOption) *Runner {
	r := &Runner{
		spool:        spool,
		stages:       stages,
		blobs:        blobs,
		logger:       logger,
		workers:      defaultWorkers,
		pollInterval: defaultPollInterval,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Run starts the worker pool and polls incoming/ until ctx is cancelled.
// In-flight messages are allowed to complete before Run returns.
func (r *Runner) Run(ctx context.Context) error {
	work := make(chan *bongov1.QueueEnvelope, r.workers)

	var wg sync.WaitGroup
	for i := 0; i < r.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for env := range work {
				if err := r.processMessage(ctx, env); err != nil {
					r.logger.Error("pipeline: process message failed",
						"id", env.Id,
						"error", err,
					)
				}
			}
		}()
	}

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

poll:
	for {
		select {
		case <-ctx.Done():
			break poll
		case <-ticker.C:
			for {
				env, err := r.spool.Dequeue()
				if err != nil {
					r.logger.Error("pipeline: dequeue failed", "error", err)
					break
				}
				if env == nil {
					break
				}
				select {
				case work <- env:
				case <-ctx.Done():
					// Put the envelope back into incoming/ so it is not lost.
					if moveErr := r.spool.Move(env.Id, "processing", "incoming"); moveErr != nil {
						r.logger.Error("pipeline: failed to return envelope on shutdown",
							"id", env.Id,
							"error", moveErr,
						)
					}
					break poll
				}
			}
		}
	}

	close(work)
	wg.Wait()
	return nil
}

// processMessage runs the configured pipeline stages sequentially on a single
// envelope. Stages that have already been recorded in env.Flags are skipped
// (idempotent). On completion the envelope is moved to deliver/; on quarantine
// or error it is moved to the appropriate directory.
func (r *Runner) processMessage(ctx context.Context, env *bongov1.QueueEnvelope) error {
	for _, stage := range r.stages {
		checkedFlag, spamFlag := flagsForStage(stage.Name())

		// Idempotent: skip stages already completed.
		if checkedFlag != 0 && HasFlag(env.Flags, checkedFlag) {
			r.logger.Debug("pipeline: skipping stage (already done)",
				"id", env.Id,
				"stage", stage.Name(),
			)
			continue
		}

		// Re-read the blob for each stage so readers are independent.
		rc, err := r.blobs.ReadBlob(env.BlobHash)
		if err != nil {
			if moveErr := r.spool.Move(env.Id, "processing", "error"); moveErr != nil {
				r.logger.Error("pipeline: move to error failed", "id", env.Id, "error", moveErr)
			}
			return fmt.Errorf("pipeline: read blob for %s stage %s: %w", env.Id, stage.Name(), err)
		}

		env.CurrentStage = stage.Name()
		result, err := stage.Process(ctx, env, rc)
		rc.Close()
		if err != nil {
			if moveErr := r.spool.Move(env.Id, "processing", "error"); moveErr != nil {
				r.logger.Error("pipeline: move to error failed", "id", env.Id, "error", moveErr)
			}
			return fmt.Errorf("pipeline: stage %s for %s: %w", stage.Name(), env.Id, err)
		}

		env.Stages = append(env.Stages, result)

		// Update checked flag.
		if checkedFlag != 0 {
			env.Flags = SetFlag(env.Flags, checkedFlag)
		}

		// Update outcome flags based on the action.
		switch result.Action {
		case "tag":
			if spamFlag != 0 {
				env.Flags = SetFlag(env.Flags, spamFlag)
			}
		case "reject", "quarantine":
			if spamFlag != 0 {
				env.Flags = SetFlag(env.Flags, spamFlag)
			}
			env.Flags = SetFlag(env.Flags, MsgFlagQuarantined)

			// Persist updated envelope before moving.
			if writeErr := r.spool.WriteEnvelope("processing", env); writeErr != nil {
				r.logger.Error("pipeline: write envelope before quarantine failed",
					"id", env.Id,
					"error", writeErr,
				)
			}
			if moveErr := r.spool.Move(env.Id, "processing", "quarantine"); moveErr != nil {
				return fmt.Errorf("pipeline: move %s to quarantine: %w", env.Id, moveErr)
			}
			r.logger.Info("pipeline: message quarantined",
				"id", env.Id,
				"stage", stage.Name(),
				"action", result.Action,
			)
			return nil
		}

		// Persist envelope state after each stage.
		if err := r.spool.WriteEnvelope("processing", env); err != nil {
			if moveErr := r.spool.Move(env.Id, "processing", "error"); moveErr != nil {
				r.logger.Error("pipeline: move to error failed", "id", env.Id, "error", moveErr)
			}
			return fmt.Errorf("pipeline: write envelope %s after stage %s: %w", env.Id, stage.Name(), err)
		}
	}

	env.CurrentStage = ""
	if err := r.spool.Move(env.Id, "processing", "deliver"); err != nil {
		return fmt.Errorf("pipeline: move %s to deliver: %w", env.Id, err)
	}

	r.logger.Info("pipeline: message ready for delivery", "id", env.Id)
	return nil
}

// flagsForStage returns the (checkedFlag, outcomeFlag) pair for a named stage.
// outcomeFlag is the flag set when the stage result is negative (spam/virus found).
func flagsForStage(name string) (checkedFlag, outcomeFlag uint32) {
	switch name {
	case "antispam":
		return MsgFlagSpamChecked, MsgFlagSpam
	case "antivirus":
		return MsgFlagVirusChecked, MsgFlagVirus
	case "rules":
		return MsgFlagRulesChecked, 0
	case "deliver":
		return MsgFlagDelivered, 0
	default:
		return 0, 0
	}
}
