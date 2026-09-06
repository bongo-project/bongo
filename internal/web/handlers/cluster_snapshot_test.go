package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/raft"
)

// fakeSnapshotFuture satisfies raft.SnapshotFuture without a consensus group.
type fakeSnapshotFuture struct {
	err error
}

func (f *fakeSnapshotFuture) Error() error { return f.err }

// Open is never called: the handler only waits on Error(). It exists to
// satisfy raft.SnapshotFuture.
func (f *fakeSnapshotFuture) Open() (*raft.SnapshotMeta, io.ReadCloser, error) {
	return nil, nil, errors.New("not implemented")
}

type fakeSnapshotter struct {
	state  raft.RaftState
	err    error
	called int
}

func (f *fakeSnapshotter) State() raft.RaftState { return f.state }

func (f *fakeSnapshotter) Snapshot() raft.SnapshotFuture {
	f.called++
	return &fakeSnapshotFuture{err: f.err}
}

func TestClusterSnapshot(t *testing.T) {
	tests := []struct {
		name        string
		snapshotter *fakeSnapshotter
		nilRaft     bool
		wantStatus  int
		wantCalled  int
		wantBody    string
	}{
		{
			name:        "leader takes a snapshot",
			snapshotter: &fakeSnapshotter{state: raft.Leader},
			wantStatus:  http.StatusOK,
			wantCalled:  1,
			wantBody:    "ok",
		},
		{
			// Not an error: the operator asked for a current snapshot and
			// there already is one.
			name:        "nothing new to snapshot is a success",
			snapshotter: &fakeSnapshotter{state: raft.Leader, err: raft.ErrNothingNewToSnapshot},
			wantStatus:  http.StatusOK,
			wantCalled:  1,
			wantBody:    "nothing new to snapshot",
		},
		{
			name:        "follower is refused without calling Snapshot",
			snapshotter: &fakeSnapshotter{state: raft.Follower},
			wantStatus:  http.StatusConflict,
			wantCalled:  0,
			wantBody:    "not the leader",
		},
		{
			name:        "candidate is refused without calling Snapshot",
			snapshotter: &fakeSnapshotter{state: raft.Candidate},
			wantStatus:  http.StatusConflict,
			wantCalled:  0,
			wantBody:    "not the leader",
		},
		{
			name:        "snapshot failure is reported",
			snapshotter: &fakeSnapshotter{state: raft.Leader, err: errors.New("disk full")},
			wantStatus:  http.StatusInternalServerError,
			wantCalled:  1,
			wantBody:    "disk full",
		},
		{
			name:       "no raft on this node",
			nilRaft:    true,
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "raft not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h *ClusterSnapshotHandler
			if tt.nilRaft {
				h = NewClusterSnapshotHandler(nil, nil)
			} else {
				h = NewClusterSnapshotHandler(tt.snapshotter, nil)
			}

			req := httptest.NewRequest(http.MethodPost, "/internal/cluster/snapshot", nil)
			w := httptest.NewRecorder()
			h.Snapshot(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if !tt.nilRaft && tt.snapshotter.called != tt.wantCalled {
				t.Errorf("Snapshot() called %d times, want %d", tt.snapshotter.called, tt.wantCalled)
			}

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", w.Body.String(), err)
			}
			joined, _ := json.Marshal(body)
			if !strings.Contains(string(joined), tt.wantBody) {
				t.Errorf("body = %s, want it to mention %q", joined, tt.wantBody)
			}
		})
	}
}

// NewClusterSnapshotHandler must tolerate a nil logger: a nil one here would
// panic on the first failed snapshot, which is exactly when it is needed.
func TestClusterSnapshotNilLoggerDoesNotPanic(t *testing.T) {
	h := NewClusterSnapshotHandler(&fakeSnapshotter{state: raft.Leader, err: errors.New("boom")}, nil)

	req := httptest.NewRequest(http.MethodPost, "/internal/cluster/snapshot", nil)
	w := httptest.NewRecorder()
	h.Snapshot(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
