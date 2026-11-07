package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// parseArgs builds a Config through the real flag set.
func parseArgs(t *testing.T, args ...string) (Config, error) {
	t.Helper()
	var cfg Config
	fs := newFlagSet("test", &cfg, io.Discard)
	err := fs.Parse(args)
	return cfg, err
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string // empty means accepted
	}{
		{"minimal ok", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid"}, ""},
		{"no allow list", []string{"--test-domain", "test.invalid"}, "no --target-allow"},
		{"target not listed", []string{"--target-allow", "lab.test.invalid", "--target", "other.test.invalid:25", "--test-domain", "test.invalid"}, "not in --target-allow"},
		{"target listed", []string{"--target-allow", "lab.test.invalid", "--target", "lab.test.invalid:25", "--test-domain", "test.invalid"}, ""},
		{"case and trailing dot", []string{"--target-allow", "lab.test.invalid", "--target", "LAB.Test.Invalid.:25", "--test-domain", "TEST.Invalid."}, ""},
		{"port mismatch", []string{"--target-allow", "lab.test.invalid:587", "--target", "lab.test.invalid:25", "--test-domain", "test.invalid"}, "not in --target-allow"},
		{"port match", []string{"--target-allow", "lab.test.invalid:587", "--target", "lab.test.invalid:587", "--test-domain", "test.invalid"}, ""},
		{"portless target against port entry", []string{"--target-allow", "lab.test.invalid:587", "--target", "lab.test.invalid", "--test-domain", "test.invalid"}, "not in --target-allow"},
		{"ip literal not listed", []string{"--target-allow", "localhost", "--target", "127.0.0.1:25", "--test-domain", "test.invalid"}, "not in --target-allow"},
		{"ip literal listed", []string{"--target-allow", "127.0.0.1", "--target", "127.0.0.1:25", "--test-domain", "test.invalid"}, ""},
		{"ipv6 bracketed not listed", []string{"--target-allow", "localhost", "--target", "[::1]:25", "--test-domain", "test.invalid"}, "not in --target-allow"},
		{"ipv6 listed in other form", []string{"--target-allow", "0:0::1", "--target", "[::1]:25", "--test-domain", "test.invalid"}, ""},
		{"v4 mapped matches v4", []string{"--target-allow", "10.0.0.1", "--target", "[::ffff:10.0.0.1]:25", "--test-domain", "test.invalid"}, ""},
		{"production host listed", []string{"--target-allow", "mail.tydewhatmay.eu", "--test-domain", "test.invalid"}, "production"},
		{"production mixed case trailing dot", []string{"--target-allow", "Mail.TydeWhatMay.EU.", "--test-domain", "test.invalid"}, "production"},
		{"production subdomain", []string{"--target-allow", "x.mail.tydewhatmay.eu", "--test-domain", "test.invalid"}, "production"},
		{"production apex", []string{"--target-allow", "tydewhatmay.eu", "--test-domain", "test.invalid"}, "production"},
		{"production override", []string{"--target-allow", "mail.tydewhatmay.eu", "--i-own-this-target", "--test-domain", "test.invalid"}, ""},
		{"production lookalike prefix is not production", []string{"--target-allow", "tydewhatmay.eu.evil.test", "--target", "tydewhatmay.eu.evil.test:25", "--test-domain", "test.invalid"}, ""},
		{"production lookalike unlisted", []string{"--target-allow", "lab.test.invalid", "--target", "tydewhatmay.eu.evil.test:25", "--test-domain", "test.invalid"}, "not in --target-allow"},
		{"empty comma item", []string{"--target-allow", "a.test.invalid,,b.test.invalid", "--test-domain", "test.invalid"}, "empty item"},
		{"bad port", []string{"--target-allow", "lab.test.invalid:70000", "--test-domain", "test.invalid"}, "port"},
		{"unicode host", []string{"--target-allow", "läb.test.invalid", "--test-domain", "test.invalid"}, "character"},
		{"missing test domain", []string{"--target-allow", "lab.test.invalid"}, "--test-domain is required"},
		{"single label test domain eu", []string{"--target-allow", "lab.test.invalid", "--test-domain", "eu"}, "two labels"},
		{"single label test domain com", []string{"--target-allow", "lab.test.invalid", "--test-domain", "com"}, "two labels"},
		{"test domain is production", []string{"--target-allow", "lab.test.invalid", "--test-domain", "tydewhatmay.eu"}, "overlaps"},
		{"test domain under production", []string{"--target-allow", "lab.test.invalid", "--test-domain", "lab.tydewhatmay.eu"}, "overlaps"},
		{"production override keeps domain rule", []string{"--target-allow", "mail.tydewhatmay.eu", "--i-own-this-target", "--test-domain", "tydewhatmay.eu"}, "overlaps"},
		{"sender in test domain", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--from", "a@test.invalid", "--rcpt", "b@x.TEST.invalid."}, ""},
		{"sender suffix lookalike", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--from", "a@eviltest.invalid"}, "outside test domain"},
		{"recipient suffix lookalike", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--rcpt", "a@eviltest.invalid"}, "outside test domain"},
		{"recipient test domain as prefix", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--rcpt", "a@test.invalid.evil.com"}, "outside test domain"},
		{"recipient empty domain", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--rcpt", "user@"}, "empty host"},
		{"recipient no at", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--rcpt", "user.test.invalid"}, "no '@'"},
		{"recipient empty local", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--rcpt", "@test.invalid"}, "empty local"},
		{"recipient at in local part", []string{"--target-allow", "lab.test.invalid", "--test-domain", "test.invalid", "--rcpt", "a@test.invalid@evil.com"}, "outside test domain"},
		{"sender production", []string{"--target-allow", "mail.tydewhatmay.eu", "--i-own-this-target", "--test-domain", "test.invalid", "--from", "a@tydewhatmay.eu"}, "outside test domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseArgs(t, tt.args...)
			if err == nil {
				err = Validate(cfg)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want accepted, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestInDomain(t *testing.T) {
	tests := []struct {
		name, domain string
		want         bool
	}{
		{"test.invalid", "test.invalid", true},
		{"a.test.invalid", "test.invalid", true},
		{"eviltest.invalid", "test.invalid", false},
		{"test.invalid.evil.com", "test.invalid", false},
		{"invalid", "test.invalid", false},
	}
	for _, tt := range tests {
		if got := inDomain(tt.name, tt.domain); got != tt.want {
			t.Errorf("inDomain(%q, %q) = %v, want %v", tt.name, tt.domain, got, tt.want)
		}
	}
}

// TestTestDomainAncestor covers the ancestor rule beyond the single-label
// rule: with a three-label production domain, its two-label parent would admit
// production addresses.
func TestTestDomainAncestor(t *testing.T) {
	oldD, oldH := productionDomains, productionHosts
	t.Cleanup(func() { productionDomains, productionHosts = oldD, oldH })
	productionDomains, productionHosts = []string{"mail.corp.test"}, nil

	if _, err := checkTestDomain("corp.test"); err == nil {
		t.Error("ancestor of a production domain accepted")
	}
	if _, err := checkTestDomain("x.mail.corp.test"); err == nil {
		t.Error("subdomain of a production domain accepted")
	}
	if _, err := checkTestDomain("other.test"); err != nil {
		t.Errorf("unrelated domain refused: %v", err)
	}
}

func TestCheckTargetDirect(t *testing.T) {
	cfg, err := parseArgs(t, "--target-allow", "lab.test.invalid:25")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTarget(cfg, "lab.test.invalid:25"); err != nil {
		t.Errorf("listed target refused: %v", err)
	}
	if err := checkTarget(cfg, "lab.test.invalid:26"); err == nil {
		t.Error("unlisted port accepted")
	}
	if err := checkTarget(Config{}, "lab.test.invalid:25"); err == nil {
		t.Error("empty allow list accepted a target")
	}
}

var deployName = regexp.MustCompile(`(hostname|domain)\s*=\s*"([^"]+)"`)

// TestDeployHostsRefused proves the hosts named in the deploy examples are
// refused when listed, and accepted only with the override.
func TestDeployHostsRefused(t *testing.T) {
	files, err := filepath.Glob("../../deploy/*.hcl")
	if err != nil || len(files) == 0 {
		t.Fatalf("no deploy HCL files found: %v", err)
	}
	names := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			if m := deployName.FindStringSubmatch(line); m != nil {
				names[m[2]] = true
			}
		}
	}
	hostFound := false
	for n := range names {
		if strings.HasPrefix(n, "mail.") {
			hostFound = true
		}
	}
	if len(names) == 0 || !hostFound {
		t.Fatalf("regexp found no mail hostname in deploy files (names: %v); test would be vacuous", names)
	}
	names["tydewhatmay.eu"] = true
	names["mail.tydewhatmay.eu"] = true
	for n := range names {
		t.Run(n, func(t *testing.T) {
			cfg, err := parseArgs(t, "--target-allow", n, "--test-domain", "test.invalid")
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "production") {
				t.Fatalf("listed production name %q not refused: %v", n, err)
			}
			cfg.IOwnThisTarget = true
			if err := Validate(cfg); err != nil {
				t.Fatalf("override should accept %q: %v", n, err)
			}
		})
	}
}
