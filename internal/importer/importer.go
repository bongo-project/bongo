package importer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// msgIDEntry tracks the destination folder and accumulated tags for a
// message that has already been imported, keyed by Message-ID header value.
// It is used during dedup to merge additional tags from later-processed
// folders (e.g. "All Mail") into the already-imported message.
type msgIDEntry struct {
	dstFolder   string
	tags        []string // tags from first import
	pendingTags []string // additional tags discovered via dedup
}

// Config holds destination IMAP connection settings.
type Config struct {
	Host string
	Port int
	TLS  bool
	User string
	Pass string
}

// Importer manages the import process from a MessageSource to a destination IMAP server.
type Importer struct {
	cfg              Config
	state            *State
	folderMap        map[string]string
	exclude          map[string]bool
	dryRun           bool
	logger           *slog.Logger
	lastFolders      map[string]bool // folders to import last (e.g. Gmail All Mail)
	dedupByMessageID bool            // deduplicate by Message-ID (for Gmail All Mail)
}

// NewImporter creates a new Importer.
func NewImporter(cfg Config, state *State, logger *slog.Logger) *Importer {
	return &Importer{
		cfg:         cfg,
		state:       state,
		folderMap:   make(map[string]string),
		exclude:     make(map[string]bool),
		lastFolders: make(map[string]bool),
		logger:      logger,
	}
}

// SetFolderMap sets folder name mappings (source -> destination).
func (im *Importer) SetFolderMap(m map[string]string) {
	im.folderMap = m
}

// SetExclude sets folders to exclude from import.
func (im *Importer) SetExclude(folders []string) {
	for _, f := range folders {
		im.exclude[f] = true
	}
}

// SetDryRun enables dry-run mode (no actual writes).
func (im *Importer) SetDryRun(v bool) {
	im.dryRun = v
}

// SetGmailAutoMap configures Gmail folder mappings from IMAP special-use
// attributes. User-provided --folder-map and --exclude take precedence.
func (im *Importer) SetGmailAutoMap(attrs map[string][]imap.MailboxAttr) {
	autoMap, autoExclude, lastFolders := GmailFolderMap(attrs)

	// Merge auto-mappings (user overrides take precedence).
	for src, dst := range autoMap {
		if _, userSet := im.folderMap[src]; !userSet {
			im.folderMap[src] = dst
			im.logger.Info("gmail auto-map", "source", src, "dest", dst)
		}
	}

	// Merge auto-exclusions (user overrides take precedence).
	for _, folder := range autoExclude {
		if !im.exclude[folder] {
			im.exclude[folder] = true
			im.logger.Info("gmail auto-exclude", "folder", folder)
		}
	}

	// Mark folders to import last.
	for _, folder := range lastFolders {
		im.lastFolders[folder] = true
	}

	im.dedupByMessageID = true
}

// Import runs the import process.
func (im *Importer) Import(ctx context.Context, src MessageSource) error {
	importStart := time.Now()

	folders, err := src.Folders()
	if err != nil {
		return fmt.Errorf("listing source folders: %w", err)
	}

	im.logger.Info("source folders", "count", len(folders), "folders", folders)

	// Reorder: move lastFolders to the end for dedup.
	if len(im.lastFolders) > 0 {
		var normal, last []string
		for _, f := range folders {
			if im.lastFolders[f] {
				last = append(last, f)
			} else {
				normal = append(normal, f)
			}
		}
		folders = append(normal, last...)
		im.logger.Info("reordered folders for dedup", "last", last)
	}

	// Connect to destination.
	var dst *imapclient.Client
	if !im.dryRun {
		dst, err = im.connectDst()
		if err != nil {
			return err
		}
		defer dst.Close()
	}

	// Track Message-IDs for dedup when Gmail auto-map is active.
	// Maps Message-ID header value to the entry for the already-imported message.
	var msgIDTrack map[string]*msgIDEntry
	if im.dedupByMessageID {
		msgIDTrack = make(map[string]*msgIDEntry)
	}

	var totalImported, totalSkipped, totalDeduped int
	folderCount := 0

	for i, folder := range folders {
		if im.exclude[folder] {
			im.logger.Info("skipping excluded folder", "folder", folder)
			continue
		}
		folderCount++

		dstFolder := im.mapFolderName(folder)
		isLastFolder := im.lastFolders[folder]

		if folder != dstFolder {
			im.logger.Info("folder mapping applied", "source", folder, "dest", dstFolder)
		}

		im.logger.Info("importing folder",
			"num", fmt.Sprintf("%d/%d", i+1, len(folders)),
			"folder", folder,
			"dst_folder", dstFolder,
		)

		// Ensure destination folder exists.
		if dst != nil {
			createCmd := dst.Create(dstFolder, nil)
			// Ignore error if already exists.
			_ = createCmd.Wait()
		}

		folderStart := time.Now()
		fetchCtx, fetchCancel := context.WithCancel(ctx)
		msgCh, errCh := src.Messages(fetchCtx, folder)

		imported, skipped, deduped := 0, 0, 0
		checkpoint := 0

		var loopErr error
		for msg := range msgCh {
			select {
			case <-ctx.Done():
				loopErr = ctx.Err()
				break
			default:
			}
			if loopErr != nil {
				break
			}

			if im.state.IsImported(folder, msg.SourceID) {
				skipped++
				continue
			}

			// Message-ID dedup: record for normal folders, check for lastFolders.
			if msgIDTrack != nil {
				msgID := extractMessageID(msg.Raw)
				if msgID != "" {
					if isLastFolder {
						if entry, seen := msgIDTrack[msgID]; seen {
							// Duplicate: merge any new tags from this occurrence
							// into pending tags for later application via IMAP STORE.
							newTags := diffTags(msg.Tags, entry.tags, entry.pendingTags)
							if len(newTags) > 0 {
								entry.pendingTags = append(entry.pendingTags, newTags...)
								im.logger.Debug("dedup: queued tag merge",
									"message_id", msgID,
									"new_tags", newTags,
									"folder", dstFolder,
								)
							}
							deduped++
							continue
						}
					}
					msgIDTrack[msgID] = &msgIDEntry{
						dstFolder: dstFolder,
						tags:      deduplicateStrings(msg.Tags),
					}
				}
			}

			if im.dryRun {
				im.logger.Info("dry-run: would import",
					"folder", dstFolder,
					"source_id", msg.SourceID,
					"size", len(msg.Raw),
				)
				imported++
				continue
			}

			// APPEND to destination.
			appendOpts := &imap.AppendOptions{
				Flags: msg.Flags,
				Time:  msg.InternalDate,
			}

			appendCmd := dst.Append(dstFolder, int64(len(msg.Raw)), appendOpts)
			if _, err := appendCmd.Write(msg.Raw); err != nil {
				loopErr = fmt.Errorf("writing message to %s: %w", dstFolder, err)
				break
			}
			if err := appendCmd.Close(); err != nil {
				loopErr = fmt.Errorf("closing append to %s: %w", dstFolder, err)
				break
			}

			if _, err := appendCmd.Wait(); err != nil {
				loopErr = fmt.Errorf("appending to %s: %w", dstFolder, err)
				break
			}

			im.state.MarkImported(folder, msg.SourceID)
			imported++

			// Checkpoint every 100 messages.
			checkpoint++
			if checkpoint >= 100 {
				if err := im.state.Save(); err != nil {
					im.logger.Warn("failed to save state", "error", err)
				}
				checkpoint = 0
			}

			if imported%50 == 0 {
				im.logger.Info("progress",
					"folder", dstFolder,
					"imported", imported,
					"size", len(msg.Raw),
				)
			}
		}

		// Cancel the fetch goroutine and drain remaining messages to avoid deadlock.
		fetchCancel()
		for range msgCh {
		}

		if loopErr != nil {
			return loopErr
		}

		// Check for source errors.
		select {
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("reading %s: %w", folder, err)
			}
		default:
		}

		// Save state after each folder.
		if err := im.state.Save(); err != nil {
			im.logger.Warn("failed to save state", "error", err)
		}

		logAttrs := []any{
			"folder", dstFolder,
			"imported", imported,
			"skipped", skipped,
			"elapsed", FormatDuration(time.Since(folderStart)),
		}
		if deduped > 0 {
			logAttrs = append(logAttrs, "deduped", deduped)
		}
		im.logger.Info("folder complete", logAttrs...)

		totalImported += imported
		totalSkipped += skipped
		totalDeduped += deduped
	}

	// Apply pending tag merges: for each message that was deduped with
	// additional tags, SELECT its destination folder and STORE +FLAGS.
	if dst != nil && msgIDTrack != nil {
		merged := im.applyPendingTagMerges(ctx, dst, msgIDTrack)
		if merged > 0 {
			im.logger.Info("tag merges applied", "count", merged)
		}
	}

	logAttrs := []any{
		"folders", folderCount,
		"imported", totalImported,
		"skipped", totalSkipped,
		"elapsed", FormatDuration(time.Since(importStart)),
	}
	if totalDeduped > 0 {
		logAttrs = append(logAttrs, "deduped", totalDeduped)
	}
	im.logger.Info("import complete", logAttrs...)

	return nil
}

// applyPendingTagMerges applies accumulated pending tags to already-imported
// messages by selecting each destination folder, searching for the message by
// Message-ID header, then issuing IMAP STORE +FLAGS with the new keywords.
// Returns the number of messages successfully updated.
func (im *Importer) applyPendingTagMerges(ctx context.Context, dst *imapclient.Client, track map[string]*msgIDEntry) int {
	// Group pending merges by destination folder so we only SELECT each folder once.
	type pendingMerge struct {
		messageID   string
		pendingTags []string
	}
	byFolder := make(map[string][]pendingMerge)
	for msgID, entry := range track {
		if len(entry.pendingTags) == 0 {
			continue
		}
		byFolder[entry.dstFolder] = append(byFolder[entry.dstFolder], pendingMerge{
			messageID:   msgID,
			pendingTags: entry.pendingTags,
		})
	}

	if len(byFolder) == 0 {
		return 0
	}

	merged := 0
	for folder, merges := range byFolder {
		select {
		case <-ctx.Done():
			return merged
		default:
		}

		selCmd := dst.Select(folder, nil)
		selData, err := selCmd.Wait()
		if err != nil {
			im.logger.Warn("tag merge: SELECT failed", "folder", folder, "error", err)
			continue
		}
		if selData.NumMessages == 0 {
			continue
		}

		for _, m := range merges {
			select {
			case <-ctx.Done():
				return merged
			default:
			}

			// Search for the message by Message-ID header.
			criteria := &imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{
					{Key: "Message-Id", Value: m.messageID},
				},
			}
			searchCmd := dst.UIDSearch(criteria, nil)
			searchData, err := searchCmd.Wait()
			if err != nil {
				im.logger.Warn("tag merge: SEARCH failed",
					"message_id", m.messageID, "error", err)
				continue
			}
			uids := searchData.AllUIDs()
			if len(uids) == 0 {
				im.logger.Debug("tag merge: message not found in folder",
					"message_id", m.messageID, "folder", folder)
				continue
			}

			// Build $keyword flags for the pending tags.
			keywords := make([]imap.Flag, 0, len(m.pendingTags))
			for _, tag := range m.pendingTags {
				keywords = append(keywords, imap.Flag("$"+tag))
			}

			uidSet := imap.UIDSetNum(uids...)
			storeFlags := &imap.StoreFlags{
				Op:     imap.StoreFlagsAdd,
				Silent: true,
				Flags:  keywords,
			}
			storeCmd := dst.Store(uidSet, storeFlags, nil)
			// Drain the fetch response (STORE returns updated flags unless SILENT).
			for {
				msg := storeCmd.Next()
				if msg == nil {
					break
				}
				for {
					item := msg.Next()
					if item == nil {
						break
					}
				}
			}
			if err := storeCmd.Close(); err != nil {
				im.logger.Warn("tag merge: STORE failed",
					"message_id", m.messageID, "error", err)
				continue
			}

			im.logger.Info("tag merge: applied",
				"message_id", m.messageID,
				"folder", folder,
				"tags", m.pendingTags,
			)
			merged++
		}
	}

	return merged
}

func (im *Importer) connectDst() (*imapclient.Client, error) {
	addr := fmt.Sprintf("%s:%d", im.cfg.Host, im.cfg.Port)
	im.logger.Info("connecting to destination", "addr", addr)

	var c *imapclient.Client
	var err error

	if im.cfg.TLS {
		c, err = imapclient.DialTLS(addr, nil)
	} else {
		c, err = imapclient.DialInsecure(addr, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to destination %s: %w", addr, err)
	}

	loginCmd := c.Login(im.cfg.User, im.cfg.Pass)
	if err := loginCmd.Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("destination login failed: %w", err)
	}

	return c, nil
}

func (im *Importer) mapFolderName(folder string) string {
	if mapped, ok := im.folderMap[folder]; ok {
		return mapped
	}
	return folder
}

// FormatDuration formats a duration for display.
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}
