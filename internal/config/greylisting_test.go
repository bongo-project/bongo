package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestGreylistingDefaults(t *testing.T) {
	c := Defaults()
	g := c.SMTP.GreylistSettings()
	if g.Enabled {
		t.Error("greylisting must be off by default")
	}
	d, w, l, err := g.Durations()
	if err != nil {
		t.Fatal(err)
	}
	if d != 5*time.Minute || w != 4*time.Hour || l != 36*24*time.Hour {
		t.Errorf("defaults = %v %v %v, want 5m 4h 864h", d, w, l)
	}
	if err := c.Validate(); err != nil && strings.Contains(err.Error(), "greylisting") {
		t.Errorf("defaults fail validation: %v", err)
	}
}

func TestGreylistingValidation(t *testing.T) {
	tests := []struct {
		name string
		g    SMTPGreylisting
		want string // substring of the error, "" for none
	}{
		{"defaults", SMTPGreylisting{}, ""},
		{"explicit ok", SMTPGreylisting{Delay: "1m", RetryWindow: "2h", PassLifetime: "2h"}, ""},
		{"delay not a duration", SMTPGreylisting{Delay: "five"}, "smtp.greylisting.delay"},
		{"retry_window not a duration", SMTPGreylisting{RetryWindow: "4d"}, "smtp.greylisting.retry_window"},
		{"pass_lifetime not a duration", SMTPGreylisting{PassLifetime: "36d"}, "smtp.greylisting.pass_lifetime"},
		{"delay zero", SMTPGreylisting{Delay: "0s"}, "smtp.greylisting.delay"},
		{"delay negative", SMTPGreylisting{Delay: "-1m"}, "smtp.greylisting.delay"},
		{"delay over an hour", SMTPGreylisting{Delay: "61m", RetryWindow: "24h"}, "smtp.greylisting.delay"},
		{"window not above delay", SMTPGreylisting{Delay: "10m", RetryWindow: "10m"}, "smtp.greylisting.retry_window"},
		{"window over a week", SMTPGreylisting{RetryWindow: "169h", PassLifetime: "1000h"}, "smtp.greylisting.retry_window"},
		{"lifetime below window", SMTPGreylisting{RetryWindow: "4h", PassLifetime: "3h"}, "smtp.greylisting.pass_lifetime"},
		{"lifetime over a year", SMTPGreylisting{PassLifetime: "8761h"}, "smtp.greylisting.pass_lifetime"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			g := tt.g
			c.SMTP.Greylisting = &g
			err := c.SMTP.GreylistSettings().validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to name %s", err, tt.want)
			}
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate err = %v, want it to name %s", err, tt.want)
			}
		})
	}
}

func TestGreylistingLoadsFromHCL(t *testing.T) {
	const base = `
server {
  role = "dev"
}
cluster {
  raft {}
  gossip {}
  tls {}
}
smtp {
  tls {}
  limits {}
  relay {}
  dkim {}
  %s
}
imap {}
pop3 {}
web {
  tls {}
}
caldav {}
antispam {
  builtin {
    enabled = true
  }
  rspamd {
    enabled = false
  }
}
antivirus {
  clamav {}
}
pipeline {}
telemetry {
  prometheus {}
  log {}
}
storage {
  pebble {}
}
`
	tests := []struct {
		name    string
		block   string
		enabled bool
		delay   string
		window  string
	}{
		{"block absent", "", false, "5m", "4h"},
		{"enabled only keeps default timings", "greylisting {\n enabled = true\n }", true, "5m", "4h"},
		{"timings set", "greylisting {\n enabled = true\n delay = \"2m\"\n retry_window = \"6h\"\n }", true, "2m", "6h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir() + "/config.hcl"
			if err := os.WriteFile(path, []byte(strings.Replace(base, "%s", tt.block, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFromFile(path)
			if err != nil {
				t.Fatalf("LoadFromFile: %v", err)
			}
			g := cfg.SMTP.GreylistSettings()
			if g.Enabled != tt.enabled || g.Delay != tt.delay || g.RetryWindow != tt.window || g.PassLifetime != "864h" {
				t.Fatalf("settings = %+v", g)
			}
		})
	}
}

func TestGreylistingKeysAreRestart(t *testing.T) {
	old := Defaults()
	nu := Defaults()
	nu.SMTP.Greylisting = &SMTPGreylisting{Enabled: true, Delay: "1m"}
	hot, restart, _ := Diff(old, nu)
	for _, k := range hot {
		if strings.HasPrefix(k.Key, "smtp.greylisting") {
			t.Errorf("%s classed hot, want restart", k.Key)
		}
	}
	found := map[string]bool{}
	for _, k := range restart {
		found[k.Key] = true
	}
	if !found["smtp.greylisting.enabled"] || !found["smtp.greylisting.delay"] {
		t.Errorf("restart changes = %v, want enabled and delay", restart)
	}
}
