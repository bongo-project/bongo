package pipeline

import (
	"context"
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// TestPrepareForRescan verifies the flag/stage reset in isolation.
func TestPrepareForRescan(t *testing.T) {
	env := &bongov1.QueueEnvelope{
		Id:           "msg-rescan-unit",
		Flags:        MsgFlagVirusChecked | MsgFlagVirus | MsgFlagQuarantined | MsgFlagSpamChecked | MsgFlagSpam | MsgFlagRulesChecked | MsgFlagDelivered,
		CurrentStage: "antivirus",
		Stages: []*bongov1.StageResult{
			{Stage: "rules", Action: "pass"},
			{Stage: "antispam", Action: "pass"},
			{Stage: "antivirus", Action: "quarantine"},
		},
	}

	PrepareForRescan(env)

	for _, flag := range []struct {
		name string
		bit  uint32
	}{
		{"MsgFlagQuarantined", MsgFlagQuarantined},
		{"MsgFlagVirusChecked", MsgFlagVirusChecked},
		{"MsgFlagVirus", MsgFlagVirus},
		{"MsgFlagSpamChecked", MsgFlagSpamChecked},
		{"MsgFlagSpam", MsgFlagSpam},
	} {
		if HasFlag(env.Flags, flag.bit) {
			t.Errorf("expected %s to be cleared after PrepareForRescan", flag.name)
		}
	}

	// Rules/delivered flags must survive — those stages must not re-run.
	if !HasFlag(env.Flags, MsgFlagRulesChecked) {
		t.Error("expected MsgFlagRulesChecked to be preserved")
	}
	if !HasFlag(env.Flags, MsgFlagDelivered) {
		t.Error("expected MsgFlagDelivered to be preserved")
	}

	if len(env.Stages) != 1 || env.Stages[0].Stage != "rules" {
		t.Errorf("expected only the rules StageResult to remain, got %+v", env.Stages)
	}

	if env.CurrentStage != "" {
		t.Errorf("expected CurrentStage to be reset, got %q", env.CurrentStage)
	}
}

// TestPrepareForRescan_EmptyStages verifies PrepareForRescan is safe on an
// envelope with no stage history (e.g. malformed/legacy data).
func TestPrepareForRescan_EmptyStages(t *testing.T) {
	env := &bongov1.QueueEnvelope{
		Id:    "msg-rescan-empty",
		Flags: MsgFlagQuarantined,
	}

	PrepareForRescan(env)

	if env.Flags != 0 {
		t.Errorf("expected flags cleared to 0, got %b", env.Flags)
	}
	if len(env.Stages) != 0 {
		t.Errorf("expected no stages, got %d", len(env.Stages))
	}
}

// TestRunnerRescan_AntivirusStageActuallyReRuns is the load-bearing test for
// task 12.4: it proves that after PrepareForRescan, the antivirus stage is
// actually invoked by the runner rather than skipped by the idempotency
// check in processMessage. A test that only asserted the file moved
// directories would pass even if the flag-clearing were missing entirely —
// that is exactly the bug this task exists to prevent.
func TestRunnerRescan_AntivirusStageActuallyReRuns(t *testing.T) {
	spool := newTestSpool(t)

	env := &bongov1.QueueEnvelope{
		Id:       "msg-rescan-av",
		Sender:   "sender@example.com",
		BlobHash: "deadbeef",
		Flags:    MsgFlagVirusChecked | MsgFlagVirus | MsgFlagQuarantined,
		Stages: []*bongov1.StageResult{
			{Stage: "antivirus", Action: "quarantine", Reasons: []string{"stale verdict"}},
		},
	}

	PrepareForRescan(env)

	if err := spool.Enqueue(env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	proc := &mockProcessor{
		name:   "antivirus",
		result: &bongov1.StageResult{Stage: "antivirus", Action: "pass"},
	}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if proc.calls != 1 {
		t.Errorf("expected antivirus stage to run exactly once after rescan, got %d calls", proc.calls)
	}
	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("expected envelope in deliver/ after a passing rescan")
	}
}

// TestRunnerRescan_WithoutPrepare_SkipsStage is the paired negative control:
// without PrepareForRescan, the stale MsgFlagVirusChecked flag makes the
// runner skip the antivirus stage entirely, proving that PrepareForRescan
// (and not some other mechanism) is what defeats the idempotency check.
func TestRunnerRescan_WithoutPrepare_SkipsStage(t *testing.T) {
	spool := newTestSpool(t)

	env := &bongov1.QueueEnvelope{
		Id:       "msg-rescan-av-noprep",
		Sender:   "sender@example.com",
		BlobHash: "deadbeef",
		Flags:    MsgFlagVirusChecked | MsgFlagVirus | MsgFlagQuarantined,
		Stages: []*bongov1.StageResult{
			{Stage: "antivirus", Action: "quarantine", Reasons: []string{"stale verdict"}},
		},
	}

	if err := spool.Enqueue(env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	proc := &mockProcessor{
		name:   "antivirus",
		result: &bongov1.StageResult{Stage: "antivirus", Action: "pass"},
	}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if proc.calls != 0 {
		t.Errorf("expected antivirus stage to be skipped (0 calls) without PrepareForRescan, got %d", proc.calls)
	}
}

// TestRunnerRescan_AntispamStageActuallyReRuns proves the same for the
// antispam stage, and that a stage still returning "quarantine" after
// rescan puts the message back in quarantine/, not deliver/. This is the
// regression guard for the corrected task decision: clearing only the
// antivirus flags would let a spam-quarantined message skip antispam
// entirely and deliver unscored.
func TestRunnerRescan_AntispamStageActuallyReRuns(t *testing.T) {
	spool := newTestSpool(t)

	env := &bongov1.QueueEnvelope{
		Id:       "msg-rescan-spam",
		Sender:   "sender@example.com",
		BlobHash: "deadbeef",
		Flags:    MsgFlagSpamChecked | MsgFlagSpam | MsgFlagQuarantined,
		Stages: []*bongov1.StageResult{
			{Stage: "antispam", Action: "quarantine", Score: 0.95},
		},
	}

	PrepareForRescan(env)

	if err := spool.Enqueue(env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	proc := &mockProcessor{
		name: "antispam",
		result: &bongov1.StageResult{
			Stage:  "antispam",
			Action: "quarantine",
			Score:  0.95,
		},
	}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if proc.calls != 1 {
		t.Errorf("expected antispam stage to run exactly once after rescan, got %d calls", proc.calls)
	}
	if !fileExistsIn(t, spool, "quarantine", env.Id) {
		t.Error("expected a message still scored as spam to land back in quarantine/")
	}
	if fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("message re-quarantined by antispam must not be in deliver/")
	}
}

// TestPrepareForRelease verifies the flag/target-mailbox behavior in
// isolation.
func TestPrepareForRelease(t *testing.T) {
	t.Run("clears only quarantined flag, retains checked/outcome flags and stages", func(t *testing.T) {
		env := &bongov1.QueueEnvelope{
			Id:           "msg-release-unit",
			Flags:        MsgFlagSpamChecked | MsgFlagSpam | MsgFlagVirusChecked | MsgFlagQuarantined,
			CurrentStage: "antispam",
			Stages: []*bongov1.StageResult{
				{Stage: "antispam", Action: "quarantine", Score: 0.95},
			},
		}

		PrepareForRelease(env)

		if HasFlag(env.Flags, MsgFlagQuarantined) {
			t.Error("expected MsgFlagQuarantined to be cleared")
		}
		for _, flag := range []struct {
			name string
			bit  uint32
		}{
			{"MsgFlagSpamChecked", MsgFlagSpamChecked},
			{"MsgFlagSpam", MsgFlagSpam},
			{"MsgFlagVirusChecked", MsgFlagVirusChecked},
		} {
			if !HasFlag(env.Flags, flag.bit) {
				t.Errorf("expected %s to be retained after PrepareForRelease (unlike rescan)", flag.name)
			}
		}
		if len(env.Stages) != 1 {
			t.Errorf("expected StageResults retained as audit record, got %d", len(env.Stages))
		}
		if env.CurrentStage != "" {
			t.Errorf("expected CurrentStage to be reset, got %q", env.CurrentStage)
		}
	})

	t.Run("forces empty TargetMailbox to INBOX", func(t *testing.T) {
		env := &bongov1.QueueEnvelope{Id: "msg-release-mailbox", Flags: MsgFlagQuarantined}
		PrepareForRelease(env)
		if env.TargetMailbox != "INBOX" {
			t.Errorf("TargetMailbox = %q, want INBOX", env.TargetMailbox)
		}
	})

	// A message scored in the tag band gets TargetMailbox="Junk" from the
	// scorer; DMARC can then escalate the action to reject/quarantine WITHOUT
	// clearing that routing decision (see applyDMARC's escalation branch), so
	// the envelope arrives in quarantine already addressed to Junk. Releasing
	// it must not honour that — the admin has overridden the very verdict that
	// set it. Without this case, release silently files approved mail to Junk.
	t.Run("forces a verdict-derived Junk target to INBOX", func(t *testing.T) {
		env := &bongov1.QueueEnvelope{
			Id:            "msg-release-junk-target",
			Flags:         MsgFlagSpamChecked | MsgFlagSpam | MsgFlagQuarantined,
			TargetMailbox: "Junk",
			Stages: []*bongov1.StageResult{
				{Stage: "antispam", Action: "reject", Score: 0.6, Reasons: []string{"dmarc_reject"}},
			},
		}
		PrepareForRelease(env)
		if env.TargetMailbox != "INBOX" {
			t.Errorf("TargetMailbox = %q, want INBOX — releasing an admin-approved message must undo the spam verdict's routing, not preserve it", env.TargetMailbox)
		}
	})

	t.Run("leaves a genuine folder target untouched", func(t *testing.T) {
		env := &bongov1.QueueEnvelope{Id: "msg-release-mailbox-set", Flags: MsgFlagQuarantined, TargetMailbox: "Projects"}
		PrepareForRelease(env)
		if env.TargetMailbox != "Projects" {
			t.Errorf("TargetMailbox = %q, want unchanged 'Projects' — only verdict-derived targets are overridden", env.TargetMailbox)
		}
	})
}

// TestRunnerRelease_AntispamStageSkipped is the pipeline-package load-bearing
// test for release: it proves that after PrepareForRelease, the antispam
// stage that already quarantined the message is NOT re-invoked, and the
// message proceeds straight through to deliver/. This is the mirror image
// of TestRunnerRescan_AntispamStageActuallyReRuns above, and is what makes
// release different from rescan in practice, not just in the doc comment.
func TestRunnerRelease_AntispamStageSkipped(t *testing.T) {
	spool := newTestSpool(t)

	env := &bongov1.QueueEnvelope{
		Id:       "msg-release-spam",
		Sender:   "sender@example.com",
		BlobHash: "deadbeef",
		Flags:    MsgFlagSpamChecked | MsgFlagSpam | MsgFlagQuarantined,
		Stages: []*bongov1.StageResult{
			{Stage: "antispam", Action: "quarantine", Score: 0.95},
		},
	}

	PrepareForRelease(env)

	if err := spool.Enqueue(env); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// If PrepareForRelease's flag-clearing regressed to also clearing
	// MsgFlagSpamChecked (turning release into rescan), this stub would be
	// invoked and re-quarantine the message with the same verdict — the
	// exact "release button does nothing" failure this task exists to fix.
	proc := &mockProcessor{
		name: "antispam",
		result: &bongov1.StageResult{
			Stage:  "antispam",
			Action: "quarantine",
			Score:  0.95,
		},
	}
	runner := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())

	dequeued, err := spool.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := runner.processMessage(context.Background(), dequeued); err != nil {
		t.Fatalf("processMessage: %v", err)
	}

	if proc.calls != 0 {
		t.Errorf("expected antispam stage to be skipped on release, got %d calls", proc.calls)
	}
	if !fileExistsIn(t, spool, "deliver", env.Id) {
		t.Error("expected released message to reach deliver/ without re-running antispam")
	}
	if fileExistsIn(t, spool, "quarantine", env.Id) {
		t.Error("released message must not be re-quarantined")
	}

	delivered, err := spool.ReadEnvelope("deliver", env.Id)
	if err != nil {
		t.Fatalf("ReadEnvelope deliver: %v", err)
	}
	if delivered.TargetMailbox != "INBOX" {
		t.Errorf("delivered.TargetMailbox = %q, want INBOX (released mail must not be silently filed to Junk)", delivered.TargetMailbox)
	}
}
