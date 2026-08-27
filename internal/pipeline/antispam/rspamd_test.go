package antispam

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// rspamdFixture returns a JSON-encoded Rspamd /checkv2 response.
func rspamdFixture(action string, score, requiredScore float64, symbols map[string]rspamdSymbol) []byte {
	r := rspamdResponse{
		Score:         score,
		RequiredScore: requiredScore,
		Action:        action,
		Symbols:       symbols,
	}
	b, _ := json.Marshal(r)
	return b
}

func TestRspamdClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(rspamdFixture("no action", 1.2, 15.0, map[string]rspamdSymbol{
			"DKIM_VALID": {Score: -0.1, Description: "DKIM signature valid"},
		}))
	}))
	defer srv.Close()

	p := NewRspamdProcessor(WithRspamdURL(srv.URL))
	env := &bongov1.QueueEnvelope{Sender: "sender@example.com"}
	result, err := p.Process(t.Context(), env, strings.NewReader("From: sender@example.com\r\n\r\nHello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "pass" {
		t.Errorf("action = %q, want %q", result.Action, "pass")
	}
	if result.Backend != "rspamd" {
		t.Errorf("backend = %q, want %q", result.Backend, "rspamd")
	}
	if result.Stage != "antispam" {
		t.Errorf("stage = %q, want %q", result.Stage, "antispam")
	}
	// 1.2 / 15.0 = 0.08; score must be well below 1.0
	if result.Score >= 1.0 {
		t.Errorf("score = %f, want < 1.0", result.Score)
	}
}

func TestRspamdSpam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(rspamdFixture("reject", 18.5, 15.0, map[string]rspamdSymbol{
			"BAYES_SPAM": {Score: 5.0, Description: "Bayes spam"},
			"SPF_FAIL":   {Score: 2.5, Description: "SPF check failed"},
		}))
	}))
	defer srv.Close()

	p := NewRspamdProcessor(WithRspamdURL(srv.URL))
	env := &bongov1.QueueEnvelope{Sender: "spammer@example.com"}
	result, err := p.Process(t.Context(), env, strings.NewReader("From: spammer@example.com\r\n\r\nBuy now!"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "quarantine" {
		t.Errorf("action = %q, want %q", result.Action, "quarantine")
	}
	// 18.5 / 15.0 > 1.0, so score must be capped at 1.0.
	if result.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", result.Score)
	}
	if len(result.Reasons) == 0 {
		t.Error("expected reasons to be populated")
	}
}

func TestRspamdAddHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(rspamdFixture("add header", 8.0, 15.0, map[string]rspamdSymbol{
			"SUSPICIOUS_LINK": {Score: 3.5, Description: "Suspicious URL detected"},
		}))
	}))
	defer srv.Close()

	p := NewRspamdProcessor(WithRspamdURL(srv.URL))
	env := &bongov1.QueueEnvelope{Sender: "maybe@example.com"}
	result, err := p.Process(t.Context(), env, strings.NewReader("From: maybe@example.com\r\n\r\nCheck this out"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Action != "tag" {
		t.Errorf("action = %q, want %q", result.Action, "tag")
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("TargetMailbox = %q, want %q", env.TargetMailbox, "Junk")
	}
}

func TestRspamdUnreachable(t *testing.T) {
	// Use a port that is not listening to simulate an unreachable server.
	p := NewRspamdProcessor(WithRspamdURL("http://127.0.0.1:19999"))
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	result, err := p.Process(t.Context(), env, strings.NewReader("From: user@example.com\r\n\r\nHello"))
	if err != nil {
		t.Fatalf("unexpected error: %v (graceful fallback should not error)", err)
	}

	if result.Action != "pass" {
		t.Errorf("action = %q, want %q (graceful fallback)", result.Action, "pass")
	}
	if result.Score != 0 {
		t.Errorf("score = %f, want 0 on fallback", result.Score)
	}
}

func TestRspamdScoreNormalisation(t *testing.T) {
	tests := []struct {
		name          string
		score         float64
		requiredScore float64
		wantScore     float32
	}{
		{
			name:          "exactly at threshold",
			score:         15.0,
			requiredScore: 15.0,
			wantScore:     1.0,
		},
		{
			name:          "above threshold capped",
			score:         15.3,
			requiredScore: 15.0,
			wantScore:     1.0,
		},
		{
			name:          "below threshold",
			score:         5.2,
			requiredScore: 15.0,
			wantScore:     float32(5.2 / 15.0),
		},
		{
			name:          "zero score",
			score:         0,
			requiredScore: 15.0,
			wantScore:     0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(rspamdFixture("no action", tc.score, tc.requiredScore, nil))
			}))
			defer srv.Close()

			p := NewRspamdProcessor(WithRspamdURL(srv.URL))
			env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
			result, err := p.Process(t.Context(), env, strings.NewReader("From: user@example.com\r\n\r\nHi"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Allow a small tolerance for float32 conversion.
			const epsilon = 0.0001
			diff := float64(result.Score) - float64(tc.wantScore)
			if diff < -epsilon || diff > epsilon {
				t.Errorf("score = %f, want %f", result.Score, tc.wantScore)
			}
		})
	}
}

func TestRspamdCustomHeaders(t *testing.T) {
	var receivedPassword string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPassword = r.Header.Get("Password")
		w.Header().Set("Content-Type", "application/json")
		w.Write(rspamdFixture("no action", 1.0, 15.0, nil))
	}))
	defer srv.Close()

	p := NewRspamdProcessor(
		WithRspamdURL(srv.URL),
		WithRspamdHeaders(map[string]string{
			"Password": "supersecret",
		}),
	)
	env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
	_, err := p.Process(t.Context(), env, strings.NewReader("From: user@example.com\r\n\r\nHi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedPassword != "supersecret" {
		t.Errorf("Password header = %q, want %q", receivedPassword, "supersecret")
	}
}

func TestRspamdActionMapping(t *testing.T) {
	tests := []struct {
		rspamdAction   string
		expectedAction string
		expectJunk     bool
	}{
		{"no action", "pass", false},
		{"greylist", "pass", false},
		{"add header", "tag", true},
		{"rewrite subject", "tag", true},
		{"soft reject", "tag", true},
		{"reject", "quarantine", false},
		{"unknown", "pass", false},
	}

	for _, tc := range tests {
		t.Run(tc.rspamdAction, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(rspamdFixture(tc.rspamdAction, 5.0, 15.0, nil))
			}))
			defer srv.Close()

			p := NewRspamdProcessor(WithRspamdURL(srv.URL))
			env := &bongov1.QueueEnvelope{Sender: "user@example.com"}
			result, err := p.Process(t.Context(), env, strings.NewReader("From: user@example.com\r\n\r\nHi"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.Action != tc.expectedAction {
				t.Errorf("rspamd action %q: got pipeline action %q, want %q",
					tc.rspamdAction, result.Action, tc.expectedAction)
			}

			if tc.expectJunk && env.TargetMailbox != "Junk" {
				t.Errorf("rspamd action %q: TargetMailbox = %q, want %q",
					tc.rspamdAction, env.TargetMailbox, "Junk")
			}
			if !tc.expectJunk && env.TargetMailbox == "Junk" {
				t.Errorf("rspamd action %q: TargetMailbox unexpectedly set to Junk", tc.rspamdAction)
			}
		})
	}
}
