package store

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// UserDomains looks up the domain of a user's primary address. The delivery
// stage uses it to pick the domain a user's forwards are sent from.
type UserDomains struct {
	Pebble *PebbleStore
}

// UserDomain returns the lowercased domain of the user's profile email. A
// missing profile, an address without a domain and a read error all return an
// error, so the caller never invents a domain.
func (u *UserDomains) UserDomain(userID string) (string, error) {
	if u == nil || u.Pebble == nil {
		return "", fmt.Errorf("user domain: no profile store")
	}
	data, err := u.Pebble.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return "", fmt.Errorf("user domain: load profile %q: %w", userID, err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return "", fmt.Errorf("user domain: decode profile %q: %w", userID, err)
	}
	at := strings.LastIndexByte(profile.Email, '@')
	if at < 0 || at == len(profile.Email)-1 {
		return "", fmt.Errorf("user domain: profile %q has no address domain", userID)
	}
	return strings.ToLower(profile.Email[at+1:]), nil
}

// PrimaryEmail returns the user's profile email, trimmed. A missing profile
// and a read error both return an error.
func (u *UserDomains) PrimaryEmail(userID string) (string, error) {
	if u == nil || u.Pebble == nil {
		return "", fmt.Errorf("primary email: no profile store")
	}
	data, err := u.Pebble.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return "", fmt.Errorf("primary email: load profile %q: %w", userID, err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return "", fmt.Errorf("primary email: decode profile %q: %w", userID, err)
	}
	return strings.TrimSpace(profile.Email), nil
}
