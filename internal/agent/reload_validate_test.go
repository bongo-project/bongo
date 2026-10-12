package agent

import (
	"strings"
	"testing"
)

func TestReload_ConfigValidateFailureAppliesNothing(t *testing.T) {
	f := newReloadFixture(t, defaultConf(), nil)
	bad := defaultConf()
	bad.Level = "debug"
	bad.Role = "bogus"
	f.write(bad)
	if err := f.reload(); err == nil {
		t.Fatal("Reload accepted an unknown role")
	}
	if f.debugOn() {
		t.Error("log level applied despite Validate failure")
	}
}

func TestValidateSessionSecret(t *testing.T) {
	long := strings.Repeat("k", 32)
	tests := []struct {
		name, role, secret string
		wantErr            bool
	}{
		{"dev empty is generated", "dev", "", false},
		{"dev short allowed", "dev", "short", false},
		{"server empty", "server", "", true},
		{"server short", "server", strings.Repeat("k", 31), true},
		{"server minimum", "server", long, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSessionSecret(tc.role, tc.secret)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Error("error contains the secret")
			}
		})
	}
}
