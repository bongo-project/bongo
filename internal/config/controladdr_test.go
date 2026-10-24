package config

import (
	"strings"
	"testing"
)

func TestValidateControlAddr(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"default loopback", "127.0.0.1:8081", false},
		{"ipv6 loopback", "[::1]:9000", false},
		{"other 127 address", "127.0.0.2:8081", false},
		{"wildcard", "0.0.0.0:8081", true},
		{"ipv6 wildcard", "[::]:8081", true},
		{"routable", "10.0.0.5:8081", true},
		{"hostname", "localhost:8081", true},
		{"no port", "127.0.0.1", true},
		{"port zero", "127.0.0.1:0", true},
		{"port too large", "127.0.0.1:70000", true},
		{"port not numeric", "127.0.0.1:http", true},
		{"empty", "", true},
	}
	for _, role := range []string{"dev", "server"} {
		for _, tt := range tests {
			t.Run(role+"/"+tt.name, func(t *testing.T) {
				cfg := Defaults()
				cfg.Server.Role = role
				cfg.Server.AdvertiseAddr = "127.0.0.1"
				cfg.Server.ControlAddr = tt.addr
				err := cfg.Validate()
				if (err != nil) != tt.wantErr {
					t.Fatalf("Validate() with %q = %v, wantErr %v", tt.addr, err, tt.wantErr)
				}
				if err != nil && !strings.Contains(err.Error(), "server.control_addr") {
					t.Errorf("error %q does not name server.control_addr", err)
				}
			})
		}
	}
}

func TestDefaultsControlAddr(t *testing.T) {
	if got := Defaults().Server.ControlAddr; got != "127.0.0.1:8081" {
		t.Errorf("default control_addr = %q, want 127.0.0.1:8081", got)
	}
}
