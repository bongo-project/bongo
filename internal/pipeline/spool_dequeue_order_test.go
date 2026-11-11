package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Envelope IDs are random, so the queue must not be served in name order.
// Under a backlog that let a message whose ID sorted late wait until the whole
// queue had drained. Dequeue serves the earliest-queued envelope first.
func TestDequeueServesOldestQueuedFirstWhateverTheID(t *testing.T) {
	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	// Queued first, but its ID sorts last.
	ids := []string{"ffff-queued-first", "8888-queued-second", "0000-queued-third"}
	start := time.Now().Add(-time.Hour)
	for i, id := range ids {
		if err := s.Enqueue(makeEnvelope(id, "sender@example.com")); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
		at := start.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(base, "incoming", id+envSuffix), at, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range ids {
		got, err := s.Dequeue()
		if err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		if got == nil || got.Id != want {
			t.Fatalf("Dequeue = %v, want %s (earliest queued)", got, want)
		}
	}
	if got, err := s.Dequeue(); err != nil || got != nil {
		t.Fatalf("Dequeue on empty queue = %v, %v", got, err)
	}
}

// Envelopes queued in the same instant are served in name order, so the
// choice is deterministic.
func TestDequeueBreaksTimeTiesByName(t *testing.T) {
	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	at := time.Now().Add(-time.Minute)
	for _, id := range []string{"bbb", "aaa"} {
		if err := s.Enqueue(makeEnvelope(id, "sender@example.com")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(base, "incoming", id+envSuffix), at, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Dequeue()
	if err != nil || got == nil || got.Id != "aaa" {
		t.Fatalf("Dequeue = %v, %v; want aaa", got, err)
	}
}
