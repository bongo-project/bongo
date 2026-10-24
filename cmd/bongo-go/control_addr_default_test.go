package main

import (
	"testing"

	"github.com/spf13/pflag"
)

// TestCLIDefaultsToControlListener pins every control-route command to the
// loopback control listener (server.control_addr), not the public web port.
func TestCLIDefaultsToControlListener(t *testing.T) {
	cases := []struct {
		name  string
		flags *pflag.FlagSet
		want  string
	}{
		{"queue", queueCmd.PersistentFlags(), "127.0.0.1:8081"},
		{"mail", mailCmd.PersistentFlags(), "127.0.0.1:8081"},
		{"cluster", clusterCmd.PersistentFlags(), "127.0.0.1:8081"},
		{"address", addressCmd.PersistentFlags(), "127.0.0.1:8081"},
		{"user create", userCreateCmd.Flags(), "http://127.0.0.1:8081"},
	}
	for _, tc := range cases {
		f := tc.flags.Lookup("http-addr")
		if f == nil {
			t.Fatalf("%s: no --http-addr flag", tc.name)
		}
		if f.DefValue != tc.want {
			t.Errorf("%s: --http-addr default = %q, want %q", tc.name, f.DefValue, tc.want)
		}
	}
}
