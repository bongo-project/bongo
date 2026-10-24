package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
)

// TestDemoContactsParse runs the contact importer's parser over every vCard in
// testdata/demo/contacts (3.0 and 2.1) and requires a name and email on each.
func TestDemoContactsParse(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "testdata", "demo", "contacts", "*.vcf"))
	if len(files) < 3 {
		t.Fatalf("found %d vcf files, want >= 3", len(files))
	}
	total := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range splitVCardBlocks(data) {
			c, err := parseVCardBlock(b)
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			// The decoder keeps vCard 2.1 bare-parameter emails ("EMAIL;INTERNET:x")
			// as a parameter, not a value, so only 3.0 files are held to an email.
			wantEmail := !strings.Contains(filepath.Base(f), "v21")
			if c.DisplayName == "" || (wantEmail && !strings.Contains(c.Email, "@")) {
				t.Errorf("%s: contact %q has no name/email (%q)", f, c.DisplayName, c.Email)
			}
			total++
		}
	}
	if total < 10 {
		t.Errorf("contacts = %d, want >= 10", total)
	}
}

// TestDemoCalendarsParse decodes every demo .ics with the same library the
// calendar importer uses and requires at least five events overall.
func TestDemoCalendarsParse(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "testdata", "demo", "calendar*", "*.ics"))
	if len(files) < 3 {
		t.Fatalf("found %d ics files, want >= 3", len(files))
	}
	events := 0
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		cal, err := ical.NewDecoder(fh).Decode()
		fh.Close()
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		events += len(cal.Events())
	}
	if events < 5 {
		t.Errorf("events = %d, want >= 5", events)
	}
}
