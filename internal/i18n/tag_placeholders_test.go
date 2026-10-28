package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tag manager fills these labels in the browser by replacing the first
// %s with the tag name, so a translation that drops the placeholder shows the
// same label for every tag.
func TestTagLabelsKeepPlaceholder(t *testing.T) {
	keys := []string{"tag.countTitle", "tag.colourLabel", "tag.editLabel", "tag.deleteLabel"}
	for _, locale := range []string{"en", "de", "es", "fr"} {
		raw, err := os.ReadFile(filepath.Join("messages", locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(raw, &msgs); err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		for _, key := range keys {
			if got := strings.Count(msgs[key], "%s"); got != 1 {
				t.Errorf("%s %s = %q: want exactly one %%s, found %d", locale, key, msgs[key], got)
			}
		}
	}
}
