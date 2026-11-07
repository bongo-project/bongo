package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRunCheck(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
		wantStdout string
	}{
		{"no target-allow", []string{"check", "--test-domain", "test.invalid"}, 2, "--target-allow", ""},
		{"unknown target", []string{"check", "--target-allow", "lab.test.invalid", "--target", "other.test.invalid:25", "--test-domain", "test.invalid"}, 2, "not in --target-allow", ""},
		{"happy path", []string{"check", "--target-allow", "lab.test.invalid", "--target", "lab.test.invalid:25", "--test-domain", "test.invalid", "--rcpt", "a@test.invalid"}, 0, "", "configuration ok"},
		{"no subcommand", nil, 2, "usage", ""},
		{"unknown subcommand", []string{"nope"}, 2, "unknown subcommand", ""},
		{"bad flag", []string{"check", "--nope"}, 2, "", ""},
		{"stray argument", []string{"check", "x"}, 2, "unexpected argument", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tt.args, &out, &errb)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (stderr %q)", code, tt.wantCode, errb.String())
			}
			if !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr %q lacks %q", errb.String(), tt.wantStderr)
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout %q lacks %q", out.String(), tt.wantStdout)
			}
			if tt.wantCode != 0 && out.Len() != 0 {
				t.Errorf("refused run wrote stdout: %q", out.String())
			}
		})
	}
}

// TestCommandsRunOnlyAfterValidate proves the guard is structural: a command
// in the table never runs when Validate fails.
func TestCommandsRunOnlyAfterValidate(t *testing.T) {
	ran := false
	table := map[string]command{"fake": func(Config, io.Writer, io.Writer) int { ran = true; return 0 }}

	var errb bytes.Buffer
	if code := runWith(table, []string{"fake"}, io.Discard, &errb); code != 2 || ran {
		t.Fatalf("fake ran without allow list: code %d ran %v", code, ran)
	}
	args := []string{"fake", "--target-allow", "mail.tydewhatmay.eu", "--test-domain", "test.invalid"}
	if code := runWith(table, args, io.Discard, &errb); code != 2 || ran {
		t.Fatalf("fake ran against production: code %d ran %v", code, ran)
	}
	args = []string{"fake", "--target-allow", "lab.test.invalid", "--test-domain", "test.invalid"}
	if code := runWith(table, args, io.Discard, &errb); code != 0 || !ran {
		t.Fatalf("fake should run on a valid config: code %d ran %v", code, ran)
	}
}

func TestCommandsTableHoldsOnlyKnown(t *testing.T) {
	if _, ok := commands["check"]; !ok {
		t.Fatal("check missing from dispatch table")
	}
}
