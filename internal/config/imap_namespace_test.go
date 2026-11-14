package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/bongo-project/bongo/internal/store"
)

func TestValidateIMAPNamespacePrefixes(t *testing.T) {
	tests := []struct {
		name    string
		other   string
		shared  string
		wantErr string
	}{
		{"defaults", DefaultIMAPOtherUsersPrefix, DefaultIMAPSharedPrefix, ""},
		{"custom pair", "People", "Team", ""},
		{"empty other", "", "Shared", "imap.other_users_prefix"},
		{"empty shared", "Other Users", "", "imap.shared_prefix"},
		{"slash in other", "a/b", "Shared", "imap.other_users_prefix"},
		{"slash in shared", "Other Users", "a/b", "imap.shared_prefix"},
		{"INBOX shared", "Other Users", "INBOX", "imap.shared_prefix"},
		{"inbox other", "inbox", "Shared", "imap.other_users_prefix"},
		{"Inbox shared", "Other Users", "Inbox", "imap.shared_prefix"},
		{"leading space", " Shared", "Team", "white space"},
		{"only space", "Other Users", "  ", "white space"},
		{"star wildcard", "Other*", "Shared", "must not contain"},
		{"percent wildcard", "Other Users", "Shared%", "must not contain"},
		{"double quote", "Other\"Users", "Shared", "must not contain"},
		{"backslash", "Other Users", "Sha\\red", "must not contain"},
		{"control character", "Other\tUsers", "Shared", "control characters"},
		{"non-ASCII allowed", "Andere Benutzer", "Geteilt é", ""},
		{"equal", "Same", "Same", "must differ"},
		{"equal except case", "Same", "sAME", "must differ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Server.Role = "dev"
			cfg.IMAP.OtherUsersPrefix = tt.other
			cfg.IMAP.SharedPrefix = tt.shared
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestIMAPNamespaceDefaultsMatchStore(t *testing.T) {
	if DefaultIMAPOtherUsersPrefix != store.DefaultOtherUsersPrefix || DefaultIMAPSharedPrefix != store.DefaultSharedPrefix {
		t.Fatal("config and store IMAP namespace defaults have drifted")
	}
}

// An imap block written before these keys existed must keep the defaults, or
// every deployed config would fail validation at startup.
func TestLoadFromFileIMAPBlockKeepsNamespaceDefaults(t *testing.T) {
	// test/testdata/config.hcl has an imap block with ports only.
	path := "../../test/testdata/config.hcl"
	v := viper.New()
	v.Set("config", path)
	v.Set("role", "dev")
	cfg, err := Load(v)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.IMAP.OtherUsersPrefix != "Other Users" || cfg.IMAP.SharedPrefix != "Shared" {
		t.Fatalf("prefixes = %q, %q", cfg.IMAP.OtherUsersPrefix, cfg.IMAP.SharedPrefix)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDiffIMAPNamespacePrefixesAreRestart(t *testing.T) {
	for _, key := range []string{"imap.other_users_prefix", "imap.shared_prefix"} {
		t.Run(key, func(t *testing.T) {
			oldCfg := Defaults()
			newCfg := *oldCfg
			if key == "imap.shared_prefix" {
				newCfg.IMAP.SharedPrefix = "Team"
			} else {
				newCfg.IMAP.OtherUsersPrefix = "People"
			}
			hot, restart, _ := Diff(oldCfg, &newCfg)
			if len(hot) != 0 || len(restart) != 1 || keys(restart)[0] != key {
				t.Fatalf("hot=%v restart=%v, want restart [%s]", keys(hot), keys(restart), key)
			}
		})
	}
}
