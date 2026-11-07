package main

import (
	"crypto/rand"
	"fmt"
	"log/slog"
)

const redacted = "[redacted]"

// Password carries a generated test-account password. Every formatting route
// prints "[redacted]"; Reveal is the only way out, for the code that creates
// the account.
type Password struct{ v string }

// NewPassword returns 26 base32 characters (about 130 bits), inside Bongo's
// 8 to 72 character limit.
func NewPassword() (Password, error) {
	s := rand.Text()
	if len(s) < 8 || len(s) > 72 {
		return Password{}, fmt.Errorf("generated password has length %d", len(s))
	}
	return Password{v: s}, nil
}

// Reveal returns the password. Callers must not log or report it.
func (p Password) Reveal() string { return p.v }

func (Password) String() string               { return redacted }
func (Password) GoString() string             { return redacted }
func (Password) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (Password) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Password) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Password) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte(redacted)) }
