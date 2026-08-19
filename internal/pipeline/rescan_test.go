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
