package antivirus

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const failureLogMsg = "antivirus: scan failed, applying on_failure policy"

// policyAction is the (policy, action) pair carried by one failure log record.
type policyAction struct{ policy, action string }

// recordingHandler is a concurrency-safe slog.Handler that keeps the
// policy/action attributes of every on_failure record.
type recordingHandler struct {
	mu   *sync.Mutex
	recs *[]policyAction
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{mu: &sync.Mutex{}, recs: &[]policyAction{}}
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler            { return h }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message != failureLogMsg {
		return nil
	}
	var pa policyAction
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "policy":
			pa.policy = a.Value.String()
		case "action":
			pa.action = a.Value.String()
		}
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, pa)
	h.mu.Unlock()
	return nil
}

func (h *recordingHandler) snapshot() []policyAction {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]policyAction(nil), *h.recs...)
}

func failureAction(p *ClamAVProcessor) string {
	env := &bongov1.QueueEnvelope{Sender: "a@example.com"}
	return p.failureResult(time.Now(), env, "scanner unavailable: test").GetAction()
}

func TestClamAVSetOnFailure(t *testing.T) {
	newP := func(start OnFailure) *ClamAVProcessor {
		return NewClamAVProcessor(WithOnFailure(start), WithLogger(slog.New(newRecordingHandler())))
	}
	actionOf := map[OnFailure]string{OnFailureAccept: "pass", OnFailureQuarantine: "quarantine"}
	policies := []OnFailure{OnFailureAccept, OnFailureQuarantine}

	t.Run("valid", func(t *testing.T) {
		for _, start := range policies {
			for _, want := range policies {
				p := newP(start)
				if err := p.SetOnFailure(want); err != nil {
					t.Fatalf("SetOnFailure(%q) from %q: %v", want, start, err)
				}
				if got := failureAction(p); got != actionOf[want] {
					t.Errorf("start %q set %q: action = %q, want %q", start, want, got, actionOf[want])
				}
			}
		}
	})

	t.Run("invalid leaves policy unchanged", func(t *testing.T) {
		for _, start := range policies {
			for _, bad := range []OnFailure{"", "Accept", "reject", "bogus"} {
				p := newP(start)
				if err := p.SetOnFailure(bad); err == nil {
					t.Fatalf("SetOnFailure(%q) = nil, want error", bad)
				}
				if got := failureAction(p); got != actionOf[start] {
					t.Errorf("start %q after rejected %q: action = %q, want %q", start, bad, got, actionOf[start])
				}
			}
		}
	})

	t.Run("default is quarantine", func(t *testing.T) {
		p := NewClamAVProcessor(WithLogger(slog.New(newRecordingHandler())))
		if got := failureAction(p); got != "quarantine" {
			t.Errorf("default action = %q, want quarantine", got)
		}
	})
}

func TestClamAVOnFailureConcurrentReload(t *testing.T) {
	h := newRecordingHandler()
	p := NewClamAVProcessor(WithLogger(slog.New(h)))

	done := make(chan struct{})
	var setter sync.WaitGroup
	setter.Add(1)
	go func() {
		defer setter.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			pol := OnFailureQuarantine
			if i%2 == 0 {
				pol = OnFailureAccept
			}
			if err := p.SetOnFailure(pol); err != nil {
				t.Errorf("SetOnFailure: %v", err)
				return
			}
		}
	}()

	const workers, perWorker = 4, 5000
	var (
		mu      sync.Mutex
		results = map[string]int{}
		readers sync.WaitGroup
	)
	for range workers {
		readers.Add(1)
		go func() {
			defer readers.Done()
			local := map[string]int{}
			for range perWorker {
				local[failureAction(p)]++
			}
			mu.Lock()
			for k, v := range local {
				results[k] += v
			}
			mu.Unlock()
		}()
	}
	readers.Wait()
	close(done)
	setter.Wait()

	for action, n := range results {
		if action != "pass" && action != "quarantine" {
			t.Errorf("invalid action %q seen %d times", action, n)
		}
	}
	if results["pass"] == 0 || results["quarantine"] == 0 {
		t.Errorf("policy never flipped under load: %v", results)
	}

	recs := h.snapshot()
	if len(recs) != workers*perWorker {
		t.Fatalf("recorded %d log records, want %d", len(recs), workers*perWorker)
	}
	for _, r := range recs {
		ok := (r.policy == "accept" && r.action == "pass") || (r.policy == "quarantine" && r.action == "quarantine")
		if !ok {
			t.Fatalf("inconsistent decision: policy=%q action=%q", r.policy, r.action)
		}
	}
}
