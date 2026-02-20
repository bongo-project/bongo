package importer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

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
	cfg       Config
	state     *State
	folderMap map[string]string
	exclude   map[string]bool
	dryRun    bool
	logger    *slog.Logger
}

// NewImporter creates a new Importer.
func NewImporter(cfg Config, state *State, logger *slog.Logger) *Importer {
	return &Importer{
		cfg:       cfg,
		state:     state,
		folderMap: make(map[string]string),
		exclude:   make(map[string]bool),
		logger:    logger,
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

// Import runs the import process.
func (im *Importer) Import(ctx context.Context, src MessageSource) error {
	folders, err := src.Folders()
	if err != nil {
		return fmt.Errorf("listing source folders: %w", err)
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

	var totalImported, totalSkipped int
	folderCount := 0

	for i, folder := range folders {
		if im.exclude[folder] {
			im.logger.Info("skipping excluded folder", "folder", folder)
			continue
		}
		folderCount++

		dstFolder := im.mapFolderName(folder)

		im.logger.Info("importing folder",
			"num", fmt.Sprintf("%d/%d", i+1, len(folders)),
			"folder", folder,
			"dst_folder", dstFolder,
		)

		// Ensure destination folder exists.
		if dst != nil {
			createCmd := dst.Create(dstFolder, nil)
			// Ignore error if already exists.
			createCmd.Wait()
		}

		msgCh, errCh := src.Messages(folder)

		imported, skipped := 0, 0
		checkpoint := 0

		for msg := range msgCh {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if im.state.IsImported(folder, msg.SourceID) {
				skipped++
				continue
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
				return fmt.Errorf("writing message to %s: %w", dstFolder, err)
			}
			if err := appendCmd.Close(); err != nil {
				return fmt.Errorf("closing append to %s: %w", dstFolder, err)
			}

			if _, err := appendCmd.Wait(); err != nil {
				return fmt.Errorf("appending to %s: %w", dstFolder, err)
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
				)
			}
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

		im.logger.Info("folder complete",
			"folder", dstFolder,
			"imported", imported,
			"skipped", skipped,
		)

		totalImported += imported
		totalSkipped += skipped
	}

	im.logger.Info("import complete",
		"folders", folderCount,
		"imported", totalImported,
		"skipped", totalSkipped,
	)

	return nil
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
