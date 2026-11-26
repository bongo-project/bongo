package store

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateMailboxName(t *testing.T) {
	ns := DefaultIMAPNamespaces()
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrMailboxNameEmpty},
		{"max length", strings.Repeat("a", 255), nil},
		{"over length", strings.Repeat("a", 256), ErrMailboxNameTooLong},
		{"multi-byte at the limit", strings.Repeat("a", 253) + "é", nil},
		{"multi-byte past the limit", strings.Repeat("a", 254) + "é", ErrMailboxNameTooLong},
		{"shared prefix", "Shared", ErrMailboxNameReserved},
		{"under shared prefix", "shared/x", ErrMailboxNameReserved},
		{"prefix lookalike", "SharedFolder", nil},
		{"other users", "Other Users/a/b", ErrMailboxNameReserved},
		{"virtual unread", "unread", ErrMailboxNameReserved},
		{"virtual starred", "starred", ErrMailboxNameReserved},
		{"virtual all-mail", "all-mail", ErrMailboxNameReserved},
		{"virtual is case sensitive", "Unread", nil},
		{"double delimiter", "a//b", ErrMailboxNameInvalid},
		{"leading delimiter", "/a", ErrMailboxNameInvalid},
		{"trailing delimiter", "a/", ErrMailboxNameInvalid},
		{"NUL", "a\x00b", ErrMailboxNameInvalid},
		{"newline", "a\nb", ErrMailboxNameInvalid},
		{"DEL", "a\x7fb", ErrMailboxNameInvalid},
		{"invalid UTF-8", "a\xffb", ErrMailboxNameInvalid},
		{"hierarchy", "A/b", nil},
		{"plain", "Projects", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMailboxName(ns, tc.in)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("ValidateMailboxName(%q) = %v, want %v", tc.in, err, tc.want)
			}
		})
	}
	// A zero namespace uses the defaults.
	if err := ValidateMailboxName(IMAPNamespaces{}, "Shared/x"); !errors.Is(err, ErrMailboxNameReserved) {
		t.Errorf("zero namespaces: %v", err)
	}
}

func TestValidateOwnerMailboxName(t *testing.T) {
	if err := ValidateOwnerMailboxName("Shared/x"); err != nil {
		t.Errorf("a prefix is allowed owner-side: %v", err)
	}
	cases := []struct {
		in   string
		want error
	}{
		{"", ErrMailboxNameEmpty},
		{strings.Repeat("a", 256), ErrMailboxNameTooLong},
		{"unread", ErrMailboxNameReserved},
		{"a\x00b", ErrMailboxNameInvalid},
		{"a//b", ErrMailboxNameInvalid},
	}
	for _, tc := range cases {
		if err := ValidateOwnerMailboxName(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("ValidateOwnerMailboxName(%q) = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestProtectedMailbox(t *testing.T) {
	cases := map[string]bool{
		"INBOX": true, "inbox": true, "Sent": true, "Drafts": true, "Trash": true,
		"Junk": true, "Archive": true, "sent": false, "archive": false, "Projects": false,
	}
	for in, want := range cases {
		if got := ProtectedMailbox(in); got != want {
			t.Errorf("ProtectedMailbox(%q) = %v, want %v", in, got, want)
		}
	}
}
