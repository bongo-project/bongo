package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildBongo compiles the binary into dir.
func buildBongo(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "bongo")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// TestSendmailDispatchAndExitCodes runs the real binary both as "bongo sendmail"
// and through a link named sendmail. Both must skip cobra's flag parsing and
// exit with the sysexits code rather than 1.
func TestSendmailDispatchAndExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	bin := buildBongo(t, dir)
	link := filepath.Join(dir, "sendmail")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "absent.conf")

	tests := []struct {
		name string
		cmd  []string
		want int
	}{
		{"subcommand, missing config", []string{bin, "sendmail", "--config", missing, "-oi", "-FCronDaemon", "-B8BITMIME", "-t"}, 78},
		{"link, missing config", []string{link, "--config", missing, "-oi", "-FCronDaemon", "-B8BITMIME", "-t"}, 78},
		{"subcommand, -bs", []string{bin, "sendmail", "-bs"}, 64},
		{"link, -bs", []string{link, "-bs"}, 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := exec.Command(tt.cmd[0], tt.cmd[1:]...)
			c.Stdin = strings.NewReader("To: a@example.net\n\nx\n")
			var stderr bytes.Buffer
			c.Stderr = &stderr
			err := c.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != tt.want {
				t.Fatalf("err = %v, want exit %d (stderr %q)", err, tt.want, stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), "sendmail: ") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}
