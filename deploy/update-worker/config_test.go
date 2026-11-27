// Package updateworker_test checks the privacy and configuration contract of
// the update Worker's source without Node or Cloudflare.
package updateworker_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func read(t *testing.T, rel string) string {
	t.Helper()
	// #nosec G304 -- fixed relative paths of this package's own fixtures.
	b, err := os.ReadFile(filepath.Clean(rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestWranglerConfig(t *testing.T) {
	toml := read(t, "wrangler.toml")
	tests := []struct {
		name string
		re   string
	}{
		{"observability disabled", `(?m)^\[observability\]\s*\n\s*enabled\s*=\s*false\s*$`},
		{"logpush false", `(?m)^logpush\s*=\s*false\s*$`},
		{"cron present", `(?m)^crons\s*=\s*\["[^"]+"\]\s*$`},
		{"workers.dev off", `(?m)^workers_dev\s*=\s*false\s*$`},
		{"preview urls off", `(?m)^preview_urls\s*=\s*false\s*$`},
		{"custom domain", `(?m)^pattern\s*=\s*"update\.bongo-project\.org"\s*$`},
		{"database id is a placeholder", `(?m)^database_id\s*=\s*"REPLACE-WITH-D1-DATABASE-ID"\s*$`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !regexp.MustCompile(tc.re).MatchString(toml) {
				t.Errorf("wrangler.toml does not match %s", tc.re)
			}
		})
	}
	if strings.Contains(toml, "account_id") {
		t.Error("wrangler.toml must not carry an account_id")
	}
}

func TestWranglerVars(t *testing.T) {
	toml := read(t, "wrangler.toml")
	_, after, ok := strings.Cut(toml, "[vars]")
	if !ok {
		t.Fatal("no [vars] table")
	}
	var keys []string
	for _, line := range strings.Split(after, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		if k, _, found := strings.Cut(line, "="); found && !strings.HasPrefix(line, "#") {
			keys = append(keys, strings.TrimSpace(k))
		}
	}
	sort.Strings(keys)
	want := "LATEST_VERSION,RELEASE_NOTES_URL,SECURITY"
	if got := strings.Join(keys, ","); got != want {
		t.Errorf("[vars] keys = %s, want %s", got, want)
	}
}

func TestSourceReadsNoClientIdentity(t *testing.T) {
	src := strings.ToLower(read(t, "src/index.js"))
	for _, forbidden := range []string{
		"console.", "cf-connecting-ip", "x-forwarded-for", "x-real-ip",
		"true-client-ip", "request.cf", "user-agent", "headers.entries", "headers.keys",
	} {
		t.Run(forbidden, func(t *testing.T) {
			if strings.Contains(src, forbidden) {
				t.Errorf("src/index.js contains %q", forbidden)
			}
		})
	}
}

func TestMigrationHasNoIdentifyingColumns(t *testing.T) {
	sql := strings.ToLower(read(t, "migrations/0001_init.sql"))
	for _, forbidden := range []string{"ip", "addr", "agent", "timestamp", "created_at"} {
		t.Run(forbidden, func(t *testing.T) {
			re := regexp.MustCompile(`(?m)^\s*[a-z_]*` + forbidden + `[a-z_]*\s+(text|integer|blob|real)`)
			if re.MatchString(sql) {
				t.Errorf("migration has a column like %q", forbidden)
			}
		})
	}
}
