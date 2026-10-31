package config

import (
	"strings"
	"testing"
)

func TestPostmasterAccountValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty is fine", "", false},
		{"plain address", "admin@example.org", false},
		{"display name", "Admin <admin@example.org>", true},
		{"angle brackets", "<admin@example.org>", true},
		{"no at", "admin", true},
		{"two ats", "a@b@example.org", true},
		{"empty domain", "admin@", true},
		{"empty local", "@example.org", true},
		{"surrounding space", " admin@example.org", true},
		{"embedded CRLF", "admin@example.org\r\nBcc: x@y.z", true},
		{"embedded NUL", "adm\x00in@example.org", true},
		{"embedded space", "ad min@example.org", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.SMTP.PostmasterAccount = tt.value
			err := c.Validate()
			if tt.wantErr && (err == nil || !strings.Contains(err.Error(), "smtp.postmaster_account")) {
				t.Fatalf("err = %v, want smtp.postmaster_account error", err)
			}
			if !tt.wantErr && err != nil && strings.Contains(err.Error(), "postmaster_account") {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
}

func TestPostmasterAccountDefaultEmptyAndRestartClass(t *testing.T) {
	if got := Defaults().SMTP.PostmasterAccount; got != "" {
		t.Fatalf("default = %q, want empty", got)
	}
	if c, ok := ClassOf("smtp.postmaster_account"); !ok || c != ClassRestart {
		t.Fatalf("ClassOf(smtp.postmaster_account) = %v, %v; want restart", c, ok)
	}
}
