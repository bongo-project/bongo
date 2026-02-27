package antispam

import (
	"context"
	"strings"
	"testing"

	"github.com/bongo-go/bongo-go/internal/antispam"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// testMessage builds a minimal RFC 5322 message for testing.
func testMessage(from, subject, body string, extraHeaders map[string]string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: recipient@example.com\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("Message-ID: <test@example.com>\r\n")
	sb.WriteString("Date: Thu, 01 Jan 2026 00:00:00 +0000\r\n")
	for k, v := range extraHeaders {
		sb.WriteString(k + ": " + v + "\r\n")
	}
	sb.WriteString("Content-Type: text/plain\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}

func TestBuiltinPass(t *testing.T) {
	scorer := antispam.NewScorer()
	proc := NewBuiltinProcessor(scorer)

	// A clean, well-formed message should score below the default threshold.
	msg := testMessage("sender@example.com", "Hello from a friend", "Just wanted to say hi.", nil)
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)", "pass", result.Action, result.Score, result.Reasons)
	}
	if result.Stage != "antispam" {
		t.Errorf("expected stage %q, got %q", "antispam", result.Stage)
	}
	if result.Backend != "builtin" {
		t.Errorf("expected backend %q, got %q", "builtin", result.Backend)
	}
	// TargetMailbox must not have been modified.
	if env.TargetMailbox != "" {
		t.Errorf("expected empty TargetMailbox, got %q", env.TargetMailbox)
	}
}

func TestBuiltinTag(t *testing.T) {
	scorer := antispam.NewScorer()
	// Use a low threshold so a slightly suspicious message is tagged.
	proc := NewBuiltinProcessor(scorer,
		WithThreshold(0.1),
		WithHighThreshold(0.9),
	)

	// Missing SPF + empty subject accumulates just enough score.
	msg := testMessage("sender@example.com", "", "Buy now!", nil)
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "fail",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "tag" {
		t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)", "tag", result.Action, result.Score, result.Reasons)
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("expected TargetMailbox %q, got %q", "Junk", env.TargetMailbox)
	}
}

func TestBuiltinQuarantine(t *testing.T) {
	scorer := antispam.NewScorer()
	// Use very low thresholds so even a mildly suspicious message is quarantined.
	proc := NewBuiltinProcessor(scorer,
		WithThreshold(0.05),
		WithHighThreshold(0.1),
	)

	// SPF fail + missing Date/Message-ID + empty subject drives a high score.
	msg := "From: spammer@evil.com\r\nSubject: \r\nContent-Type: text/plain\r\n\r\nBuy cheap meds now!"
	env := &bongov1.QueueEnvelope{
		Sender:     "spammer@evil.com",
		Recipients: []string{"victim@example.com"},
		SpfResult:  "fail",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "quarantine" {
		t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)", "quarantine", result.Action, result.Score, result.Reasons)
	}
}

func TestBuiltinNilBayes(t *testing.T) {
	scorer := antispam.NewScorer()
	// Explicitly pass no WithBayes option — bayes should remain nil.
	proc := NewBuiltinProcessor(scorer)

	if proc.bayes != nil {
		t.Fatal("expected bayes to be nil by default")
	}

	msg := testMessage("sender@example.com", "Regular newsletter", "Here is your update.", nil)
	env := &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		SpfResult:  "pass",
	}

	result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	// Should succeed without panicking and return a valid stage name.
	if result.Stage != "antispam" {
		t.Errorf("expected stage %q, got %q", "antispam", result.Stage)
	}
}

func TestBuiltinCustomThresholds(t *testing.T) {
	tests := []struct {
		name          string
		threshold     float32
		highThreshold float32
		spfResult     string
		subject       string
		wantAction    string
		wantJunk      bool
	}{
		{
			name:          "custom low threshold causes tag",
			threshold:     0.1,
			highThreshold: 0.8,
			spfResult:     "softfail",
			subject:       "Hello",
			wantAction:    "tag",
			wantJunk:      true,
		},
		{
			name:          "high thresholds cause pass",
			threshold:     0.95,
			highThreshold: 0.99,
			spfResult:     "fail",
			subject:       "Free money winner congratulations act now",
			wantAction:    "pass",
			wantJunk:      false,
		},
		{
			name:          "very low high threshold causes quarantine",
			threshold:     0.05,
			highThreshold: 0.06,
			spfResult:     "fail",
			subject:       "Hi",
			wantAction:    "quarantine",
			wantJunk:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scorer := antispam.NewScorer()
			proc := NewBuiltinProcessor(scorer,
				WithThreshold(tc.threshold),
				WithHighThreshold(tc.highThreshold),
			)

			msg := testMessage("sender@example.com", tc.subject, "Body text.", nil)
			env := &bongov1.QueueEnvelope{
				Sender:     "sender@example.com",
				Recipients: []string{"recipient@example.com"},
				SpfResult:  tc.spfResult,
			}

			result, err := proc.Process(context.Background(), env, strings.NewReader(msg))
			if err != nil {
				t.Fatalf("Process returned unexpected error: %v", err)
			}
			if result.Action != tc.wantAction {
				t.Errorf("expected action %q, got %q (score=%.3f, reasons=%v)",
					tc.wantAction, result.Action, result.Score, result.Reasons)
			}
			if tc.wantJunk && env.TargetMailbox != "Junk" {
				t.Errorf("expected TargetMailbox %q, got %q", "Junk", env.TargetMailbox)
			}
			if !tc.wantJunk && result.Action != "tag" && env.TargetMailbox == "Junk" {
				t.Errorf("expected TargetMailbox to not be Junk")
			}
		})
	}
}

func TestBuiltinName(t *testing.T) {
	proc := NewBuiltinProcessor(antispam.NewScorer())
	if proc.Name() != "antispam" {
		t.Errorf("expected Name() %q, got %q", "antispam", proc.Name())
	}
}

func TestBuiltinExtractBasicHeaders(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantSubject   string
		wantHeaderKey string
		wantHeaderVal string
	}{
		{
			name:          "simple headers",
			raw:           "Subject: Test subject\r\nFrom: foo@bar.com\r\n\r\nBody",
			wantSubject:   "Test subject",
			wantHeaderKey: "From",
			wantHeaderVal: "foo@bar.com",
		},
		{
			name:          "folded header",
			raw:           "Subject: Long\r\n folded subject\r\n\r\nBody",
			wantSubject:   "Long folded subject",
			wantHeaderKey: "Subject",
			wantHeaderVal: "Long folded subject",
		},
		{
			name:          "empty input",
			raw:           "\r\n\r\nBody only",
			wantSubject:   "",
			wantHeaderKey: "",
			wantHeaderVal: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, headers := extractBasicHeaders([]byte(tc.raw))
			if subject != tc.wantSubject {
				t.Errorf("subject: want %q, got %q", tc.wantSubject, subject)
			}
			if tc.wantHeaderKey != "" {
				if got := headers[tc.wantHeaderKey]; got != tc.wantHeaderVal {
					t.Errorf("header[%q]: want %q, got %q", tc.wantHeaderKey, tc.wantHeaderVal, got)
				}
			}
		})
	}
}
