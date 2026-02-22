package i18n

import (
	"testing"
)

func TestNewI18n(t *testing.T) {
	i := NewI18n("en")
	if i == nil {
		t.Fatal("NewI18n returned nil")
	}
	if i.bundle == nil {
		t.Fatal("NewI18n: bundle is nil")
	}
}

func TestNewI18n_InvalidLocale(t *testing.T) {
	// Invalid locale should fall back to English without panicking.
	i := NewI18n("invalid-locale-xyz")
	if i == nil {
		t.Fatal("NewI18n with invalid locale returned nil")
	}
}

func TestT_EnglishString(t *testing.T) {
	i := NewI18n("en")

	got := i.T("en", "nav.unread")
	if got != "Unread" {
		t.Errorf("T(en, nav.unread) = %q, want %q", got, "Unread")
	}
}

func TestT_MultipleMessages(t *testing.T) {
	i := NewI18n("en")

	tests := []struct {
		messageID string
		want      string
	}{
		{"nav.starred", "Starred"},
		{"nav.allMail", "All Mail"},
		{"nav.contacts", "Contacts"},
		{"nav.calendar", "Calendar"},
		{"nav.admin", "Admin"},
		{"mail.reply", "Reply"},
		{"mail.forward", "Forward"},
		{"mail.delete", "Delete"},
		{"compose.send", "Send"},
		{"compose.saveDraft", "Save Draft"},
		{"calendar.month", "Month"},
		{"calendar.week", "Week"},
		{"calendar.day", "Day"},
		{"contacts.newContact", "New Contact"},
		{"admin.dashboard", "Dashboard"},
		{"settings.theme", "Theme"},
		{"auth.login", "Login"},
		{"auth.logout", "Logout"},
		{"common.save", "Save"},
		{"common.cancel", "Cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.messageID, func(t *testing.T) {
			got := i.T("en", tt.messageID)
			if got != tt.want {
				t.Errorf("T(en, %q) = %q, want %q", tt.messageID, got, tt.want)
			}
		})
	}
}

func TestT_UnknownLocaleFallsBackToEnglish(t *testing.T) {
	i := NewI18n("en")

	// An unsupported locale should fall back to English.
	got := i.T("xx", "nav.unread")
	if got != "Unread" {
		t.Errorf("T(xx, nav.unread) = %q, want English fallback %q", got, "Unread")
	}
}

func TestT_UnknownMessageIDReturnsID(t *testing.T) {
	i := NewI18n("en")

	messageID := "does.not.exist"
	got := i.T("en", messageID)
	if got != messageID {
		t.Errorf("T(en, %q) = %q, want message ID %q as fallback", messageID, got, messageID)
	}
}

func TestT_FrenchTranslation(t *testing.T) {
	i := NewI18n("en")

	got := i.T("fr", "nav.unread")
	if got != "Non lu" {
		t.Errorf("T(fr, nav.unread) = %q, want %q", got, "Non lu")
	}
}

func TestT_GermanTranslation(t *testing.T) {
	i := NewI18n("en")

	got := i.T("de", "nav.unread")
	if got != "Ungelesen" {
		t.Errorf("T(de, nav.unread) = %q, want %q", got, "Ungelesen")
	}
}

func TestT_SpanishTranslation(t *testing.T) {
	i := NewI18n("en")

	got := i.T("es", "nav.unread")
	if got != "Sin leer" {
		t.Errorf("T(es, nav.unread) = %q, want %q", got, "Sin leer")
	}
}

func TestAvailableLocales(t *testing.T) {
	i := NewI18n("en")

	locales := i.AvailableLocales()
	if len(locales) != 4 {
		t.Fatalf("AvailableLocales() returned %d locales, want 4", len(locales))
	}

	// Check that all expected locales are present.
	wantCodes := map[string]string{
		"en": "English",
		"fr": "Français",
		"de": "Deutsch",
		"es": "Español",
	}

	for _, locale := range locales {
		wantName, ok := wantCodes[locale.Code]
		if !ok {
			t.Errorf("unexpected locale code %q in AvailableLocales()", locale.Code)
			continue
		}
		if locale.Name != wantName {
			t.Errorf("locale %q: Name = %q, want %q", locale.Code, locale.Name, wantName)
		}
	}
}

func TestAvailableLocales_ReturnsCopy(t *testing.T) {
	i := NewI18n("en")

	locales1 := i.AvailableLocales()
	locales2 := i.AvailableLocales()

	// Mutating the result should not affect subsequent calls.
	locales1[0].Name = "modified"
	if locales2[0].Name == "modified" {
		t.Error("AvailableLocales() returned the same slice; mutations affect future calls")
	}
}

func TestLocalizer_NotNil(t *testing.T) {
	i := NewI18n("en")

	loc := i.Localizer("en")
	if loc == nil {
		t.Error("Localizer(en) returned nil")
	}
}

func TestLocalizer_UnknownLocale_NotNil(t *testing.T) {
	i := NewI18n("en")

	loc := i.Localizer("zz")
	if loc == nil {
		t.Error("Localizer(zz) returned nil for unknown locale")
	}
}

func TestT_WithTemplateData(t *testing.T) {
	// This test verifies the data parameter is plumbed through without panic.
	// Since none of our current messages use template variables, we just ensure
	// the method does not crash when data is provided.
	i := NewI18n("en")

	got := i.T("en", "nav.unread", map[string]interface{}{"count": 5})
	if got != "Unread" {
		t.Errorf("T with data: got %q, want %q", got, "Unread")
	}
}
