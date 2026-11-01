package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionsAllowPrivateNetworks(t *testing.T) {
	if Defaults().SubscriptionsAllowPrivateNetworks() {
		t.Fatal("default must be false")
	}
	var nilCfg *Config
	if nilCfg.SubscriptionsAllowPrivateNetworks() {
		t.Fatal("nil config must be false")
	}
	on := Defaults()
	on.Calendar = &CalendarConfig{Subscriptions: &CalendarSubscriptionsConfig{AllowPrivateNetworks: true}}
	if !on.SubscriptionsAllowPrivateNetworks() || !Clone(on).SubscriptionsAllowPrivateNetworks() {
		t.Fatal("true must survive Clone")
	}
	if (&Config{Calendar: &CalendarConfig{}}).SubscriptionsAllowPrivateNetworks() {
		t.Fatal("empty calendar block must be false")
	}
}

// The shipped example documents the block; once uncommented it must decode,
// and a file without the block must keep decoding (the field is a pointer
// block because gohcl treats a plain struct block as mandatory).
func TestExampleCalendarBlockParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "bongo.example.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain.hcl")
	if err := os.WriteFile(plain, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(plain)
	if err != nil {
		t.Fatalf("example without the calendar block: %v", err)
	}
	if cfg.SubscriptionsAllowPrivateNetworks() {
		t.Fatal("commented-out block must leave the default")
	}

	uncommented, found := uncommentBlock(string(raw), "calendar {")
	if !found {
		t.Fatal("no commented-out `# calendar {` block in deploy/bongo.example.hcl")
	}
	if !strings.Contains(uncommented, "allow_private_networks") {
		t.Fatal("uncommented block lost its key")
	}
	tmp := filepath.Join(t.TempDir(), "uncommented.hcl")
	if err := os.WriteFile(tmp, []byte(uncommented), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFromFile(tmp)
	if err != nil {
		t.Fatalf("documented calendar block does not parse: %v", err)
	}
	if !cfg.SubscriptionsAllowPrivateNetworks() {
		t.Fatal("documented block did not set the key")
	}
}

func TestSubscriptionsKeyIsRestart(t *testing.T) {
	class, ok := ClassOf("calendar.subscriptions.allow_private_networks")
	if !ok || class != ClassRestart {
		t.Fatalf("class = %v, ok = %v; want restart", class, ok)
	}
	on := Defaults()
	on.Calendar = &CalendarConfig{Subscriptions: &CalendarSubscriptionsConfig{AllowPrivateNetworks: true}}
	_, restart, _ := Diff(Defaults(), on)
	if len(restart) != 1 || restart[0].Key != "calendar.subscriptions.allow_private_networks" {
		t.Fatalf("restart diff = %+v", restart)
	}
}
