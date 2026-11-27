package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sources returns the text of every non-test Go file of the package.
func sources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		// #nosec G304 -- file names come from a glob of this package.
		b, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		out[f] = string(b)
	}
	if len(out) == 0 {
		t.Fatal("no sources found")
	}
	return out
}

// TestSourceReadsNothingAboutTheClient pins the privacy contract: the
// service never reads the client's address or any header, and uses no
// request-logging helper.
func TestSourceReadsNothingAboutTheClient(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`RemoteAddr`),
		regexp.MustCompile(`(?i)X-Forwarded`),
		regexp.MustCompile(`(?i)X-Real-IP`),
		regexp.MustCompile(`(?i)\bForwarded\b`),
		regexp.MustCompile(`(?i)True-Client-IP`),
		regexp.MustCompile(`(?i)User-?Agent`),
		regexp.MustCompile(`(?i)Referer`),
		regexp.MustCompile(`\b(r|req|request)\.(Header|Host|RequestURI|TLS|Cookie|Cookies|Form|PostForm|Trailer)\b`),
		regexp.MustCompile(`\.Header\.(Get|Values|Clone)\(`),
		regexp.MustCompile(`httputil`),
		regexp.MustCompile(`middleware\.`),
		regexp.MustCompile(`go-chi`),
		regexp.MustCompile(`\bchi\.`),
		regexp.MustCompile(`net/http/pprof`),
		regexp.MustCompile(`expvar`),
	}
	for name, src := range sources(t) {
		for _, re := range banned {
			if loc := re.FindStringIndex(src); loc != nil {
				t.Errorf("%s matches %s at byte %d", name, re, loc[0])
			}
		}
	}
}

func TestServerErrorLogDiscards(t *testing.T) {
	srv := newServer(":0", nil)
	if srv.ErrorLog == nil {
		t.Fatal("ErrorLog is nil: net/http would write client addresses to stderr")
	}
	if srv.ErrorLog.Writer() != io.Discard {
		t.Error("ErrorLog does not write to io.Discard")
	}
}

func TestServerTimeouts(t *testing.T) {
	srv := newServer(":0", nil)
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 ||
		srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("a limit is unset: %+v", srv)
	}
}

// TestSchemaHasNoIdentifyingColumns is the port of the Worker's migration
// check: nothing about the client is stored besides the random ID.
func TestSchemaHasNoIdentifyingColumns(t *testing.T) {
	src := sources(t)["schema.go"]
	bad := regexp.MustCompile(`(?i)\b(ip\w*|addr\w*|agent\w*|user_agent|timestamp\w*|created_at|updated_at|host\w*)\b`)
	// Strip comments, which describe the tables in prose.
	var code []string
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		code = append(code, line)
	}
	if m := bad.FindString(strings.Join(code, "\n")); m != "" {
		t.Errorf("schema mentions %q", m)
	}
	for _, want := range []string{"seen", "daily_counts", "instance_id", "instances"} {
		if !strings.Contains(src, want) {
			t.Errorf("schema lacks %q", want)
		}
	}
}

// TestNoHostingNames keeps the deployment out of the repository.
func TestNoHostingNames(t *testing.T) {
	re := regexp.MustCompile(`(?i)cloudflare|nomad|hetzner|traefik|wrangler`)
	files, _ := filepath.Glob("*")
	for _, f := range files {
		if f == "privacy_test.go" {
			continue
		}
		// #nosec G304 -- file names come from a glob of this package.
		b, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			continue
		}
		if m := re.Find(b); m != nil {
			t.Errorf("%s names %q", f, m)
		}
	}
}
