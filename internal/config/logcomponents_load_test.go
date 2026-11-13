package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The example's commented components map decodes once uncommented.
func TestExampleLogComponentsParse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "bongo.example.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	uncommented, found := uncommentBlock(string(raw), "components = {")
	if !found {
		t.Fatal("no commented-out `# components = {` map in deploy/bongo.example.hcl")
	}
	path := filepath.Join(t.TempDir(), "bongo.hcl")
	if err := os.WriteFile(path, []byte(uncommented), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if err := cfg.Telemetry.Log.validateComponents(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := cfg.Telemetry.Log.Components
	if got["imap"] != "debug" || got["smtp"] != "info" || len(got) != 2 {
		t.Errorf("components = %v", got)
	}
}
