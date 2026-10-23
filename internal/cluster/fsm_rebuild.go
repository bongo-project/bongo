package cluster

import (
	"log/slog"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// rebuildDerivedIndexes scans all Pebble keys and rebuilds SQLite and Bleve indexes.
// Called after Restore() to ensure derived indexes are consistent with the primary store.
func rebuildDerivedIndexes(ps *store.PebbleStore, blobs *store.BlobStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger) error {
	// Rebuild user profiles into system.db.
	profileCount, err := rebuildProfiles(ps, metadata, search, logger)
	if err != nil {
		return err
	}

	// Rebuild mail indexes.
	mailCount, err := rebuildMail(ps, blobs, metadata, search, logger)
	if err != nil {
		return err
	}

	logger.Info("fsm: derived indexes rebuilt", "profiles", profileCount, "mails", mailCount)
	return nil
}

// forEachProfile calls fn for every parseable /user/{id}/profile record.
// Unreadable or unparseable records are logged and skipped.
func forEachProfile(ps *store.PebbleStore, logger *slog.Logger, fn func(userID string, profile *bongov1.UserProfile)) error {
	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return err
	}
	defer iter.Close()

	for iter.Next() {
		key := string(iter.Key())
		// Exactly /user/{id}/profile, as in runDerivedIndexes. A suffix match
		// would also take /user/{id}/tag/profile and
		// /user/{id}/bayes/{cat}/profile and index them as the user's profile.
		parts := strings.SplitN(key, "/", 5)
		if len(parts) != 4 || parts[3] != "profile" {
			continue
		}

		val, err := iter.Value()
		if err != nil {
			logger.Warn("fsm rebuild: error reading profile value", "key", key, "error", err)
			continue
		}

		userID := parts[2]

		var profile bongov1.UserProfile
		if err := proto.Unmarshal(val, &profile); err != nil {
			logger.Warn("fsm rebuild: failed to parse UserProfile", "user", userID, "error", err)
			continue
		}
		fn(userID, &profile)
	}
	return nil
}

// profileEnabled is the users.enabled value derived from a Pebble profile. The
// profile is the source of truth, so a stale row can never re-enable an account.
func profileEnabled(profile *bongov1.UserProfile) int {
	if profile.Disabled {
		return 0
	}
	return 1
}

func rebuildProfiles(ps *store.PebbleStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger) (int, error) {
	count := 0
	err := forEachProfile(ps, logger, func(userID string, profile *bongov1.UserProfile) {
		enabled := profileEnabled(profile)
		if metadata != nil && metadata.System != nil {
			_, err := metadata.System.DB().Exec(`
				INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
				VALUES (?, ?, ?, ?, unixepoch(), unixepoch())
				ON CONFLICT(id) DO UPDATE SET
					display_name = excluded.display_name,
				enabled = excluded.enabled,
					modified_at = unixepoch()
			`, userID, profile.Email, profile.DisplayName, enabled)
			if err != nil {
				logger.Warn("fsm rebuild: failed to update system.db user", "user", userID, "error", err)
			}
		}

		// Index in Bleve GAL.
		if search != nil {
			if err := search.IndexProfile(userID, profile.Email, profile.DisplayName); err != nil {
				logger.Warn("fsm rebuild: failed to index profile", "user", userID, "error", err)
			}
		}

		count++
	})
	return count, err
}

// ReconcileUserEnabled makes users.enabled in system.db agree with the Pebble
// profiles. FSM replay skips entries up to the persisted applied index, so a
// node that restarts without a snapshot would otherwise keep a stale
// enabled=1 row for an account disabled while the index was behind. It only
// UPDATEs existing rows (no inserts, no search work) and returns how many it
// changed. Call it before the Raft node starts so it cannot race FSM applies.
func ReconcileUserEnabled(ps *store.PebbleStore, metadata *store.MetadataManager, logger *slog.Logger) (int, error) {
	if metadata == nil || metadata.System == nil {
		return 0, nil
	}
	changed := 0
	err := forEachProfile(ps, logger, func(userID string, profile *bongov1.UserProfile) {
		enabled := profileEnabled(profile)
		res, err := metadata.System.DB().Exec(
			`UPDATE users SET enabled = ?, modified_at = unixepoch() WHERE id = ? AND enabled <> ?`,
			enabled, userID, enabled)
		if err != nil {
			logger.Warn("fsm reconcile: failed to update users.enabled", "user", userID, "error", err)
			return
		}
		if n, err := res.RowsAffected(); err == nil {
			changed += int(n)
		}
	})
	return changed, err
}

func rebuildMail(ps *store.PebbleStore, blobs *store.BlobStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger) (int, error) {
	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	count := 0
	for iter.Next() {
		key := string(iter.Key())
		parts := strings.SplitN(key, "/", 5) // ["", "user", "{id}", "mail", "{guid}"]
		if len(parts) < 5 || parts[3] != "mail" {
			continue
		}
		userID := parts[2]
		guid := parts[4]

		val, err := iter.Value()
		if err != nil {
			logger.Warn("fsm rebuild: error reading mail value", "key", key, "error", err)
			continue
		}

		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			logger.Warn("fsm rebuild: failed to parse MailMeta", "user", userID, "guid", guid, "error", err)
			continue
		}

		// Index into per-user SQLite.
		if metadata != nil {
			if err := metadata.IndexMail(userID, guid, meta.Subject, meta.From, meta.ReceivedAt, meta.GetRawMessage().GetSize()); err != nil {
				logger.Warn("fsm rebuild: failed to index mail in sqlite", "user", userID, "guid", guid, "error", err)
			}
		}

		// Index into Bleve.
		if search != nil {
			hasAttachment := len(meta.Attachments) > 0
			// Same body extraction as the live indexer, so a rebuilt index is
			// equivalent to one built incrementally. Without this a restore
			// silently produced an index that could not answer body searches.
			body := mailBodyText(blobs, &meta, logger)
			if err := search.IndexMail(userID, guid, meta.Subject, meta.From, meta.To, body, meta.ReceivedAt, hasAttachment, meta.Tags); err != nil {
				logger.Warn("fsm rebuild: failed to index mail in bleve", "user", userID, "guid", guid, "error", err)
			}
		}

		count++
	}

	return count, nil
}
