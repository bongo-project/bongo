package updatecheck

import (
	"net/url"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"0.17.0", "0.9.9", 1},
		{"1.0.0", "2.0.0", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"1.0.0-alpha.beta", "1.0.0-beta", -1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"1.0.0-rc.1", "1.0.0-rc.1", 0},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"18446744073709551615.0.0", "1.0.0", 1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			a, ok1 := ParseSemver(tt.a)
			b, ok2 := ParseSemver(tt.b)
			if !ok1 || !ok2 {
				t.Fatalf("parse failed: %v %v", ok1, ok2)
			}
			if got := a.Compare(b); got != tt.want {
				t.Errorf("Compare = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseSemverRefuses(t *testing.T) {
	for _, s := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.2.x", "dev", "1.2.3+build", "1.2.3-", " 1.2.3", "1.2.3\n", strings.Repeat("1", 70) + ".0.0", "99999999999999999999.0.0"} {
		if _, ok := ParseSemver(s); ok {
			t.Errorf("ParseSemver(%q) accepted", s)
		}
	}
}

func TestRunningVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"v0.17.0", "0.17.0", true},
		{"0.17.0", "0.17.0", true},
		{"v0.17.0-rc.1", "0.17.0-rc.1", true},
		{"dev", "", false},
		{"v0.17.0-3-gabcdef", "", false},
		{"v0.17.0-3-gabcdef-dirty", "", false},
		{"v0.17.0-dirty", "", false},
		{"v0.17.0-rc.1-2-g1a2b3c4", "", false},
		{"v0.17.0-rc.1-dirty", "", false},
		{"v0.17.0-beta-3", "0.17.0-beta-3", true},
		{"v0.17.0-3-g_x", "", false},
	}
	for _, tt := range tests {
		got, ok := RunningVersion(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("RunningVersion(%q) = %q, %v", tt.in, got, ok)
		}
	}
}

func TestParseResponse(t *testing.T) {
	check, _ := url.Parse("https://update.example.test/v1/check")
	tests := []struct {
		name, body string
		ok         bool
		security   bool
	}{
		{"valid", `{"latest":"0.17.2","release_notes_url":"https://update.example.test/r/0.17.2","security":true}`, true, true},
		{"security missing is false", `{"latest":"0.17.2","release_notes_url":"https://update.example.test/r"}`, true, false},
		{"unknown fields ignored", `{"latest":"0.17.2","release_notes_url":"https://update.example.test/r","x":1}`, true, false},
		{"host case", `{"latest":"0.17.2","release_notes_url":"https://UPDATE.example.test/r"}`, true, false},
		{"javascript URL", `{"latest":"0.17.2","release_notes_url":"javascript:alert(1)"}`, false, false},
		{"control char", "{\"latest\":\"0.17.2\",\"release_notes_url\":\"https://update.example.test/r\\u0000\"}", false, false},
		{"space", `{"latest":"0.17.2","release_notes_url":"https://update.example.test/a b"}`, false, false},
		{"subdomain", `{"latest":"0.17.2","release_notes_url":"https://x.update.example.test/r"}`, false, false},
		{"userinfo trick", `{"latest":"0.17.2","release_notes_url":"https://update.example.test@evil.example/r"}`, false, false},
		{"missing URL", `{"latest":"0.17.2"}`, false, false},
		{"latest wrong type", `{"latest":2,"release_notes_url":"https://update.example.test/r"}`, false, false},
		{"empty", ``, false, false},
		{"huge URL", `{"latest":"0.17.2","release_notes_url":"https://update.example.test/` + strings.Repeat("a", 3000) + `"}`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseResponse([]byte(tt.body), check)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if tt.ok && r.Security != tt.security {
				t.Errorf("security = %v", r.Security)
			}
			if err != nil && strings.Contains(err.Error(), "evil") {
				t.Errorf("error echoes the body: %v", err)
			}
		})
	}
}

func TestBannerFor(t *testing.T) {
	check, _ := url.Parse("https://update.example.test/v1/check")
	other, _ := url.Parse("https://other.example.test/v1/check")
	rec := Record{Latest: "0.18.0", ReleaseNotesURL: "https://update.example.test/r", Security: true}
	tests := []struct {
		name    string
		rec     Record
		check   *url.URL
		running string
		want    bool
	}{
		{"newer", rec, check, "v0.17.0", true},
		{"same", rec, check, "v0.18.0", false},
		{"older latest", rec, check, "v0.19.0", false},
		{"dev build", rec, check, "dev", false},
		{"configured host changed", rec, other, "v0.17.0", false},
		{"bad latest", Record{Latest: "<script>", ReleaseNotesURL: rec.ReleaseNotesURL}, check, "v0.17.0", false},
		{"bad URL", Record{Latest: "0.18.0", ReleaseNotesURL: "https://evil.example/x"}, check, "v0.17.0", false},
		{"empty", Record{}, check, "v0.17.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, ok := BannerFor(tt.rec, tt.check, tt.running)
			if ok != tt.want {
				t.Fatalf("ok = %v, want %v", ok, tt.want)
			}
			if ok && (b.Latest != "0.18.0" || !b.Security || b.URL != rec.ReleaseNotesURL) {
				t.Errorf("banner = %+v", b)
			}
		})
	}
}
