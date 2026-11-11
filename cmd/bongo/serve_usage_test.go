package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A runtime failure in serve must print the error, not the flag list after
// it. Production saw the usage text on stderr after a shutdown error.
func TestServeRuntimeErrorPrintsNoUsage(t *testing.T) {
	t.Cleanup(func() {
		serveConfig = ""
		serveCmd.SilenceUsage = false
	})
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"serve", "--config", filepath.Join(t.TempDir(), "missing.hcl")})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("serve with a missing config file succeeded")
	}
	if strings.Contains(out.String(), "Usage:") || strings.Contains(out.String(), "--log-level") {
		t.Fatalf("runtime error printed the usage text:\n%s", out.String())
	}
}
