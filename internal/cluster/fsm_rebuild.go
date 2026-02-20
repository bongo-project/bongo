package cluster

import (
	"log/slog"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/store"
)

// rebuildDerivedIndexes scans all Pebble keys and rebuilds SQLite and Bleve indexes.
// Called after Restore() to ensure derived indexes are consistent with the primary store.
func rebuildDerivedIndexes(ps *store.PebbleStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger) error {
	// Rebuild user profiles into system.db.
	profileCount, err := rebuildProfiles(ps, metadata, search, logger)
	if err != nil {
		return err
	}

	// Rebuild mail indexes.
	mailCount, err := rebuildMail(ps, metadata, search, logger)
	if err != nil {
		return err
	}

	logger.Info("fsm: derived indexes rebuilt", "profiles", profileCount, "mails", mailCount)
	return nil
}

func rebuildProfiles(ps *store.PebbleStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger) (int, error) {
	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	count := 0
	for iter.Next() {
		key := string(iter.Key())
		if !strings.HasSuffix(key, "/profile") {
			continue
		}

		val, err := iter.Value()
		if err != nil {
			logger.Warn("fsm rebuild: error reading profile value", "key", key, "error", err)
			continue
		}

		// Extract user ID: /user/{id}/profile
		parts := strings.SplitN(key, "/", 5)
		if len(parts) < 4 {
			continue
		}
		userID := parts[2]

		var profile bongov1.UserProfile
		if err := proto.Unmarshal(val, &profile); err != nil {
			logger.Warn("fsm rebuild: failed to parse UserProfile", "user", userID, "error", err)
			continue
		}

		// Insert into system.db.
		if metadata != nil && metadata.System != nil {
			_, err := metadata.System.DB().Exec(`
				INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
				VALUES (?, ?, ?, 1, unixepoch(), unixepoch())
				ON CONFLICT(id) DO UPDATE SET
					display_name = excluded.display_name,
					modified_at = unixepoch()
			`, userID, profile.Email, profile.DisplayName)
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
	}

	return count, nil
}

func rebuildMail(ps *store.PebbleStore, metadata *store.MetadataManager, search SearchIndexer, logger *slog.Logger) (int, error) {
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
			if err := search.IndexMail(userID, guid, meta.Subject, meta.From, meta.To, "", meta.ReceivedAt, hasAttachment); err != nil {
				logger.Warn("fsm rebuild: failed to index mail in bleve", "user", userID, "guid", guid, "error", err)
			}
		}

		count++
	}

	return count, nil
}
