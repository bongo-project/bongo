package agentgrpc

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// moduleRoot finds the directory holding go.mod, starting from this file.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above " + file)
		}
		dir = parent
	}
}

// TestNoInsecureGRPCCredentialsInProductionCode scans every non-test Go file
// in the module: agent gRPC is mutual TLS only, so no plaintext dial may exist.
func TestNoInsecureGRPCCredentialsInProductionCode(t *testing.T) {
	root := moduleRoot(t)
	forbidden := []string{"insecure.NewCredentials", "grpc.WithInsecure"}
	// Other packages have documented, DANE- or tool-scoped uses of
	// InsecureSkipVerify; the agent gRPC package has none.
	pkgDir := filepath.Join(root, "internal", "agent", "grpc")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for _, f := range forbidden {
			if strings.Contains(string(src), f) {
				t.Errorf("%s references %s", rel, f)
			}
		}
		if filepath.Dir(path) == pkgDir && strings.Contains(string(src), "InsecureSkipVerify") {
			t.Errorf("%s references InsecureSkipVerify", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("scanned only %d files; module root detection is wrong", scanned)
	}
}
