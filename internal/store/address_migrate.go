package store

import (
	"log/slog"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// PopulateAddressIndex seeds the global address index (AddressStore) from
// existing Pebble data.
//
// Pass 1 scans /user/*/profile, unmarshals each UserProfile and creates a
// primary AddressRecord (kind=primary, can_send=true, verified=true) owned by
// the user whose ID is embedded in the key, building a
// map[loweredEmail → userID] as a side effect for pass 2.
//
// Pass 2 scans /system/alias/{addr}. Each value is the target user's username
// (== their email), resolved to a user_id via the pass-1 map or, failing that,
// the SystemIndex users table. Aliases with an unresolvable target are logged
// and skipped.
//
// Create-only: an address already present in Pebble (the source of truth) is
// left untouched, so admin-added or admin-edited records are never overwritten
// on restart. The emailToUserID map is still populated for EVERY primary
// profile, including the ones whose Put is skipped — otherwise pass 2 could not
// resolve aliases whose target primary was already in the index.
//
// Intended to run on the Raft leader at startup only. Leadership changes after
// boot will NOT re-trigger the migration — the same limitation as
// ensureConfiguredDomain.
//
// logger may be nil; slog.Default() is used in that case.
func PopulateAddressIndex(ps *PebbleStore, as *AddressStore, sys *SystemIndex, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	// --- Pass 1: enumerate /user/*/profile to create primary records. ---

	// emailToUserID maps a lowercased user email to the Pebble user_id.
	// Built here and reused in pass 2 to resolve alias targets.
	emailToUserID := make(map[string]string)

	{
		iter, err := ps.PrefixScan([]byte("/user/"))
		if err != nil {
			return err // non-recoverable: cannot scan Pebble at all
		}

		for iter.Next() {
			key := string(iter.Key())

			// Only process the profile key: /user/{user_id}/profile.
			// The key space also contains /user/{id}/mail/{guid}, /user/{id}/tag/{name}, etc.
			// Splitting on "/" yields ["", "user", "<id>", "profile"] — exactly 4 parts.
			parts := strings.Split(key, "/")
			if len(parts) != 4 || parts[3] != "profile" {
				continue
			}
			userID := parts[2]
			if userID == "" {
				continue
			}

			valBytes, err := iter.Value()
			if err != nil {
				logger.Warn("address index migration: reading profile value",
					"key", key, "error", err)
				continue
			}

			var profile bongov1.UserProfile
			if err := proto.Unmarshal(valBytes, &profile); err != nil {
				logger.Warn("address index migration: unmarshaling profile",
					"key", key, "error", err)
				continue
			}

			email := strings.ToLower(strings.TrimSpace(profile.Email))
			if email == "" {
				logger.Warn("address index migration: profile has empty email; skipping",
					"user_id", userID)
				continue
			}

			atIdx := strings.LastIndex(email, "@")
			if atIdx <= 0 || atIdx == len(email)-1 {
				logger.Warn("address index migration: malformed email in profile; skipping",
					"user_id", userID, "email", email)
				continue
			}
			domain := email[atIdx+1:]

			// Always populate the map — even if we skip Put below — so that
			// pass 2 alias targets can be resolved for already-present primaries.
			emailToUserID[email] = userID

			// Create-only: skip Put if the record already exists in Pebble.
			// This preserves admin-edited fields (e.g. can_send, verified toggles)
			// across restarts.
			if _, exists, err := as.Get(email); err != nil {
				logger.Warn("address index migration: checking existing primary record",
					"email", email, "user_id", userID, "error", err)
				// Fall through and try to write anyway so a transient read error
				// does not permanently suppress the record.
			} else if exists {
				// Record already in Pebble — preserve it unchanged.
				continue
			}

			rec := &bongov1.AddressRecord{
				UserId:   userID,
				Domain:   domain,
				Kind:     "primary",
				CanSend:  true,
				Verified: true,
			}
			if err := as.Put(email, rec); err != nil {
				logger.Warn("address index migration: writing primary record",
					"email", email, "user_id", userID, "error", err)
				// Best-effort: log and continue rather than aborting startup.
			}
		}

		if err := iter.Close(); err != nil {
			logger.Warn("address index migration: closing user iterator", "error", err)
		}
	}

	// --- Pass 2: enumerate /system/alias/{addr} to create alias records. ---

	{
		iter, err := ps.PrefixScan([]byte("/system/alias/"))
		if err != nil {
			return err // non-recoverable
		}

		for iter.Next() {
			key := string(iter.Key())
			aliasAddr := strings.ToLower(strings.TrimPrefix(key, "/system/alias/"))
			if aliasAddr == "" {
				continue
			}

			atIdx := strings.LastIndex(aliasAddr, "@")
			if atIdx <= 0 || atIdx == len(aliasAddr)-1 {
				logger.Warn("address index migration: malformed alias address; skipping",
					"alias", aliasAddr)
				continue
			}
			domain := aliasAddr[atIdx+1:]

			valBytes, err := iter.Value()
			if err != nil {
				logger.Warn("address index migration: reading alias value",
					"alias", aliasAddr, "error", err)
				continue
			}

			// /system/alias/{addr} stores the target USERNAME (== email) as the value.
			// This is NOT a user_id; it must be resolved via the profile map or SystemIndex.
			// See admin.go CreateAlias: Value: []byte(targetUser) where targetUser is the email.
			targetUsername := strings.ToLower(strings.TrimSpace(string(valBytes)))
			if targetUsername == "" {
				logger.Warn("address index migration: alias has empty target; skipping",
					"alias", aliasAddr)
				continue
			}

			// Primary resolution: profile map built in pass 1.
			userID, ok := emailToUserID[targetUsername]
			if !ok && sys != nil {
				// Fallback: SystemIndex users table (handles edge cases where a profile
				// exists in SQLite but the Pebble profile key was somehow missing).
				var id string
				if err := sys.DB().QueryRow(
					"SELECT id FROM users WHERE username = ?", targetUsername,
				).Scan(&id); err == nil {
					userID = id
				}
			}

			if userID == "" {
				logger.Warn("address index migration: alias target unresolvable; skipping",
					"alias", aliasAddr, "target", targetUsername)
				continue
			}

			// Create-only: skip Put if the alias record already exists in Pebble.
			// This preserves any admin-edited alias records across restarts.
			if _, exists, err := as.Get(aliasAddr); err != nil {
				logger.Warn("address index migration: checking existing alias record",
					"alias", aliasAddr, "error", err)
				// Fall through on read error.
			} else if exists {
				// Record already in Pebble — preserve it unchanged.
				continue
			}

			rec := &bongov1.AddressRecord{
				UserId:   userID,
				Domain:   domain,
				Kind:     "alias",
				CanSend:  false,
				Verified: true,
			}
			if err := as.Put(aliasAddr, rec); err != nil {
				logger.Warn("address index migration: writing alias record",
					"alias", aliasAddr, "target_user_id", userID, "error", err)
				// Best-effort: log and continue.
			}
		}

		if err := iter.Close(); err != nil {
			logger.Warn("address index migration: closing alias iterator", "error", err)
		}
	}

	return nil
}
