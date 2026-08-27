package pipeline

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func makeEnvelope(id, sender string) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id:         id,
		Sender:     sender,
		Recipients: []string{"user@example.com"},
		ReceivedAt: 1700000000,
		BlobHash:   "abc123",
	}
}

func TestNewSpoolStore(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil SpoolStore")
	}

	for _, dir := range []string{"incoming", "processing", "deliver", "quarantine", "error"} {
		info, err := os.Stat(filepath.Join(base, dir))
		if err != nil {
			t.Errorf("directory %s missing: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestEnqueueDequeue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  *bongov1.QueueEnvelope
	}{
		{
			name: "simple message",
			env:  makeEnvelope("msg-001", "sender@example.com"),
		},
		{
			name: "message with flags",
			env: &bongov1.QueueEnvelope{
				Id:           "msg-002",
				Sender:       "other@example.com",
				Recipients:   []string{"a@b.com", "c@d.com"},
				ReceivedAt:   1700000001,
				BlobHash:     "deadbeef",
				Flags:        MsgFlagSpamChecked | MsgFlagVirusChecked,
				CurrentStage: "antivirus",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			s, err := NewSpoolStore(base, discardLogger())
			if err != nil {
				t.Fatalf("NewSpoolStore: %v", err)
			}

			if err := s.Enqueue(tc.env); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}

			// Verify file exists in incoming.
			_, err = os.Stat(filepath.Join(base, "incoming", tc.env.Id+envSuffix))
			if err != nil {
				t.Fatalf("expected file in incoming: %v", err)
			}

			got, err := s.Dequeue()
			if err != nil {
				t.Fatalf("Dequeue: %v", err)
			}
			if got == nil {
				t.Fatal("expected non-nil envelope from Dequeue")
			}

			if got.Id != tc.env.Id {
				t.Errorf("id: got %q, want %q", got.Id, tc.env.Id)
			}
			if got.Sender != tc.env.Sender {
				t.Errorf("sender: got %q, want %q", got.Sender, tc.env.Sender)
			}
			if got.Flags != tc.env.Flags {
				t.Errorf("flags: got %d, want %d", got.Flags, tc.env.Flags)
			}

			// After dequeue, file should be in processing, not incoming.
			_, err = os.Stat(filepath.Join(base, "incoming", tc.env.Id+envSuffix))
			if !os.IsNotExist(err) {
				t.Error("file should not remain in incoming after dequeue")
			}
			_, err = os.Stat(filepath.Join(base, "processing", tc.env.Id+envSuffix))
			if err != nil {
				t.Errorf("file should be in processing: %v", err)
			}
		})
	}
}

func TestDequeueEmpty(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	env, err := s.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue on empty queue: %v", err)
	}
	if env != nil {
		t.Errorf("expected nil envelope from empty queue, got %+v", env)
	}
}

func TestDequeueOrder(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	// Enqueue three messages; expect them dequeued in lexicographic (sorted) order.
	ids := []string{"aaa-msg", "bbb-msg", "ccc-msg"}
	for _, id := range ids {
		if err := s.Enqueue(makeEnvelope(id, "x@x.com")); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}

	var got []string
	for i := 0; i < len(ids); i++ {
		env, err := s.Dequeue()
		if err != nil {
			t.Fatalf("Dequeue iteration %d: %v", i, err)
		}
		if env == nil {
			t.Fatalf("got nil on iteration %d", i)
		}
		got = append(got, env.Id)
	}

	want := make([]string, len(ids))
	copy(want, ids)
	sort.Strings(want)

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
	}{
		{"processing to deliver", "processing", "deliver"},
		{"processing to quarantine", "processing", "quarantine"},
		{"processing to error", "processing", "error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			s, err := NewSpoolStore(base, discardLogger())
			if err != nil {
				t.Fatalf("NewSpoolStore: %v", err)
			}

			env := makeEnvelope("move-test", "a@b.com")
			// Write directly to from directory.
			if err := s.WriteEnvelope(tc.from, env); err != nil {
				t.Fatalf("WriteEnvelope: %v", err)
			}

			if err := s.Move(env.Id, tc.from, tc.to); err != nil {
				t.Fatalf("Move: %v", err)
			}

			// Should exist in to directory.
			_, err = os.Stat(filepath.Join(base, tc.to, env.Id+envSuffix))
			if err != nil {
				t.Errorf("file not found in %s: %v", tc.to, err)
			}

			// Should not exist in from directory.
			_, err = os.Stat(filepath.Join(base, tc.from, env.Id+envSuffix))
			if !os.IsNotExist(err) {
				t.Errorf("file still in %s after move", tc.from)
			}
		})
	}
}

func TestList(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	// Write several envelopes to deliver.
	want := map[string]bool{
		"list-aaa": true,
		"list-bbb": true,
		"list-ccc": true,
	}
	for id := range want {
		if err := s.WriteEnvelope("deliver", makeEnvelope(id, "x@x.com")); err != nil {
			t.Fatalf("WriteEnvelope %s: %v", id, err)
		}
	}

	envs, err := s.List("deliver")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(envs) != len(want) {
		t.Fatalf("List returned %d envelopes, want %d", len(envs), len(want))
	}
	for _, env := range envs {
		if !want[env.Id] {
			t.Errorf("unexpected id in list: %q", env.Id)
		}
	}
}

func TestListEmpty(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	envs, err := s.List("quarantine")
	if err != nil {
		t.Fatalf("List empty dir: %v", err)
	}
	if len(envs) != 0 {
		t.Errorf("expected 0 envelopes, got %d", len(envs))
	}
}

func TestRecoverCrashed(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	// Create directories manually to pre-populate processing without triggering recovery.
	for _, d := range []string{"incoming", "processing", "deliver", "quarantine", "error"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	// Directly write envelopes into processing/ to simulate a crash.
	crashed := []*bongov1.QueueEnvelope{
		makeEnvelope("crash-001", "a@a.com"),
		makeEnvelope("crash-002", "b@b.com"),
	}
	// Use a temporary SpoolStore that won't call RecoverCrashed yet.
	tmp := &SpoolStore{baseDir: base, logger: discardLogger()}
	for _, env := range crashed {
		if err := tmp.WriteEnvelope("processing", env); err != nil {
			t.Fatalf("write to processing: %v", err)
		}
	}

	// Now create a proper SpoolStore which calls RecoverCrashed.
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	_ = s

	// All files should be in incoming now.
	for _, env := range crashed {
		_, err = os.Stat(filepath.Join(base, "incoming", env.Id+envSuffix))
		if err != nil {
			t.Errorf("recovered file not in incoming for %s: %v", env.Id, err)
		}
		_, err = os.Stat(filepath.Join(base, "processing", env.Id+envSuffix))
		if !os.IsNotExist(err) {
			t.Errorf("file still in processing after recovery for %s", env.Id)
		}
	}
}

func TestRecoverCrashedCount(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	for _, d := range []string{"incoming", "processing", "deliver", "quarantine", "error"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	tmp := &SpoolStore{baseDir: base, logger: discardLogger()}
	for i := 0; i < 5; i++ {
		id := "recover-" + string(rune('a'+i))
		if err := tmp.WriteEnvelope("processing", makeEnvelope(id, "x@x.com")); err != nil {
			t.Fatalf("WriteEnvelope: %v", err)
		}
	}

	n, err := tmp.RecoverCrashed()
	if err != nil {
		t.Fatalf("RecoverCrashed: %v", err)
	}
	if n != 5 {
		t.Errorf("RecoverCrashed returned %d, want 5", n)
	}
}

func TestAtomicWrite(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	env := makeEnvelope("atomic-test", "a@b.com")
	if err := s.WriteEnvelope("incoming", env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	// Temp file must not exist after a successful write.
	tmpPath := filepath.Join(base, "incoming", ".tmp-"+env.Id+envSuffix)
	_, err = os.Stat(tmpPath)
	if !os.IsNotExist(err) {
		t.Errorf("temp file should not exist after successful WriteEnvelope: %v", err)
	}

	// Final file must exist.
	finalPath := filepath.Join(base, "incoming", env.Id+envSuffix)
	if _, err := os.Stat(finalPath); err != nil {
		t.Errorf("final file missing: %v", err)
	}
}

func TestWriteEnvelope(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	env := makeEnvelope("update-test", "a@b.com")
	if err := s.WriteEnvelope("processing", env); err != nil {
		t.Fatalf("initial WriteEnvelope: %v", err)
	}

	// Update the envelope in-place.
	env.Flags = SetFlag(env.Flags, MsgFlagSpamChecked)
	env.CurrentStage = "antivirus"
	if err := s.WriteEnvelope("processing", env); err != nil {
		t.Fatalf("update WriteEnvelope: %v", err)
	}

	// Read back and verify.
	got, err := s.ReadEnvelope("processing", env.Id)
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if got.Flags != env.Flags {
		t.Errorf("flags: got %d, want %d", got.Flags, env.Flags)
	}
	if got.CurrentStage != env.CurrentStage {
		t.Errorf("current_stage: got %q, want %q", got.CurrentStage, env.CurrentStage)
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	env := makeEnvelope("remove-test", "a@b.com")
	if err := s.WriteEnvelope("deliver", env); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	if err := s.Remove("deliver", env.Id); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err = os.Stat(filepath.Join(base, "deliver", env.Id+envSuffix))
	if !os.IsNotExist(err) {
		t.Error("file should not exist after Remove")
	}
}

func TestRemoveNonExistent(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	s, err := NewSpoolStore(base, discardLogger())
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	err = s.Remove("deliver", "no-such-id")
	if err == nil {
		t.Error("expected error removing non-existent file")
	}
}
