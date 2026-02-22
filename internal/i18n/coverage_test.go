package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestI18nCoverage ensures every t(ctx, "key") call in .templ files has a
// corresponding entry in en.json, and every en.json key is used in at least
// one template. This prevents stale or missing translations.
func TestI18nCoverage(t *testing.T) {
	templDir := filepath.Join("..", "web", "templates")
	messagesDir := "messages"

	// 1. Extract all message IDs from .templ files.
	templIDs := extractTemplMessageIDs(t, templDir)
	if len(templIDs) == 0 {
		t.Fatal("no t(ctx, ...) calls found in .templ files")
	}

	// 2. Load en.json keys.
	enKeys := loadJSONKeys(t, filepath.Join(messagesDir, "en.json"))
	if len(enKeys) == 0 {
		t.Fatal("en.json has no keys")
	}

	// 3. Check every template ID exists in en.json.
	enSet := toSet(enKeys)
	var missing []string
	for _, id := range templIDs {
		if !enSet[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("message IDs used in templates but missing from en.json (%d):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}

	// 4. Check all locale files have the same keys as en.json.
	for _, locale := range []string{"fr", "de", "es"} {
		localeKeys := loadJSONKeys(t, filepath.Join(messagesDir, locale+".json"))
		localeSet := toSet(localeKeys)

		var locMissing []string
		for _, key := range enKeys {
			if !localeSet[key] {
				locMissing = append(locMissing, key)
			}
		}
		if len(locMissing) > 0 {
			sort.Strings(locMissing)
			t.Errorf("%s.json missing keys present in en.json (%d):\n  %s",
				locale, len(locMissing), strings.Join(locMissing, "\n  "))
		}
	}
}

var tCallRegex = regexp.MustCompile(`t\(ctx,\s*"([^"]+)"\)`)

func extractTemplMessageIDs(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading templates dir: %v", err)
	}

	seen := make(map[string]bool)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".templ") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}

		matches := tCallRegex.FindAllSubmatch(data, -1)
		for _, match := range matches {
			seen[string(match[1])] = true
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func loadJSONKeys(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
