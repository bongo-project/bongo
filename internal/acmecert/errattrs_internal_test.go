package acmecert

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/acme"
)

// attr returns the value logged under key, and whether it was present.
func attr(attrs []any, key string) (string, bool) {
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i] == key {
			s, _ := attrs[i+1].(string)
			return s, true
		}
	}
	return "", false
}

// CA-supplied text reaches the log only through errAttrs, cut to maxDetail
// bytes plus a marker, and the error string itself carries none of it.
func TestErrAttrsTruncatesCAText(t *testing.T) {
	long := strings.Repeat("a", 500)
	want := strings.Repeat("a", maxDetail) + "..."
	tests := []struct {
		name string
		err  error
		key  string
		step string
	}{
		{"problem document", fail("order", &acme.Error{ProblemType: "urn:x", StatusCode: 400, Detail: long}), "detail", "order"},
		{"authorisation problem", fail("validation", &acme.AuthorizationError{
			Identifier: "mail.example.com",
			Errors:     []error{&acme.Error{ProblemType: "urn:y", Detail: long}},
		}), "detail", "validation"},
		{"transport error", fail("account", errors.New(long)), "error", "account"},
		{"bare error", errors.New(long), "error", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := errAttrs(tt.err)
			got, ok := attr(attrs, tt.key)
			if !ok {
				t.Fatalf("no %q attribute in %v", tt.key, attrs)
			}
			if got != want {
				t.Errorf("%s is %d bytes, want %d (%d plus the marker)", tt.key, len(got), len(want), maxDetail)
			}
			if tt.step == "" {
				return
			}
			if msg := tt.err.Error(); msg != "acme: "+tt.step+" failed" || strings.Contains(msg, "aaa") {
				t.Errorf("Error() = %q, carries CA text or the wrong step", msg)
			}
		})
	}
}

func TestTruncateKeepsShortText(t *testing.T) {
	for _, s := range []string{"", "short", strings.Repeat("b", maxDetail)} {
		if got := truncate(s); got != s {
			t.Errorf("truncate(%d bytes) = %d bytes, want it unchanged", len(s), len(got))
		}
	}
}
