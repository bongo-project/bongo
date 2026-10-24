package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		wantCode int
		// wantOut must appear in the combined output of a failing run.
		wantOut string
	}{
		{"pinned", "pinned.yml", 0, ""},
		{"unpinned", "unpinned.yml", 1, "postgres:16"},
		{"quoted key", "quoted_key.yml", 1, "nginx:1"},
		{"flow style", "flow.yml", 1, "nginx:1"},
		{"bongo exemption any tag", "exempt.yml", 0, ""},
		{"bongo lookalike", "exempt_lookalike.yml", 1, "bongo-evil:1"},
		{"interpolation", "interp.yml", 1, "${IMG}"},
		{"interpolated tag", "interp.yml", 1, "nginx:${TAG}"},
		{"no images", "noimages.yml", 1, "no image keys"},
		{"empty file", "empty.yml", 1, "no image keys"},
		{"malformed yaml", "malformed.yml", 1, "parse yaml"},
		{"short digest", "bad_digest.yml", 1, "nginx@sha256:bbb"},
		{"uppercase hex digest", "bad_digest2.yml", 1, "EEEE"},
		{"trailing text after digest", "bad_digest3.yml", 1, "-extra"},
		{"second document", "multidoc.yml", 1, "redis:7"},
		{"null value", "nonscalar.yml", 1, "line 5"},
		{"number value", "nonscalar.yml", 1, "line 7"},
		{"sequence value", "nonscalar.yml", 1, "line 9"},
		{"mapping value", "nonscalar.yml", 1, "line 11"},
		{"empty string value", "nonscalar.yml", 1, "line 13"},
		{"nested outside services", "nested.yml", 1, "nginx:1"},
		{"alias key", "alias_key.yml", 1, "nginx:1"},
		{"image in sequence merged", "sequence.yml", 1, "nginx:1"},
		{"custom tagged digest", "custom_tag.yml", 1, "line 3"},
		{"image inside a complex key", "complex_key.yml", 1, "nginx:1"},
		{"alias value", "alias.yml", 1, "nginx:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run([]string{filepath.Join("testdata", tt.fixture)}, &out, &errOut)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tt.wantCode, out.String(), errOut.String())
			}
			if combined := out.String() + errOut.String(); !strings.Contains(combined, tt.wantOut) {
				t.Errorf("output %q does not contain %q", combined, tt.wantOut)
			}
		})
	}
}

func TestRunMissingFile(t *testing.T) {
	var out, errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "nope.yml")
	if code := run([]string{missing}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), missing) {
		t.Errorf("stderr %q does not name the path", errOut.String())
	}
}

func TestRunTooManyArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"a", "b"}, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// The real compose file is guarded by make test, not only by the lint job.
func TestDeployComposePinned(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{filepath.Join("..", "..", "deploy", "docker-compose.yml")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
}
