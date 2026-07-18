package store

import (
	"log/slog"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// PopulateAddressIndex seeds the global address index (AddressStore) from
// existing Pebble data. It is the migration routine for FR-12 / US-RECV-2.
//
// Phase 1 — primary records:
//
//	Scans /user/*/profile keys in Pebble, unmarshals each UserProfile, and
//	creates a "primary" AddressRecord (kind=primary, can_send=true,
//	verified=true) owned by the user whose ID is embedded in the key.
//	A map[loweredEmail → userID] is built as a side effect and reused in
//	phase 2 for alias resolution.
//
// Phase 2 — alias records:
//
//	Scans /system/alias/{addr} keys in Pebble. Each value is the target
//	user's username (== their email). The username is resolved to a user_id
//	via the phase-1 map; if not found there, falls back to the SystemIndex
//	users table. Aliases with an unresolvable target are logged and skipped.
//
// Idempotency: AddressStore.Put is an upsert, so re-running the migration is
// safe and produces identical records. It is additive — it never deletes or
// modifies existing users, aliases, profiles, or mailboxes.
//
// CAUTION: This function performs blind upserts on every run. Once address
// records become user-editable (tasks 4.4/4.5, e.g. verified/can_send toggles),
// this MUST become create-only (Get-before-Put) to avoid clobbering user edits.
// Add that guard before exposing the edit UI.
//
// This function is intended to be called on the Raft leader at startup only.
// Leadership changes after boot will NOT re-trigger the migration — the same
// limitation as ensureConfiguredDomain.
//
// logger may be nil; slog.Default() is used in that case.
func PopulateAddressIndex(ps *PebbleStore, as *AddressStore, sys *SystemIndex, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	// --- Phase 1: enumerate /user/*/profile to create primary records. ---

	// emailToUserID maps a lowercased user email to the Pebble user_id.
	// Built here and reused in phase 2 to resolve alias targets.
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

			emailToUserID[email] = userID

			// NOTE: blind upsert — idempotent now (only migration writes address records).
			// Must become Get-before-Put once tasks 4.4/4.5 allow users to edit these fields.
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

	// --- Phase 2: enumerate /system/alias/{addr} to create alias records. ---

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

			// Primary resolution: profile map built in phase 1.
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
