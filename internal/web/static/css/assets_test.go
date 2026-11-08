package css_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

var applicationStyles = []string{
	"bongo.css",
	"bongo-mail.css",
	"bongo-apps.css",
	"bongo-utilities.css",
	"bongo-components.css",
}

func readApplicationCSS() ([]byte, error) {
	var combined bytes.Buffer
	for _, stylesheet := range applicationStyles {
		data, err := os.ReadFile(stylesheet)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", stylesheet, err)
		}
		combined.Write(data)
		combined.WriteByte('\n')
	}
	return combined.Bytes(), nil
}

func mustApplicationCSS(t testing.TB) string {
	t.Helper()
	data, err := readApplicationCSS()
	if err != nil {
		t.Fatalf("read application CSS: %v", err)
	}
	return string(data)
}

func mustReadCSSFile(t testing.TB, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestApplicationCSSIsSplitBySubsystem(t *testing.T) {
	for _, stylesheet := range applicationStyles {
		info, err := os.Stat(stylesheet)
		if err != nil {
			t.Fatalf("stat %s: %v", stylesheet, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", stylesheet)
		}
	}
	core, err := os.Stat("bongo.css")
	if err != nil {
		t.Fatal(err)
	}
	if core.Size() > 40_000 {
		t.Errorf("bongo.css grew back into a monolith: %d bytes", core.Size())
	}
	if strings.Contains(mustApplicationCSS(t), "COMPONENTLIB POLISH LAYER") {
		t.Error("the legacy polish override layer returned")
	}
}
