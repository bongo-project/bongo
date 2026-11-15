package store

import "strings"

// Default IMAP namespace prefixes. internal/config declares the same strings
// for its defaults; a test in that package pins the two together, because
// neither package may import the other.
const (
	DefaultOtherUsersPrefix = "Other Users"
	DefaultSharedPrefix     = "Shared"
)

// IMAPNamespaces holds the two folder prefixes RFC 2342 NAMESPACE reserves
// for other users' and shared mailboxes. A personal folder may not take either
// name, or a future shared folder could not be told apart from it.
type IMAPNamespaces struct {
	OtherUsers string
	Shared     string
}

// DefaultIMAPNamespaces returns the prefixes used when none are configured.
func DefaultIMAPNamespaces() IMAPNamespaces {
	return IMAPNamespaces{OtherUsers: DefaultOtherUsersPrefix, Shared: DefaultSharedPrefix}
}

// OrDefaults replaces an empty prefix with its default, so a setter that was
// never called cannot leave a prefix that matches nothing or everything.
func (n IMAPNamespaces) OrDefaults() IMAPNamespaces {
	if n.OtherUsers == "" {
		n.OtherUsers = DefaultOtherUsersPrefix
	}
	if n.Shared == "" {
		n.Shared = DefaultSharedPrefix
	}
	return n
}

// Reserves reports whether a personal folder called name collides with either
// prefix: the name equals it, or starts with it followed by "/". Matching is
// case-insensitive. A name that merely starts with the same letters
// ("SharedFolder") is allowed.
func (n IMAPNamespaces) Reserves(name string) bool {
	return reservesPrefix(name, n.OtherUsers) || reservesPrefix(name, n.Shared)
}

// IsOtherUsersName reports whether name lies in the other-users tree: it equals
// the prefix or starts with it and a "/". The match is case-insensitive.
func (n IMAPNamespaces) IsOtherUsersName(name string) bool {
	return reservesPrefix(name, n.OtherUsers)
}

// SplitOtherUsers cuts a name of the form {prefix}/{address}/{folder} into its
// address and folder. The address ends at the first "/", so an address that
// itself contains one is unreachable by design. ok is false when the name is
// outside the tree or any of the three segments is empty, which covers "//",
// a trailing "/", the bare prefix and the bare address.
func (n IMAPNamespaces) SplitOtherUsers(name string) (addr, folder string, ok bool) {
	if !n.IsOtherUsersName(name) || len(name) <= len(n.OtherUsers)+1 {
		return "", "", false
	}
	rest := name[len(n.OtherUsers)+1:]
	addr, folder, found := strings.Cut(rest, "/")
	if !found || addr == "" || folder == "" {
		return "", "", false
	}
	return addr, folder, true
}

func reservesPrefix(name, prefix string) bool {
	if prefix == "" {
		return false
	}
	if strings.EqualFold(name, prefix) {
		return true
	}
	// Compare on the byte length of the prefix plus the delimiter. Slicing
	// name is safe for EqualFold: a cut inside a multi-byte rune yields
	// invalid UTF-8, which cannot fold-equal a valid prefix.
	n := len(prefix) + 1
	return len(name) > len(prefix) && len(name) >= n && strings.EqualFold(name[:n], prefix+"/")
}
