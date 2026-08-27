package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// stubStore is a minimal ProfileLookup used in locale tests.
type stubStore struct {
	data map[string][]byte
}

func (s *stubStore) GetKey(key []byte) ([]byte, error) {
	v, ok := s.data[string(key)]
	if !ok {
		return nil, http.ErrNoCookie
	}
	return v, nil
}

func marshalPrefs(t *testing.T, locale string) []byte {
	t.Helper()
	prefs := &bongov1.UserPreferences{Locale: locale}
	b, err := proto.Marshal(prefs)
	if err != nil {
		t.Fatalf("marshal prefs: %v", err)
	}
	return b
}

func TestLocaleFromContext_Default(t *testing.T) {
	got := LocaleFromContext(context.Background())
	if got != "en" {
		t.Errorf("expected en, got %q", got)
	}
}

func TestInjectLocale_NoUser(t *testing.T) {
	store := &stubStore{data: map[string][]byte{}}
	mw := InjectLocale(store)

	var capturedLocale string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLocale = LocaleFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if capturedLocale != "en" {
		t.Errorf("expected en for unauthenticated request, got %q", capturedLocale)
	}
}

func TestInjectLocale_WithLocale(t *testing.T) {
	tests := []struct {
		locale   string
		expected string
	}{
		{"fr", "fr"},
		{"de", "de"},
		{"es", "es"},
		{"en", "en"},
		{"xx", "en"}, // unsupported locale falls back to en
		{"", "en"},   // empty falls back to en
	}

	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			userID := "user-1"
			store := &stubStore{data: map[string][]byte{
				"/user/" + userID + "/preferences": marshalPrefs(t, tc.locale),
			}}

			mw := InjectLocale(store)

			var capturedLocale string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedLocale = LocaleFromContext(r.Context())
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			// Inject user context as RequireAuth would.
			ctx := context.WithValue(req.Context(), userIDKey, userID)
			ctx = context.WithValue(ctx, emailKey, "user@example.com")
			req = req.WithContext(ctx)

			mw(next).ServeHTTP(httptest.NewRecorder(), req)

			if capturedLocale != tc.expected {
				t.Errorf("locale=%q: expected %q, got %q", tc.locale, tc.expected, capturedLocale)
			}
		})
	}
}
