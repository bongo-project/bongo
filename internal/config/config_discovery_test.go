package config

import (
	"os"
	"path/filepath"
	"testing"
)

// realpath resolves symlinks so macOS /var → /private/var comparisons don't fail.
func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

// writeHCL writes a minimal valid HCL file to the given path.
func writeHCL(t *testing.T, path string) {
	t.Helper()
	content := `server { role = "dev" }` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeHCL: %v", err)
	}
}

func TestDiscoverFlagPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom.hcl")
	writeHCL(t, cfgPath)

	// Ensure env var and cwd candidates don't interfere.
	t.Setenv(envVarConfig, "")

	got, err := DiscoverConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfgPath {
		t.Errorf("got %q, want %q", got, cfgPath)
	}
}

func TestDiscoverFlagNotFound(t *testing.T) {
	_, err := DiscoverConfigFile("/nonexistent/path/bongo.hcl")
	if err == nil {
		t.Fatal("expected error for missing explicit flag path, got nil")
	}
}

func TestDiscoverEnvVar(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "env.hcl")
	writeHCL(t, cfgPath)

	t.Setenv(envVarConfig, cfgPath)

	got, err := DiscoverConfigFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	abs, _ := filepath.Abs(cfgPath)
	if realpath(t, got) != realpath(t, abs) {
		t.Errorf("got %q, want %q", got, abs)
	}
}

func TestDiscoverEnvVarMissingFileSilentlyFallsThrough(t *testing.T) {
	// BONGO_CONFIG points to a file that doesn't exist — should fall through
	// to CWD/system paths without error (or return "" if none exist).
	t.Setenv(envVarConfig, "/nonexistent/nowhere/bongo.hcl")

	// Run from a temp dir so ./bongo.hcl doesn't exist.
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	got, err := DiscoverConfigFile("")
	if err != nil {
		t.Fatalf("expected silent fall-through, got error: %v", err)
	}
	// /etc/bongo/bongo.hcl likely doesn't exist in test environments either,
	// so we just assert no error and that no flag-missing error occurred.
	_ = got
}

func TestDiscoverCWD(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bongo.hcl")
	writeHCL(t, cfgPath)

	// Clear env var so it doesn't shadow CWD.
	t.Setenv(envVarConfig, "")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	got, err := DiscoverConfigFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	abs, _ := filepath.Abs(cfgPath)
	if realpath(t, got) != realpath(t, abs) {
		t.Errorf("got %q, want %q", got, abs)
	}
}

func TestDiscoverDefaults(t *testing.T) {
	// No flag, no env var, CWD has no bongo.hcl, /etc/bongo/bongo.hcl not present.
	dir := t.TempDir()
	t.Setenv(envVarConfig, "")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	got, err := DiscoverConfigFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// We expect either "" (if /etc/bongo/bongo.hcl doesn't exist) or that path.
	// In CI /etc/bongo/bongo.hcl almost certainly doesn't exist, so assert "".
	if _, statErr := os.Stat("/etc/bongo/bongo.hcl"); os.IsNotExist(statErr) {
		if got != "" {
			t.Errorf("expected empty path when no config exists, got %q", got)
		}
	}
}

func TestDiscoverSystemDefault(t *testing.T) {
	// Only run if /etc/bongo/bongo.hcl actually exists on the test host.
	if _, err := os.Stat("/etc/bongo/bongo.hcl"); os.IsNotExist(err) {
		t.Skip("/etc/bongo/bongo.hcl not present on this host")
	}

	// Make sure flag and env are clear, and CWD has no bongo.hcl.
	dir := t.TempDir()
	t.Setenv(envVarConfig, "")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	got, err := DiscoverConfigFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/etc/bongo/bongo.hcl" {
		t.Errorf("got %q, want /etc/bongo/bongo.hcl", got)
	}
}

func TestDiscoverFlagTakesPriorityOverEnvAndCWD(t *testing.T) {
	dir := t.TempDir()

	flagFile := filepath.Join(dir, "flag.hcl")
	envFile := filepath.Join(dir, "env.hcl")
	cwdFile := filepath.Join(dir, "bongo.hcl")

	writeHCL(t, flagFile)
	writeHCL(t, envFile)
	writeHCL(t, cwdFile)

	t.Setenv(envVarConfig, envFile)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	got, err := DiscoverConfigFile(flagFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != flagFile {
		t.Errorf("got %q, want %q (flag should win)", got, flagFile)
	}
}

func TestDiscoverEnvTakesPriorityOverCWD(t *testing.T) {
	dir := t.TempDir()

	envFile := filepath.Join(dir, "env.hcl")
	cwdFile := filepath.Join(dir, "bongo.hcl")

	writeHCL(t, envFile)
	writeHCL(t, cwdFile)

	t.Setenv(envVarConfig, envFile)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck

	abs, _ := filepath.Abs(envFile)
	got, err := DiscoverConfigFile("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if realpath(t, got) != realpath(t, abs) {
		t.Errorf("got %q, want %q (env should beat CWD)", got, abs)
	}
}
