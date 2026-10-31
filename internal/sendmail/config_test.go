package sendmail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodConf = `host = "127.0.0.1"
port = 2465
username = "alice@example.test"
password = "hunter2-secret"
`

func writeConfFile(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sendmail.conf")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // WriteFile is subject to umask
		t.Fatal(err)
	}
	return p
}

func wantExit(t *testing.T, err error, code int) *ExitError {
	t.Helper()
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != code {
		t.Fatalf("err = %v, want exit %d", err, code)
	}
	return ee
}

func TestLoadConfigPermissions(t *testing.T) {
	tests := []struct {
		mode os.FileMode
		ok   bool
	}{
		{0o600, true},
		{0o400, true},
		{0o640, false},
		{0o604, false},
		{0o644, false},
		{0o660, false},
		{0o666, false},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			_, err := LoadConfig(writeConfFile(t, goodConf, tt.mode))
			if tt.ok && err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if !tt.ok {
				ee := wantExit(t, err, ExitConfig)
				if strings.Contains(ee.Msg, "hunter2") {
					t.Error("message contains the password")
				}
			}
		})
	}
}

func TestLoadConfigNotRegularOrMissing(t *testing.T) {
	wantExit(t, mustFail(LoadConfig(t.TempDir())), ExitConfig)
	wantExit(t, mustFail(LoadConfig(filepath.Join(t.TempDir(), "absent.conf"))), ExitConfig)
}

func mustFail(_ *Config, err error) error { return err }

func TestLoadConfigDefaults(t *testing.T) {
	tests := []struct {
		name, body, wantTLS string
		wantPort            int
	}{
		{"implicit default", "host = \"h.example\"\nusername = \"a@example.test\"\npassword = \"p\"\n", "implicit", 465},
		{"587 implies starttls", "host = \"h.example\"\nport = 587\nusername = \"a@example.test\"\npassword = \"p\"\n", "starttls", 587},
		{"explicit starttls", "host = \"h.example\"\nport = 2587\ntls = \"starttls\"\nusername = \"a@example.test\"\npassword = \"p\"\n", "starttls", 2587},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := LoadConfig(writeConfFile(t, tt.body, 0o600))
			if err != nil {
				t.Fatal(err)
			}
			if c.TLS != tt.wantTLS || c.Port != tt.wantPort {
				t.Errorf("got tls=%q port=%d", c.TLS, c.Port)
			}
		})
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	base := func(extra string) string {
		return "host = \"h.example\"\nusername = \"a@example.test\"\npassword = \"p\"\n" + extra
	}
	tests := []struct{ name, body string }{
		{"missing host", "username = \"a@example.test\"\npassword = \"p\"\n"},
		{"missing username", "host = \"h.example\"\npassword = \"p\"\n"},
		{"missing password", "host = \"h.example\"\nusername = \"a@example.test\"\n"},
		{"empty password", "host = \"h.example\"\nusername = \"a@example.test\"\npassword = \"\"\n"},
		{"unqualified username", "host = \"h.example\"\nusername = \"alice\"\npassword = \"p\"\n"},
		{"bad tls", base("tls = \"none\"\n")},
		{"bad port", base("port = 70000\n")},
		{"unknown key", base("debug = true\n")},
		{"syntax error", "host = \n"},
		{"insecure on remote host", base("insecure_skip_verify = true\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(writeConfFile(t, tt.body, 0o600))
			wantExit(t, err, ExitConfig)
		})
	}
}

// A parse error on the line that holds the password must not echo it.
func TestLoadConfigErrorNeverQuotesPassword(t *testing.T) {
	body := "host = \"h.example\"\nusername = \"a@example.test\"\npassword = \"hunter2-secret\" oops\n"
	_, err := LoadConfig(writeConfFile(t, body, 0o600))
	ee := wantExit(t, err, ExitConfig)
	if strings.Contains(ee.Error(), "hunter2") {
		t.Fatalf("error quotes the password: %v", ee)
	}
	if !strings.Contains(ee.Msg, "line 3") {
		t.Errorf("error should name the line: %q", ee.Msg)
	}
}

func TestInsecureSkipVerifyLoopbackOnly(t *testing.T) {
	tests := []struct {
		host string
		ok   bool
	}{
		{"127.0.0.1", true},
		{"127.5.6.7", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST.", true},
		{"mail.example.com", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"0.0.0.0", false},
		{"localhost.evil.example", false},
		{"evil-localhost", false},
		{"127.0.0.1.evil.example", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			body := "host = \"" + tt.host + "\"\nusername = \"a@example.test\"\npassword = \"p\"\ninsecure_skip_verify = true\n"
			_, err := LoadConfig(writeConfFile(t, body, 0o600))
			if tt.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tt.ok {
				wantExit(t, err, ExitConfig)
			}
		})
	}
}
