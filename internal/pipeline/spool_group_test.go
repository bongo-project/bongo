package pipeline

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type fakeEnvelopeSpool struct {
	failAt  int // 1-based Enqueue call that fails; 0 never
	calls   int
	written []string
	removed []string
}

func (f *fakeEnvelopeSpool) Enqueue(env *bongov1.QueueEnvelope) error {
	f.calls++
	if f.calls == f.failAt {
		return errors.New("boom")
	}
	f.written = append(f.written, env.GetId())
	return nil
}

func (f *fakeEnvelopeSpool) Remove(dir, id string) error {
	if dir != "incoming" {
		return errors.New("wrong dir")
	}
	f.removed = append(f.removed, id)
	return nil
}

func TestEnqueueAll(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name        string
		n, failAt   int
		wantErr     bool
		wantRemoved []string
	}{
		{"all succeed", 3, 0, false, nil},
		{"first fails", 3, 1, true, nil},
		{"second fails", 3, 2, true, []string{"e0"}},
		{"last fails", 3, 3, true, []string{"e0", "e1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var envs []*bongov1.QueueEnvelope
			for i := range tt.n {
				envs = append(envs, &bongov1.QueueEnvelope{Id: "e" + string(rune('0'+i))})
			}
			f := &fakeEnvelopeSpool{failAt: tt.failAt}
			err := EnqueueAll(f, envs, logger)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(f.removed) != len(tt.wantRemoved) {
				t.Fatalf("removed = %v, want %v", f.removed, tt.wantRemoved)
			}
			for i := range f.removed {
				if f.removed[i] != tt.wantRemoved[i] {
					t.Fatalf("removed = %v, want %v", f.removed, tt.wantRemoved)
				}
			}
		})
	}
}
