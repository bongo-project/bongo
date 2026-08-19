package pipeline

import bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

// PrepareForRescan resets the scanning state on a quarantined envelope so the
// runner's idempotency check (processMessage, see runner.go) does not skip the
// scanning stages when the envelope is requeued to incoming/.
//
// Decision: rescan clears BOTH scanning stages — MsgFlagSpamChecked,
// MsgFlagSpam, MsgFlagVirusChecked, MsgFlagVirus — plus MsgFlagQuarantined,
// and drops both the "antispam" and "antivirus" StageResults from env.Stages.
//
// Rationale: processMessage skips any stage whose checked flag is already
// set (runner.go, the idempotency check ahead of the stage loop). Leaving
// MsgFlagSpamChecked set would turn rescan into "release without spam
// scoring" for anything the spam scorer quarantined — the requeued envelope
// would run antivirus and deliver, but never be re-scored, and would land
// straight in the inbox instead of Junk. Re-running antispam is safe to
// repeat: internal/pipeline/antispam/builtin.go only reads the trained Bayes
// model (TrainedCount/classify) and does not train on rescan, and its only
// envelope mutation is env.TargetMailbox. A spam message rescanned unchanged
// is therefore genuinely re-quarantined by antispam, which is the intended
// behaviour for a bulk backlog drain.
//
// PrepareForRescan deliberately does NOT clear MsgFlagRulesChecked or
// MsgFlagDelivered. Those stages have external side effects that must not
// repeat: the rules stage accumulates PendingTags and appends to
// ForwardAddresses (re-running it would double-forward), and
// MsgFlagDelivered is the crash-recovery guard that prevents duplicate
// delivery (see internal/pipeline/deliver).
func PrepareForRescan(env *bongov1.QueueEnvelope) {
	env.Flags = ClearFlag(env.Flags, MsgFlagQuarantined)
	env.Flags = ClearFlag(env.Flags, MsgFlagVirusChecked)
	env.Flags = ClearFlag(env.Flags, MsgFlagVirus)
	env.Flags = ClearFlag(env.Flags, MsgFlagSpamChecked)
	env.Flags = ClearFlag(env.Flags, MsgFlagSpam)

	// Rebuild env.Stages into a new slice rather than filtering in place
	// while ranging, which would corrupt the tail of the slice.
	stages := make([]*bongov1.StageResult, 0, len(env.Stages))
	for _, sr := range env.Stages {
		if sr.Stage == "antivirus" || sr.Stage == "antispam" {
			continue
		}
		stages = append(stages, sr)
	}
	env.Stages = stages
	env.CurrentStage = ""
}
