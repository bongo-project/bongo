package config

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestValidateLogComponents(t *testing.T) {
	tests := []struct {
		name    string
		comps   map[string]string
		wantErr string
	}{
		{"none", nil, ""},
		{"valid", map[string]string{"imap": "debug", "queue": "error", "smtp": "INFO"}, ""},
		{"unknown component", map[string]string{"imapp": "debug"}, `unknown component "imapp"`},
		{"unknown level", map[string]string{"imap": "verbose"}, `imap: unknown level "verbose"`},
		{"off is refused", map[string]string{"imap": "off"}, `unknown level "off"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.Telemetry.Log.Components = tt.comps
			err := c.Validate()
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

func TestComponentLevels(t *testing.T) {
	l := LogConfig{Components: map[string]string{"imap": "debug", "smtp": "Warn"}}
	got := l.ComponentLevels()
	if len(got) != 2 || got["imap"] != slog.LevelDebug || got["smtp"] != slog.LevelWarn {
		t.Errorf("ComponentLevels = %v", got)
	}
}

var (
	logPrefixRe     = regexp.MustCompile(`\.(?:Debug|Info|Warn|Error)(?:Context)?\((?:ctx, )?"([a-z0-9_-]+): `)
	componentAttrRe = regexp.MustCompile(`"component", "([a-z0-9_-]+)"`)
)

// Every message prefix and component attribute in the server source is a
// name the config accepts, and every accepted name is still used, so the list
// an operator reads in LOGGING.md is the list the server logs with.
func TestLogComponentsMatchSource(t *testing.T) {
	used := map[string]bool{}
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, re := range []*regexp.Regexp{logPrefixRe, componentAttrRe} {
				for _, m := range re.FindAllSubmatch(b, -1) {
					used[string(m[1])] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for name := range used {
		if !slices.Contains(LogComponents, name) {
			t.Errorf("log prefix %q is used in the source but missing from LogComponents", name)
		}
	}
	for _, name := range LogComponents {
		if !used[name] {
			t.Errorf("LogComponents lists %q, which no log call uses", name)
		}
	}
	if !slices.IsSorted(LogComponents) {
		t.Error("LogComponents is not sorted")
	}
}

// deploy/LOGGING.md lists every accepted component name.
func TestLoggingDocListsComponents(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "LOGGING.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range LogComponents {
		if !strings.Contains(string(b), "`"+name+"`") {
			t.Errorf("deploy/LOGGING.md does not list component %q", name)
		}
	}
}
