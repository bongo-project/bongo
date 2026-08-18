package antispam

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// saTestMsg returns a minimal RFC 5322 message for SpamAssassin tests.
func saTestMsg() string {
	return testMessage("sender@example.com", "Test", "Hello world.", nil)
}

// mockSpamd starts a TCP listener that accepts one connection, reads the
// SPAMC request, and writes the provided response. It returns the listening
// address and a channel that is closed once the goroutine finishes.
func mockSpamd(t *testing.T, response string) (addr string, done <-chan struct{}) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mockSpamd: listen: %v", err)
	}

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		defer ln.Close() //nolint:errcheck

		conn, err := ln.Accept()
		if err != nil {
			// listener was closed before a connection arrived (e.g. test done early)
			return
		}
		defer conn.Close() //nolint:errcheck

		// Drain the SPAMC request so the client does not get ECONNRESET while writing.
		scanner := bufio.NewReader(conn)
		// Read the request line and headers until blank line.
		var contentLength int
		for {
			line, err := scanner.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			var n int
			fmt.Sscanf(line, "Content-length: %d", &n)
			if n > 0 {
				contentLength = n
			}
		}
		// Drain message body.
		if contentLength > 0 {
			io.ReadFull(scanner, make([]byte, contentLength)) //nolint:errcheck
		}

		// Send the canned response.
		conn.Write([]byte(response)) //nolint:errcheck
	}()

	return ln.Addr().String(), ch
}

// hostPort splits an addr string returned by net.Listener.Addr().String().
func hostPort(t *testing.T, addr string) (host string, port int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("hostPort: %v", err)
	}
	var portInt int
	fmt.Sscanf(p, "%d", &portInt)
	return h, portInt
}

// newProcessor builds a SpamAssassinProcessor pointed at the given addr.
func newProcessor(t *testing.T, addr string) *SpamAssassinProcessor {
	t.Helper()
	h, p := hostPort(t, addr)
	return NewSpamAssassinProcessor(
		WithSAHost(h),
		WithSAPort(p),
		WithSATimeout(5*time.Second),
	)
}

func testEnvelope() *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Sender:     "sender@example.com",
		Recipients: []string{"rcpt@example.com"},
	}
}

// approxEqual returns true when a and b differ by less than epsilon.
func approxEqual(a, b, epsilon float32) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}

func TestSAClean(t *testing.T) {
	response := "SPAMD/1.5 0 EX_OK\r\n" +
		"Spam: False ; 1.2 / 5.0\r\n" +
		"\r\n" +
		"DATE_IN_PAST_03_06,NO_RELAYS\r\n"

	addr, done := mockSpamd(t, response)

	proc := newProcessor(t, addr)
	result, err := proc.Process(context.Background(), testEnvelope(), strings.NewReader(saTestMsg()))
	<-done

	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass, got %q (score=%.3f)", result.Action, result.Score)
	}
	// normalised = 1.2 / 5.0 = 0.24, well below tag threshold 0.5
	const wantScore = float32(1.2) / float32(5.0)
	if !approxEqual(result.Score, wantScore, 0.001) {
		t.Errorf("expected score ~%.3f, got %.3f", wantScore, result.Score)
	}
	if result.Stage != "antispam" {
		t.Errorf("expected stage=antispam, got %q", result.Stage)
	}
	if result.Backend != "spamassassin" {
		t.Errorf("expected backend=spamassassin, got %q", result.Backend)
	}
}

func TestSASpam(t *testing.T) {
	response := "SPAMD/1.5 0 EX_OK\r\n" +
		"Spam: True ; 15.3 / 5.0\r\n" +
		"\r\n" +
		"BAYES_99,HTML_MESSAGE,RAZOR2_CHECK\r\n"

	addr, done := mockSpamd(t, response)

	proc := newProcessor(t, addr)
	env := testEnvelope()
	result, err := proc.Process(context.Background(), env, strings.NewReader(saTestMsg()))
	<-done

	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	// 15.3 / 5.0 = 3.06, capped to 1.0 -> quarantine
	if result.Action != "quarantine" {
		t.Errorf("expected action=quarantine, got %q (score=%.3f)", result.Action, result.Score)
	}
	if result.Score != 1.0 {
		t.Errorf("expected score=1.0 (capped), got %.3f", result.Score)
	}
	// Rules should be propagated.
	if len(result.Reasons) == 0 {
		t.Error("expected non-empty Reasons")
	}
}

func TestSATag(t *testing.T) {
	// Score 3.5 / 5.0 = 0.7 -> above tag threshold (0.5), below quarantine (0.9)
	response := "SPAMD/1.5 0 EX_OK\r\n" +
		"Spam: True ; 3.5 / 5.0\r\n" +
		"\r\n" +
		"BAYES_50,HTML_MESSAGE\r\n"

	addr, done := mockSpamd(t, response)

	proc := newProcessor(t, addr)
	env := testEnvelope()
	result, err := proc.Process(context.Background(), env, strings.NewReader(saTestMsg()))
	<-done

	if err != nil {
		t.Fatalf("Process returned unexpected error: %v", err)
	}
	if result.Action != "tag" {
		t.Errorf("expected action=tag, got %q (score=%.3f)", result.Action, result.Score)
	}
	if env.TargetMailbox != "Junk" {
		t.Errorf("expected TargetMailbox=Junk, got %q", env.TargetMailbox)
	}
}

func TestSAUnreachable(t *testing.T) {
	// Point at a port with no listener. Use an ephemeral port that is very
	// unlikely to be in use; bind briefly to get a free port then close it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not bind: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // immediately release so nothing listens there

	proc := newProcessor(t, addr)
	proc.timeout = 2 * time.Second

	result, err := proc.Process(context.Background(), testEnvelope(), strings.NewReader(saTestMsg()))
	if err != nil {
		t.Fatalf("Process should not return an error on unreachable spamd, got: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected action=pass on unreachable spamd, got %q", result.Action)
	}
	found := false
	for _, r := range result.Reasons {
		if r == "spamd_unavailable" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected reason spamd_unavailable in %v", result.Reasons)
	}
}

func TestSAScoreNormalisation(t *testing.T) {
	tests := []struct {
		name        string
		saScore     float32
		saThreshold float32
		wantNorm    float32
	}{
		{"below threshold", 2.3, 5.0, 0.46},
		{"at threshold", 5.0, 5.0, 1.0},
		{"above threshold capped", 7.5, 5.0, 1.0},
		{"zero score", 0, 5.0, 0.0},
		{"negative score", -1.0, 5.0, 0.0},
		{"custom threshold", 3.0, 10.0, 0.3},
		{"zero threshold uses default", 2.5, 0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normaliseSAScore(tt.saScore, tt.saThreshold)
			if !approxEqual(got, tt.wantNorm, 0.001) {
				t.Errorf("normaliseSAScore(%.1f, %.1f) = %.4f, want %.4f",
					tt.saScore, tt.saThreshold, got, tt.wantNorm)
			}
		})
	}
}

func TestSAParseResponse(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantIsSpam bool
		wantScore  float32
		wantThresh float32
		wantRules  []string
		wantErr    bool
	}{
		{
			name: "clean message",
			input: "SPAMD/1.5 0 EX_OK\r\n" +
				"Spam: False ; 1.2 / 5.0\r\n" +
				"\r\n" +
				"DATE_IN_PAST_03_06,NO_RELAYS\r\n",
			wantIsSpam: false,
			wantScore:  1.2,
			wantThresh: 5.0,
			wantRules:  []string{"DATE_IN_PAST_03_06", "NO_RELAYS"},
		},
		{
			name: "spam message",
			input: "SPAMD/1.5 0 EX_OK\r\n" +
				"Spam: True ; 15.3 / 5.0\r\n" +
				"\r\n" +
				"BAYES_99,HTML_MESSAGE,RAZOR2_CHECK\r\n",
			wantIsSpam: true,
			wantScore:  15.3,
			wantThresh: 5.0,
			wantRules:  []string{"BAYES_99", "HTML_MESSAGE", "RAZOR2_CHECK"},
		},
		{
			name: "no rules in body",
			input: "SPAMD/1.5 0 EX_OK\r\n" +
				"Spam: False ; 0.0 / 5.0\r\n" +
				"\r\n",
			wantIsSpam: false,
			wantScore:  0.0,
			wantThresh: 5.0,
			wantRules:  nil,
		},
		{
			name: "non-zero status code",
			input: "SPAMD/1.5 76 EX_PROTOCOL\r\n" +
				"\r\n",
			wantErr: true,
		},
		{
			name:    "truncated response",
			input:   "SPAMD/1.5",
			wantErr: true,
		},
		{
			name: "decimal score with comma-separated rules and spaces",
			input: "SPAMD/1.5 0 EX_OK\r\n" +
				"Spam: True ; 8.7 / 5.0\r\n" +
				"\r\n" +
				" RULE_ONE , RULE_TWO , RULE_THREE \r\n",
			wantIsSpam: true,
			wantScore:  8.7,
			wantThresh: 5.0,
			wantRules:  []string{"RULE_ONE", "RULE_TWO", "RULE_THREE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			resp, err := parseSAResponse(r)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.isSpam != tt.wantIsSpam {
				t.Errorf("isSpam: got %v, want %v", resp.isSpam, tt.wantIsSpam)
			}
			const eps = float32(0.01)
			if !approxEqual(resp.score, tt.wantScore, eps) {
				t.Errorf("score: got %.2f, want %.2f", resp.score, tt.wantScore)
			}
			if !approxEqual(resp.saThreshold, tt.wantThresh, eps) {
				t.Errorf("saThreshold: got %.2f, want %.2f", resp.saThreshold, tt.wantThresh)
			}

			if len(resp.rules) != len(tt.wantRules) {
				t.Errorf("rules len: got %d (%v), want %d (%v)",
					len(resp.rules), resp.rules, len(tt.wantRules), tt.wantRules)
			} else {
				for i := range tt.wantRules {
					if resp.rules[i] != tt.wantRules[i] {
						t.Errorf("rules[%d]: got %q, want %q", i, resp.rules[i], tt.wantRules[i])
					}
				}
			}
		})
	}
}

// TestSAContextCancellation ensures that a context deadline causes Process to
// return promptly (not block indefinitely on a slow spamd).
func TestSAContextCancellation(t *testing.T) {
	// Listener that accepts but never responds.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Hold the connection open without sending a response.
		defer conn.Close() //nolint:errcheck
		time.Sleep(10 * time.Second)
	}()

	proc := newProcessor(t, ln.Addr().String())
	proc.timeout = 200 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	result, err := proc.Process(ctx, testEnvelope(), strings.NewReader(saTestMsg()))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected graceful fallback, got error: %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("expected pass on timeout, got %q", result.Action)
	}
	// Should have returned well within 2 seconds despite the slow mock.
	if elapsed > 2*time.Second {
		t.Errorf("Process took too long: %v", elapsed)
	}

	_ = math.Pi // avoid unused import
}
