package pipeline

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// TestListSkipsEnvelopeThatVanished pins that an envelope moved or removed
// between List's directory read and its file read is skipped, while every
// other read failure still fails the listing. The blob GC builds its reference
// set from List, so an error here aborts the whole pass, and under load an
// envelope moves on most passes.
func TestListSkipsEnvelopeThatVanished(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// break is applied to the "list-bbb" envelope after the directory read.
		breakEnv func(t *testing.T, s *SpoolStore, path string)
		wantErr  bool
		wantIDs  []string
	}{
		{
			name: "renamed to processing",
			breakEnv: func(t *testing.T, s *SpoolStore, _ string) {
				if err := s.Move("list-bbb", "incoming", "processing"); err != nil {
					t.Fatalf("Move: %v", err)
				}
			},
			wantIDs: []string{"list-aaa", "list-ccc"},
		},
		{
			name: "removed on completion",
			breakEnv: func(t *testing.T, s *SpoolStore, _ string) {
				if err := s.Remove("incoming", "list-bbb"); err != nil {
					t.Fatalf("Remove: %v", err)
				}
			},
			wantIDs: []string{"list-aaa", "list-ccc"},
		},
		{
			name: "corrupt envelope still fails",
			breakEnv: func(t *testing.T, _ *SpoolStore, path string) {
				if err := os.WriteFile(path, []byte{0xff, 0xff, 0xff}, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
		{
			name: "permission denied still fails",
			breakEnv: func(t *testing.T, _ *SpoolStore, path string) {
				if runtime.GOOS == "windows" || os.Geteuid() == 0 {
					t.Skip("file modes do not deny reads here")
				}
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := NewSpoolStore(t.TempDir(), discardLogger())
			if err != nil {
				t.Fatalf("NewSpoolStore: %v", err)
			}
			for _, id := range []string{"list-aaa", "list-bbb", "list-ccc"} {
				if err := s.WriteEnvelope("incoming", makeEnvelope(id, "x@x.com")); err != nil {
					t.Fatalf("WriteEnvelope %s: %v", id, err)
				}
			}
			s.beforeListRead = func(dir, id string) {
				if id == "list-bbb" {
					tc.breakEnv(t, s, filepath.Join(s.baseDir, dir, id+envSuffix))
				}
			}

			envs, err := s.List("incoming")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("List succeeded with %d envelopes, want an error", len(envs))
				}
				if errors.Is(err, fs.ErrNotExist) {
					t.Errorf("err = %v, want a failure other than not-exist", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var got []string
			for _, env := range envs {
				got = append(got, env.Id)
			}
			if !slices.Equal(got, tc.wantIDs) {
				t.Errorf("List ids = %v, want %v", got, tc.wantIDs)
			}
		})
	}
}
