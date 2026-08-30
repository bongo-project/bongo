package store

import (
	"log/slog"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// MixedCaseAddress is one address that is stored with upper-case characters
// while every lookup path lower-cases before reading.
type MixedCaseAddress struct {
	Stored     string // the address exactly as stored
	Normalised string // what every reader actually asks for
	Kind       string // "alias" or "account"
	Target     string // alias target username; empty for accounts
}

// ReportMixedCaseAddresses scans Pebble for addresses stored with upper-case
// characters and returns them. It only reads.
//
// Why this exists: RecipientResolver lower-cases an address before every tier
// of its lookup, and AddressStore lower-cases on read and write. For a long
// time neither CreateAlias nor account creation normalised on write, so an
// address entered with a capital was written to a key nothing ever asks for.
// The consequence is worse than a simple miss: an unresolvable recipient on an
// AUTHENTICATED submission session is not rejected, it is reclassified as
// external (smtp/server.go Rcpt), so mail to a local address is relayed off
// the server instead of delivered.
//
// The boot-time address-index migration (PopulateAddressIndex) lower-cases
// these into AddressStore, so an affected address starts resolving again after
// the next leader restart. That is why the symptom can look intermittent --
// it is broken from creation until the next restart. Reporting is therefore
// worth doing even though the migration will paper over it: the operator needs
// to know which addresses were unreachable, and for how long.
//
// Reporting only. Rewriting a live address key is the operator's decision:
// deleting and recreating the alias in the UI is safe and visible, whereas a
// silent rewrite at boot changes mail routing with no record.
func ReportMixedCaseAddresses(ps *PebbleStore, logger *slog.Logger) ([]MixedCaseAddress, error) {
	if ps == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	var found []MixedCaseAddress

	// Aliases: /system/alias/{addr} -> target username.
	iter, err := ps.PrefixScan([]byte("/system/alias/"))
	if err != nil {
		return nil, err
	}
	for iter.Next() {
		addr := strings.TrimPrefix(string(iter.Key()), "/system/alias/")
		if addr == "" || addr == strings.ToLower(addr) {
			continue
		}
		target := ""
		if v, vErr := iter.Value(); vErr == nil {
			target = strings.TrimSpace(string(v))
		}
		found = append(found, MixedCaseAddress{
			Stored:     addr,
			Normalised: strings.ToLower(addr),
			Kind:       "alias",
			Target:     target,
		})
	}
	if cErr := iter.Close(); cErr != nil {
		logger.Warn("mixed-case address report: closing alias iterator", "error", cErr)
	}

	// Accounts: /user/{id}/profile -> UserProfile.Email.
	pIter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return found, err
	}
	for pIter.Next() {
		parts := strings.Split(string(pIter.Key()), "/")
		if len(parts) != 4 || parts[3] != "profile" {
			continue
		}
		val, vErr := pIter.Value()
		if vErr != nil {
			continue
		}
		var profile bongov1.UserProfile
		if err := proto.Unmarshal(val, &profile); err != nil {
			continue
		}
		email := strings.TrimSpace(profile.Email)
		if email == "" || email == strings.ToLower(email) {
			continue
		}
		found = append(found, MixedCaseAddress{
			Stored:     email,
			Normalised: strings.ToLower(email),
			Kind:       "account",
		})
	}
	if cErr := pIter.Close(); cErr != nil {
		logger.Warn("mixed-case address report: closing profile iterator", "error", cErr)
	}

	return found, nil
}
