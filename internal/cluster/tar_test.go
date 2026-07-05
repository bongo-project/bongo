package cluster

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUntarPathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{"valid relative path", "subdir/file.txt", false},
		{"traversal with dotdot", "../../etc/passwd", true},
		{"traversal with dotdot deeper", "../../../etc/shadow", true},
		{"valid nested", "a/b/c.txt", false},
		{"current dir file", "file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)

			hdr := &tar.Header{
				Name: tt.entry,
				Mode: 0o644,
				Size: 5,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("WriteHeader: %v", err)
			}
			if _, err := tw.Write([]byte("hello")); err != nil {
				t.Fatalf("Write: %v", err)
			}
			tw.Close()

			dir := t.TempDir()
			err := untar(&buf, dir)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error for path traversal, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				// Verify file was extracted.
				target := filepath.Join(dir, filepath.Clean(tt.entry))
				if _, err := os.Stat(target); err != nil {
					t.Errorf("file not found after extract: %v", err)
				}
			}
		})
	}
}

func TestUntarRejectsOversizedFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "huge.bin",
		Mode: 0o644,
		Size: maxSnapshotTarFileBytes + 1,
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	_ = tw.Close()

	err := untar(&buf, t.TempDir())
	if err == nil {
		t.Fatal("expected oversized tar entry to be rejected")
	}
}

func TestTarRoundTrip(t *testing.T) {
	srcDir := t.TempDir()

	// Create some files.
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "sub/b.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("content-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	if err := tarDir(srcDir, &buf); err != nil {
		t.Fatalf("tarDir: %v", err)
	}

	dstDir := t.TempDir()
	if err := untar(&buf, dstDir); err != nil {
		t.Fatalf("untar: %v", err)
	}

	for _, name := range []string{"a.txt", "sub/b.txt"} {
		data, err := os.ReadFile(filepath.Join(dstDir, name))
		if err != nil {
			t.Errorf("reading %s: %v", name, err)
			continue
		}
		want := "content-" + name
		if string(data) != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
}
