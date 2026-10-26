package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsAssetFailure(t *testing.T) {
	const origin = "http://127.0.0.1:9000"
	tests := []struct {
		name   string
		url    string
		status int
		want   bool
	}{
		{"same-origin 404", origin + "/static/missing.png", 404, true},
		{"same-origin 500", origin + "/static/x.js", 500, true},
		{"same-origin 401 is reported", origin + "/admin", 401, true},
		{"same-origin exactly 400", origin + "/x", 400, true},
		{"same-origin 399", origin + "/x", 399, false},
		{"same host over https", "https://127.0.0.1:9000/x.png", 404, false},
		{"same-origin 200", origin + "/static/ok.css", 200, false},
		{"same-origin 304", origin + "/static/ok.css", 304, false},
		{"cross-origin 404", "http://example.com/x.png", 404, false},
		{"other port 404", "http://127.0.0.1:9001/x.png", 404, false},
		{"origin prefix lookalike", origin + ".evil.example/x.png", 404, false},
		{"data url", "data:image/png;base64,AAAA", 404, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAssetFailure(origin, tt.url, tt.status); got != tt.want {
				t.Errorf("isAssetFailure(%q, %d) = %v, want %v", tt.url, tt.status, got, tt.want)
			}
		})
	}
}

func TestIsLoadFailure(t *testing.T) {
	const origin = "http://127.0.0.1:9000"
	tests := []struct {
		name     string
		url      string
		canceled bool
		blocked  string
		want     bool
	}{
		{"failed same-origin", origin + "/static/a.js", false, "", true},
		{"canceled is ignored", origin + "/static/a.js", true, "", false},
		{"csp blocked is kept", origin + "/static/a.js", false, "csp", true},
		{"canceled but blocked is kept", origin + "/static/a.js", true, "csp", true},
		{"cross-origin failure ignored", "http://example.com/a.js", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLoadFailure(origin, tt.url, tt.canceled, tt.blocked); got != tt.want {
				t.Errorf("isLoadFailure = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	clean := Result{Route: "inbox", Variant: "desktop-light"}
	tests := []struct {
		name string
		in   []Result
		want int
	}{
		{"empty", nil, 0},
		{"clean", []Result{clean, clean}, 0},
		{"console", []Result{clean, {Findings: []Finding{{Kind: KindConsole}}}}, 1},
		{"csp", []Result{{Findings: []Finding{{Kind: KindCSP}}}}, 1},
		{"asset", []Result{{Findings: []Finding{{Kind: KindAsset}}}}, 1},
		{"capture error", []Result{{Err: errors.New("timeout")}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.in); got != tt.want {
				t.Errorf("exitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWriteSummary(t *testing.T) {
	rs := []Result{
		{Route: "inbox", Variant: "desktop-light", Screenshot: "/o/inbox-desktop-light.png"},
		{Route: "bad", Variant: "mobile-light", Findings: []Finding{
			{Kind: KindCSP, Detail: "script-src inline"},
			{Kind: KindAsset, Detail: "404 /static/x.png"},
			{Kind: KindAsset, Detail: "404 /static/y.png"},
		}},
		{Route: "gone", Variant: "desktop-dark", Err: errors.New("boom")},
	}
	var buf bytes.Buffer
	writeSummary(&buf, rs)
	out := buf.String()
	if !strings.Contains(out, "(withheld)") {
		t.Errorf("failed row should show (withheld):\n%s", out)
	}
	for _, want := range []string{"inbox", "script-src inline", "404 /static/y.png", "boom", "ROUTE"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestWriteSummaryCollapsesRepeats(t *testing.T) {
	f := Finding{Kind: KindConsole, Detail: "exception: boom\n    at stack frame one\n    at frame two"}
	var buf bytes.Buffer
	writeSummary(&buf, []Result{{Route: "r", Variant: "v", Findings: []Finding{f, f, f}}})
	out := buf.String()
	if strings.Contains(out, "stack frame") {
		t.Errorf("stack trace leaked into summary:\n%s", out)
	}
	if !strings.Contains(out, "(x3)") {
		t.Errorf("repeats not collapsed:\n%s", out)
	}
	if !strings.Contains(out, "\t3\t") && !strings.Contains(out, "  3  ") {
		t.Errorf("table should still count all three:\n%s", out)
	}
}

func TestCheckOutDir(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(outside, "link")
	if err := os.Symlink(filepath.Join(repo, "sub"), link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		out     string
		wantErr bool
	}{
		{"repo root", repo, true},
		{"repo subdir", filepath.Join(repo, "sub"), true},
		{"repo subdir not yet created", filepath.Join(repo, "sub", "new", "deeper"), true},
		{"symlink into repo", link, true},
		{"symlink into repo, new child", filepath.Join(link, "new"), true},
		{"temp dir outside", filepath.Join(outside, "shots"), false},
		{"parent of repo", filepath.Dir(repo), false},
		{"dotdot-named dir inside repo", filepath.Join(repo, "..shots"), true},
		{"dotdot-named dir inside repo, nested", filepath.Join(repo, "..shots", "a"), true},
		{"sibling with repo as name prefix", repo + "-shots", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := checkOutDir(tt.out, repo)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkOutDir(%q) err = %v, wantErr %v", tt.out, err, tt.wantErr)
			}
		})
	}
}

func TestRoutesCoverRequiredScreens(t *testing.T) {
	want := []string{"login", "inbox", "message", "compose", "calendar-month", "calendar-week",
		"calendar-day", "contacts", "junk", "rules", "settings", "security", "tags",
		"admin-dashboard", "admin-users", "admin-domains"}
	have := map[string]bool{}
	for _, r := range routeNames() {
		have[r] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("route %q missing", w)
		}
	}
	if len(variants) != 3 {
		t.Errorf("want 3 variants, got %d", len(variants))
	}
}
