// Package i18n provides internationalization infrastructure for Bongo.
// It wraps the go-i18n/v2 library with a simplified API and embeds
// all message catalogue files.
package i18n

import (
	"embed"
	"encoding/json"
	"log/slog"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed messages/*.json
var messageFiles embed.FS

// LocaleInfo holds display information for a supported locale.
type LocaleInfo struct {
	Code string
	Name string
}

// supportedLocales lists all locales with their display names.
var supportedLocales = []LocaleInfo{
	{Code: "en", Name: "English"},
	{Code: "fr", Name: "Français"},
	{Code: "de", Name: "Deutsch"},
	{Code: "es", Name: "Español"},
}

// I18n wraps go-i18n Bundle with a simplified API.
type I18n struct {
	bundle *goi18n.Bundle
}

// NewI18n creates a new I18n instance with the given default locale.
// It loads all embedded message files. If a file fails to load, it
// logs a warning and continues so the application can still start.
func NewI18n(defaultLocale string) *I18n {
	tag := language.Make(defaultLocale)
	if tag == language.Und {
		tag = language.English
	}

	bundle := goi18n.NewBundle(tag)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	for _, locale := range supportedLocales {
		filename := "messages/" + locale.Code + ".json"
		if _, err := bundle.LoadMessageFileFS(messageFiles, filename); err != nil {
			slog.Warn("i18n: failed to load message file", "file", filename, "error", err)
		}
	}

	return &I18n{bundle: bundle}
}

// Localizer returns a go-i18n Localizer for the given locale code.
// Falls back to the bundle default language if locale is unknown.
func (i *I18n) Localizer(locale string) *goi18n.Localizer {
	return goi18n.NewLocalizer(i.bundle, locale, "en")
}

// T localizes a message by ID for the given locale.
// An optional data map can be provided for template interpolation.
// If the message ID is not found in any locale, the ID itself is returned.
func (i *I18n) T(locale, messageID string, data ...map[string]interface{}) string {
	loc := i.Localizer(locale)

	cfg := &goi18n.LocalizeConfig{
		MessageID: messageID,
	}

	if len(data) > 0 && data[0] != nil {
		cfg.TemplateData = data[0]
	}

	result, err := loc.Localize(cfg)
	if err != nil {
		// Message not found: return the ID as a safe fallback.
		return messageID
	}
	return result
}

// AvailableLocales returns the list of locales that have message files.
func (i *I18n) AvailableLocales() []LocaleInfo {
	result := make([]LocaleInfo, len(supportedLocales))
	copy(result, supportedLocales)
	return result
}
