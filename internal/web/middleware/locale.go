package middleware

import (
	"context"
	"net/http"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const localeKey contextKey = "locale"

// validLocales is the set of supported locale codes.
var validLocales = map[string]bool{
	"en": true,
	"fr": true,
	"de": true,
	"es": true,
}

// LocaleFromContext returns the locale stored in the request context.
// Falls back to "en" if not set.
func LocaleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(localeKey).(string); ok && v != "" {
		return v
	}
	return "en"
}

// WithLocale returns ctx carrying the given locale, as InjectLocale would set it.
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey, locale)
}

// InjectLocale returns middleware that reads the user's locale preference from
// Pebble and stores it in the request context. Must run after RequireAuth so
// that a user ID is available. If the preference cannot be loaded the locale
// defaults to "en".
func InjectLocale(store ProfileLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locale := localeFromStore(store, r)
			ctx := context.WithValue(r.Context(), localeKey, locale)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// localeFromStore loads the user's preferred locale from Pebble.
// Returns "en" on any error or if the stored locale is not supported.
func localeFromStore(store ProfileLookup, r *http.Request) string {
	userID, _ := UserFromContext(r.Context())
	if userID == "" {
		return "en"
	}

	key := []byte("/user/" + userID + "/preferences")
	data, err := store.GetKey(key)
	if err != nil || len(data) == 0 {
		return "en"
	}

	var prefs bongov1.UserPreferences
	if err := proto.Unmarshal(data, &prefs); err != nil {
		return "en"
	}

	if prefs.Locale != "" && validLocales[prefs.Locale] {
		return prefs.Locale
	}
	return "en"
}
