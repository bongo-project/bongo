package store

import (
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newPrefTestEnv(t *testing.T) *PreferenceStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewPreferenceStore(ps, &directApplier{store: ps})
}

func TestPreferenceStore_DefaultsForMissingUser(t *testing.T) {
	ps := newPrefTestEnv(t)
	prefs := ps.Get("nonexistent-user")

	if prefs.Theme != bongov1.Theme_THEME_AUTO {
		t.Errorf("expected THEME_AUTO, got %v", prefs.Theme)
	}
	if prefs.MessagesPerPage != 25 {
		t.Errorf("expected 25 messages per page, got %d", prefs.MessagesPerPage)
	}
	if prefs.Density != bongov1.DisplayDensity_DENSITY_COMFORTABLE {
		t.Errorf("expected DENSITY_COMFORTABLE, got %v", prefs.Density)
	}
	if prefs.Timezone != "UTC" {
		t.Errorf("expected UTC timezone, got %s", prefs.Timezone)
	}
}

func TestPreferenceStore_SetGetRoundTrip(t *testing.T) {
	ps := newPrefTestEnv(t)
	userID := "test-user-1"

	prefs := &bongov1.UserPreferences{
		Theme:           bongov1.Theme_THEME_DARK,
		LayoutMode:      bongov1.LayoutMode_LAYOUT_TWO_PANE,
		Density:         bongov1.DisplayDensity_DENSITY_SPACIOUS,
		MessagesPerPage: 50,
		DefaultReply:    bongov1.DefaultReplyMode_REPLY_MODE_ALL,
		Signature:       "-- Best regards",
		Timezone:        "America/New_York",
	}

	if err := ps.Set(userID, prefs); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got := ps.Get(userID)

	if got.Theme != bongov1.Theme_THEME_DARK {
		t.Errorf("theme: got %v, want THEME_DARK", got.Theme)
	}
	if got.LayoutMode != bongov1.LayoutMode_LAYOUT_TWO_PANE {
		t.Errorf("layout: got %v, want LAYOUT_TWO_PANE", got.LayoutMode)
	}
	if got.Density != bongov1.DisplayDensity_DENSITY_SPACIOUS {
		t.Errorf("density: got %v, want DENSITY_SPACIOUS", got.Density)
	}
	if got.MessagesPerPage != 50 {
		t.Errorf("messages_per_page: got %d, want 50", got.MessagesPerPage)
	}
	if got.DefaultReply != bongov1.DefaultReplyMode_REPLY_MODE_ALL {
		t.Errorf("default_reply: got %v, want REPLY_MODE_ALL", got.DefaultReply)
	}
	if got.Signature != "-- Best regards" {
		t.Errorf("signature: got %q, want %q", got.Signature, "-- Best regards")
	}
	if got.Timezone != "America/New_York" {
		t.Errorf("timezone: got %q, want %q", got.Timezone, "America/New_York")
	}
}

func TestPreferenceStore_SignatureHtmlRoundTrip(t *testing.T) {
	ps := newPrefTestEnv(t)
	userID := "test-user-sightml"

	prefs := &bongov1.UserPreferences{
		Signature:     "-- plain",
		SignatureHtml: "<b>x</b>",
	}

	if err := ps.Set(userID, prefs); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got := ps.Get(userID)

	if got.Signature != "-- plain" {
		t.Errorf("signature: got %q, want %q", got.Signature, "-- plain")
	}
	if got.SignatureHtml != "<b>x</b>" {
		t.Errorf("signature_html: got %q, want %q", got.SignatureHtml, "<b>x</b>")
	}
}

func TestPreferenceStore_UpdatedAtSet(t *testing.T) {
	ps := newPrefTestEnv(t)
	userID := "test-user-ts"

	before := time.Now().Unix()
	prefs := DefaultPreferences()
	if err := ps.Set(userID, prefs); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	after := time.Now().Unix()

	got := ps.Get(userID)
	if got.UpdatedAt < before || got.UpdatedAt > after {
		t.Errorf("updated_at %d not in range [%d, %d]", got.UpdatedAt, before, after)
	}
}

func TestResolveZone(t *testing.T) {
	// A non-UTC server zone, so "Local" would be visible if it leaked through.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	prev := time.Local
	time.Local = tokyo
	t.Cleanup(func() { time.Local = prev })

	for _, tt := range []struct {
		name   string
		in     string
		offset int // seconds east of UTC on 2026-11-05 12:00 UTC
	}{
		{"valid", "America/New_York", -5 * 3600},
		{"far east", "Pacific/Kiritimati", 14 * 3600},
		{"posix inverted sign", "Etc/GMT+12", -12 * 3600},
		{"empty", "", 0},
		{"Local is not the server zone", "Local", 0},
		{"unknown", "Not/AZone", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, off := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).In(ResolveZone(tt.in)).Zone()
			if off != tt.offset {
				t.Errorf("offset = %d, want %d", off, tt.offset)
			}
		})
	}
}

func TestPreferenceStore_Location(t *testing.T) {
	s := newPrefTestEnv(t)
	if got := s.Location("nobody"); got != time.UTC {
		t.Errorf("unset user zone = %v, want UTC", got)
	}
	prefs := DefaultPreferences()
	prefs.Timezone = "Pacific/Kiritimati"
	if err := s.Set("u1", prefs); err != nil {
		t.Fatal(err)
	}
	if got := s.Location("u1").String(); got != "Pacific/Kiritimati" {
		t.Errorf("zone = %q", got)
	}
}
