package pipeline

import (
	"fmt"
	"log/slog"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// EnvelopeSpool is the part of SpoolStore that EnqueueAll needs. It is defined
// here so callers can inject a failing spool in tests.
type EnvelopeSpool interface {
	Enqueue(env *bongov1.QueueEnvelope) error
	Remove(dir string, id string) error
}

// EnqueueAll writes each envelope to the spool's incoming/ directory.
//
// The writes are not atomic as a group: spool files cannot be committed
// together. If envelope k fails, the k-1 envelopes already written are removed
// on a best-effort basis and the error is returned, so the caller can refuse
// the whole message (SMTP 451, gRPC Internal) and the sender retries. A removal
// can lose a race with the runner, which may already have moved the envelope to
// processing/; that recipient then receives a duplicate on retry. That is
// deliberate: a duplicate is recoverable, a lost message is not.
func EnqueueAll(spool EnvelopeSpool, envs []*bongov1.QueueEnvelope, logger *slog.Logger) error {
	for i, env := range envs {
		if err := spool.Enqueue(env); err != nil {
			for _, done := range envs[:i] {
				if rerr := spool.Remove("incoming", done.GetId()); rerr != nil {
					logger.Warn("spool: rollback of envelope failed; a retry may duplicate it",
						"id", done.GetId(), "error", rerr)
				}
			}
			return fmt.Errorf("enqueuing envelope %d of %d: %w", i+1, len(envs), err)
		}
	}
	return nil
}
