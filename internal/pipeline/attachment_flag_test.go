package pipeline

import (
	"context"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestFlagsForAttachmentPolicy(t *testing.T) {
	checked, outcome := flagsForStage("attachment_policy")
	if checked != MsgFlagAttachmentChecked || outcome != 0 {
		t.Fatalf("flagsForStage = %d, %d", checked, outcome)
	}
	for _, other := range []uint32{MsgFlagSpamChecked, MsgFlagVirusChecked, MsgFlagRulesChecked, MsgFlagSpam, MsgFlagVirus, MsgFlagQuarantined, MsgFlagDelivered} {
		if other == MsgFlagAttachmentChecked {
			t.Fatalf("flag collides with %d", other)
		}
	}
}

func TestPrepareForRescanClearsAttachmentPolicy(t *testing.T) {
	env := &bongov1.QueueEnvelope{
		Flags: MsgFlagAttachmentChecked | MsgFlagQuarantined | MsgFlagRulesChecked,
		Stages: []*bongov1.StageResult{
			{Stage: "attachment_policy", Action: "quarantine"},
			{Stage: "rules", Action: "pass"},
		},
	}
	PrepareForRescan(env)
	if HasFlag(env.Flags, MsgFlagAttachmentChecked) {
		t.Error("rescan must check attachments again")
	}
	if !HasFlag(env.Flags, MsgFlagRulesChecked) {
		t.Error("rules flag must survive")
	}
	if len(env.Stages) != 1 || env.Stages[0].Stage != "rules" {
		t.Errorf("stages = %+v", env.Stages)
	}
}

func TestPrepareForRescanKeepsStripResult(t *testing.T) {
	env := &bongov1.QueueEnvelope{
		Flags: MsgFlagAttachmentChecked,
		Stages: []*bongov1.StageResult{
			{Stage: "attachment_policy", Action: "strip", DkimResult: "pass", DkimDomain: "example.test"},
			{Stage: "attachment_policy", Action: "quarantine"},
		},
	}
	PrepareForRescan(env)
	if len(env.Stages) != 1 || env.Stages[0].Action != "strip" || env.Stages[0].DkimResult != "pass" {
		t.Fatalf("stages = %+v", env.Stages)
	}
}

func TestPrepareForReleaseKeepsAttachmentPolicy(t *testing.T) {
	env := &bongov1.QueueEnvelope{
		Flags:  MsgFlagAttachmentChecked | MsgFlagQuarantined,
		Stages: []*bongov1.StageResult{{Stage: "attachment_policy", Action: "quarantine"}},
	}
	PrepareForRelease(env)
	if !HasFlag(env.Flags, MsgFlagAttachmentChecked) || len(env.Stages) != 1 {
		t.Fatalf("release must keep the verdict: %+v", env)
	}
}

func TestRunnerAttachmentPolicyReleaseSkipsRescanReruns(t *testing.T) {
	run := func(t *testing.T, prep func(*bongov1.QueueEnvelope)) int {
		spool := newTestSpool(t)
		env := &bongov1.QueueEnvelope{
			Id: "msg-att", Sender: "s@example.com", BlobHash: "deadbeef",
			Flags:  MsgFlagAttachmentChecked | MsgFlagQuarantined,
			Stages: []*bongov1.StageResult{{Stage: "attachment_policy", Action: "quarantine"}},
		}
		prep(env)
		if err := spool.Enqueue(env); err != nil {
			t.Fatal(err)
		}
		proc := &mockProcessor{name: "attachment_policy", result: &bongov1.StageResult{Stage: "attachment_policy", Action: "pass"}}
		r := NewRunner(spool, &mockBlobReader{}, []Processor{proc}, newTestLogger())
		d, err := spool.Dequeue()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.processMessage(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		return proc.calls
	}
	if n := run(t, PrepareForRelease); n != 0 {
		t.Errorf("release re-ran the stage %d times", n)
	}
	if n := run(t, PrepareForRescan); n != 1 {
		t.Errorf("rescan ran the stage %d times, want 1", n)
	}
}
