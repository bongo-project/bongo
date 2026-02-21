package store

import (
	"testing"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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
