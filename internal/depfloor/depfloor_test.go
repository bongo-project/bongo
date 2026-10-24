// Package depfloor holds tests that pin minimum dependency and toolchain
// versions, so a revert of a security upgrade fails the build rather than
// waiting for the next govulncheck run.
package depfloor

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// parseVersion turns "v1.8.0" or "1.27.3" into its numeric parts. Missing
// parts are zero; anything non-numeric fails the test.
func parseVersion(t *testing.T, s string) [3]int {
	t.Helper()
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) > 3 {
		t.Fatalf("version %q has too many parts", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("version %q: %v", s, err)
		}
		out[i] = n
	}
	return out
}

func atLeast(got, want [3]int) bool {
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return true
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestMinimumVersions fails if a security-relevant dependency or the Go
// directive drops below the version that carries the fix.
func TestMinimumVersions(t *testing.T) {
	gomod := readRepoFile(t, "go.mod")

	tests := []struct {
		name    string
		pattern string
		min     string
		why     string
	}{
		{"go directive", `(?m)^go (\S+)$`, "1.27.3", "latest stable Go"},
		{"go-chi/chi", `(?m)^\s*github\.com/go-chi/chi/v5 (\S+)`, "5.3.0", "GO-2026-5774, GO-2026-5775, GO-2026-5777"},
		{"hashicorp/raft", `(?m)^\s*github\.com/hashicorp/raft (\S+)`, "1.8.0", "raft, memberlist and go-metrics move together"},
		{"hashicorp/memberlist", `(?m)^\s*github\.com/hashicorp/memberlist (\S+)`, "0.7.0", "raft, memberlist and go-metrics move together"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := regexp.MustCompile(tc.pattern).FindStringSubmatch(gomod)
			if m == nil {
				t.Fatalf("%s not found in go.mod", tc.name)
			}
			if !atLeast(parseVersion(t, m[1]), parseVersion(t, tc.min)) {
				t.Errorf("%s is %s, want at least %s (%s)", tc.name, m[1], tc.min, tc.why)
			}
		})
	}
}

// TestDockerBuilderMatchesGoDirective keeps the Dockerfile builder image on
// the same Go release as go.mod, with a digest pin.
func TestDockerBuilderMatchesGoDirective(t *testing.T) {
	gomod := readRepoFile(t, "go.mod")
	goVer := regexp.MustCompile(`(?m)^go (\S+)$`).FindStringSubmatch(gomod)
	if goVer == nil {
		t.Fatal("go directive not found in go.mod")
	}
	dockerfile := readRepoFile(t, "deploy/Dockerfile")
	m := regexp.MustCompile(`(?m)^FROM golang:(\S+?)-bookworm@sha256:[0-9a-f]{64} AS build$`).FindStringSubmatch(dockerfile)
	if m == nil {
		t.Fatal("digest-pinned golang builder stage not found in deploy/Dockerfile")
	}
	if m[1] != goVer[1] {
		t.Errorf("Dockerfile builder is golang:%s, go.mod says %s", m[1], goVer[1])
	}
}

// TestComposeImageTag keeps the deploy template on the release it ships with.
func TestComposeImageTag(t *testing.T) {
	compose := readRepoFile(t, "deploy/docker-compose.yml")
	if !strings.Contains(compose, "image: ghcr.io/bongo-project/bongo:0.12.0\n") {
		t.Error("deploy/docker-compose.yml does not use ghcr.io/bongo-project/bongo:0.12.0")
	}
}
