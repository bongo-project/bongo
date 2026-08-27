package pipeline

import bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

// PrepareForRescan resets the scanning state on a quarantined envelope so the
// runner's idempotency check (processMessage, see runner.go) does not skip the
// scanning stages when the envelope is requeued to incoming/. It clears the
// spam and virus flags plus MsgFlagQuarantined, and drops both the "antispam"
// and "antivirus" StageResults from env.Stages.
//
// BOTH scanning stages must be cleared, not just the one that quarantined:
// processMessage skips any stage whose checked flag is already set, so leaving
// MsgFlagSpamChecked set would turn rescan into "release without spam scoring"
// for anything the spam scorer quarantined — the requeued envelope would run
// antivirus and deliver, but never be re-scored, and would land straight in
// the inbox instead of Junk. Re-running antispam is safe to repeat:
// internal/pipeline/antispam/builtin.go only reads the trained Bayes model
// (TrainedCount/classify) and does not train on rescan, and its only envelope
// mutation is env.TargetMailbox. A spam message rescanned unchanged is
// therefore genuinely re-quarantined by antispam, which is the intended
// behaviour for a bulk backlog drain.
//
// MsgFlagRulesChecked and MsgFlagDelivered are deliberately NOT cleared. Those
// stages have external side effects that must not repeat: the rules stage
// accumulates PendingTags and appends to ForwardAddresses (re-running it would
// double-forward), and MsgFlagDelivered is the crash-recovery guard that
// prevents duplicate delivery (see internal/pipeline/deliver).
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

// PrepareForRelease resets the quarantine state on an envelope so it is
// re-delivered without re-running the stage that quarantined it. It clears
// ONLY MsgFlagQuarantined; the scanning-related flags (MsgFlagSpamChecked,
// MsgFlagSpam, MsgFlagVirusChecked, MsgFlagVirus) and their StageResults are
// retained.
//
// Release means "the admin has reviewed this and overrides the verdict"; it is
// not rescan (which means "check this again"). The runner's idempotency check
// (runner.go, processMessage) will skip stages whose checked flags are already
// set, so the released envelope will not re-run whichever of antispam/antivirus
// already ran and quarantined it — we trust the admin's judgment, not the
// scanner's original verdict. The StageResults are retained as an audit record
// of why the message was originally quarantined (admin visibility in logs and
// potential later investigation), but they do not block delivery.
//
// The rules stage is NOT skipped: when antispam or antivirus quarantines a
// message, the pipeline returns before the rules stage ever runs (see
// processMessage's quarantine branch, which returns immediately), so
// MsgFlagRulesChecked was never set. On release, rules therefore runs for the
// first and only time — correctly, since it has not run before — and may
// accumulate PendingTags, append ForwardAddresses, or move the message again
// as it would for any other message. That is intentional: a released message
// should still be filed and forwarded per the user's own rules, which are not
// what the admin overrode. Callers should be aware release can have outbound
// side effects (forwarding) that a plain "unquarantine" might not expect.
//
// Destination mailbox: a quarantined envelope reaches here with
// TargetMailbox either empty or "Junk", and BOTH are consequences of the
// verdict the admin has just overridden, so both must be undone.
//
//   - Empty is the common case. The deliver stage would then fall back to
//     spamScoreFromStages(env.Stages) — reading the very StageResult this
//     function retains for audit — and because config guarantees
//     high_threshold >= junk_threshold, that score is always above the Junk
//     line (see deliver.go's targetMailbox fallback).
//   - "Junk" arises when the scorer put the message in the tag band (setting
//     TargetMailbox = "Junk", antispam/builtin.go) and DMARC then escalated
//     the action to reject/quarantine without clearing it
//     (applyDMARC's escalation branch). The routing decision outlives the
//     action that caused it.
//
// Either way, leaving it alone files an admin-approved message into Junk: a
// softer replay of the silent non-delivery bug this function exists to fix.
// Release therefore forces "INBOX" for both. Any OTHER non-empty value is
// left untouched — that can only be a genuine folder target, not a spam
// verdict, and overriding it would discard a real routing decision.
func PrepareForRelease(env *bongov1.QueueEnvelope) {
	env.Flags = ClearFlag(env.Flags, MsgFlagQuarantined)
	env.CurrentStage = ""
	if env.TargetMailbox == "" || env.TargetMailbox == "Junk" {
		env.TargetMailbox = "INBOX"
	}
}
