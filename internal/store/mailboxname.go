package store

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxMailboxNameLen bounds a folder name in bytes. The name becomes part of a
// Pebble key.
const MaxMailboxNameLen = 255

// ArchiveMailbox is the folder archived messages are moved to.
const ArchiveMailbox = "Archive"

// Reasons ValidateMailboxName refuses a name. Callers map each to fixed text;
// none carries the name, so it is never echoed.
var (
	ErrMailboxNameEmpty    = errors.New("mailbox name is empty")
	ErrMailboxNameTooLong  = errors.New("mailbox name is too long")
	ErrMailboxNameReserved = errors.New("mailbox name is reserved")
	ErrMailboxNameInvalid  = errors.New("mailbox name is invalid")
)

// ValidateMailboxName checks a new personal folder name. It is the one check
// IMAP CREATE and RENAME, web create and rename and the mbox import target
// share. It reads configuration only, never the store, so the answer reveals
// nothing about existing folders. "/" is the hierarchy delimiter.
//
// Only a NEW name is validated. A folder that predates a rule (an old "unread",
// an over-long name) can still be renamed away or deleted.
func ValidateMailboxName(ns IMAPNamespaces, name string) error {
	if err := validateMailboxShape(name); err != nil {
		return err
	}
	if ns.OrDefaults().Reserves(name) {
		return ErrMailboxNameReserved
	}
	return nil
}

// ValidateOwnerMailboxName is ValidateMailboxName for a name in the owner's own
// keyspace that IMAP built under the other-users prefix (the part after the
// owner's address). It never sits under a prefix, so only the shape and the
// virtual view names are checked.
func ValidateOwnerMailboxName(name string) error {
	return validateMailboxShape(name)
}

func validateMailboxShape(name string) error {
	switch {
	case name == "":
		return ErrMailboxNameEmpty
	case len(name) > MaxMailboxNameLen:
		return ErrMailboxNameTooLong
	case !utf8.ValidString(name):
		return ErrMailboxNameInvalid
	case strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return ErrMailboxNameInvalid
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return ErrMailboxNameInvalid
	}
	// Exact and case-sensitive, as the /mail/{mailbox} routes compare.
	if VirtualMailboxes[name] {
		return ErrMailboxNameReserved
	}
	return nil
}

// ProtectedMailbox reports whether the web may not rename or delete the folder:
// the standard folders (INBOX in any case) and Archive.
func ProtectedMailbox(name string) bool {
	if strings.EqualFold(name, "INBOX") || name == ArchiveMailbox {
		return true
	}
	for _, d := range DefaultMailboxes {
		if name == d {
			return true
		}
	}
	return false
}
