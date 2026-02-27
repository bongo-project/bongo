package bongov1

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestQueueEnvelopeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *QueueEnvelope
	}{
		{
			name: "empty envelope",
			msg:  &QueueEnvelope{},
		},
		{
			name: "full envelope with single recipient",
			msg: &QueueEnvelope{
				Id:           "550e8400-e29b-41d4-a716-446655440000",
				Sender:       "alice@example.com",
				Recipients:   []string{"bob@example.com"},
				ReceivedAt:   1700000000,
				SourceIp:     "203.0.113.42",
				BlobHash:     "abc123def456",
				Flags:        3,
				SpfResult:    "pass",
				CurrentStage: "antispam",
				TargetMailbox: "INBOX",
			},
		},
		{
			name: "envelope with multiple recipients and stages",
			msg: &QueueEnvelope{
				Id:         "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
				Sender:     "sender@domain.org",
				Recipients: []string{"r1@example.com", "r2@example.com", "r3@example.com"},
				ReceivedAt: 1700001234,
				SourceIp:   "192.0.2.1",
				BlobHash:   "deadbeef00112233",
				Flags:      63,
				SpfResult:  "fail",
				Stages: []*StageResult{
					{
						Stage:       "antispam",
						Backend:     "rspamd",
						Action:      "tag",
						Score:       0.85,
						Reasons:     []string{"BAYES_SPAM", "DMARC_POLICY_REJECT"},
						ProcessedAt: 1700001235,
						DurationMs:  42,
					},
					{
						Stage:       "antivirus",
						Backend:     "clamav",
						Action:      "pass",
						Score:       0.0,
						Reasons:     []string{},
						ProcessedAt: 1700001236,
						DurationMs:  15,
					},
				},
				CurrentStage:  "rules",
				TargetMailbox: "Spam",
			},
		},
		{
			name: "envelope with zero flags",
			msg: &QueueEnvelope{
				Id:        "00000000-0000-0000-0000-000000000000",
				Sender:    "noreply@example.com",
				BlobHash:  "0000000000000000",
				SpfResult: "neutral",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proto.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("proto.Marshal() error = %v", err)
			}

			got := &QueueEnvelope{}
			if err := proto.Unmarshal(data, got); err != nil {
				t.Fatalf("proto.Unmarshal() error = %v", err)
			}

			if !proto.Equal(tt.msg, got) {
				t.Errorf("round-trip mismatch:\n  original = %v\n  got      = %v", tt.msg, got)
			}
		})
	}
}

func TestStageResultRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *StageResult
	}{
		{
			name: "empty stage result",
			msg:  &StageResult{},
		},
		{
			name: "antispam pass",
			msg: &StageResult{
				Stage:       "antispam",
				Backend:     "builtin",
				Action:      "pass",
				Score:       0.1,
				Reasons:     []string{"low score"},
				ProcessedAt: 1700000100,
				DurationMs:  5,
			},
		},
		{
			name: "antispam reject with multiple reasons",
			msg: &StageResult{
				Stage:       "antispam",
				Backend:     "rspamd",
				Action:      "reject",
				Score:       0.99,
				Reasons:     []string{"BAYES_SPAM", "PHISHING", "RCVD_IN_DNSWL_BLOCKED"},
				ProcessedAt: 1700000200,
				DurationMs:  120,
			},
		},
		{
			name: "antivirus virus detected",
			msg: &StageResult{
				Stage:       "antivirus",
				Backend:     "clamav",
				Action:      "quarantine",
				Score:       1.0,
				Reasons:     []string{"Eicar-Test-Signature"},
				ProcessedAt: 1700000300,
				DurationMs:  300,
			},
		},
		{
			name: "rules stage",
			msg: &StageResult{
				Stage:       "rules",
				Backend:     "builtin",
				Action:      "pass",
				Score:       0.0,
				Reasons:     []string{},
				ProcessedAt: 1700000400,
				DurationMs:  1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proto.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("proto.Marshal() error = %v", err)
			}

			got := &StageResult{}
			if err := proto.Unmarshal(data, got); err != nil {
				t.Fatalf("proto.Unmarshal() error = %v", err)
			}

			if !proto.Equal(tt.msg, got) {
				t.Errorf("round-trip mismatch:\n  original = %v\n  got      = %v", tt.msg, got)
			}
		})
	}
}

func TestQueueEnvelopeFlags(t *testing.T) {
	// Verify that the flags field survives serialization for all relevant bit patterns.
	flagValues := []uint32{0, 1, 2, 4, 8, 16, 32, 63, 0xFFFFFFFF}

	for _, flags := range flagValues {
		env := &QueueEnvelope{
			Id:    "test-id",
			Flags: flags,
		}

		data, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("flags=%d: proto.Marshal() error = %v", flags, err)
		}

		got := &QueueEnvelope{}
		if err := proto.Unmarshal(data, got); err != nil {
			t.Fatalf("flags=%d: proto.Unmarshal() error = %v", flags, err)
		}

		if got.Flags != flags {
			t.Errorf("flags=%d: round-trip got %d", flags, got.Flags)
		}
	}
}
