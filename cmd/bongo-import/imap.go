package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bongo-go/bongo-go/internal/importer"
)

var (
	srcHost    string
	srcPort    int
	srcTLS     bool
	srcUser    string
	srcPass    string
	folderMaps []string
	excludes   []string
	gmailMode  bool
)

var imapCmd = &cobra.Command{
	Use:   "imap",
	Short: "Import from another IMAP server",
	Long:  `Connects to a source IMAP server and replicates all folders and messages to the destination.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if dstUser == "" || dstPass == "" {
			return fmt.Errorf("--dst-user and --dst-pass are required")
		}
		if srcUser == "" || srcPass == "" {
			return fmt.Errorf("--src-user and --src-pass are required")
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

		logger.Info("connecting to source", "host", srcHost, "port", srcPort)
		src, err := importer.NewIMAPSource(srcHost, srcPort, srcTLS, srcUser, srcPass, logger)
		if err != nil {
			return fmt.Errorf("connecting to source: %w", err)
		}
		defer src.Close()

		state, err := importer.NewState(stateFile)
		if err != nil {
			return err
		}

		im := importer.NewImporter(importer.Config{
			Host: dstHost,
			Port: dstPort,
			TLS:  dstTLS,
			User: dstUser,
			Pass: dstPass,
		}, state, logger)

		im.SetDryRun(dryRun)

		// Parse folder mappings (user-provided take precedence over auto-map).
		if len(folderMaps) > 0 {
			m := make(map[string]string)
			for _, mapping := range folderMaps {
				parts := strings.SplitN(mapping, "=", 2)
				if len(parts) == 2 {
					m[parts[0]] = parts[1]
				}
			}
			im.SetFolderMap(m)
		}

		im.SetExclude(excludes)

		// Auto-detect Gmail or use --gmail flag.
		isGmail := gmailMode ||
			strings.Contains(strings.ToLower(srcHost), "gmail.com") ||
			strings.Contains(strings.ToLower(srcHost), "googlemail.com")

		if isGmail {
			logger.Info("gmail mode enabled", "host", srcHost, "auto_detected", !gmailMode)

			// Folders() must be called first to populate special-use attrs.
			if _, err := src.Folders(); err != nil {
				return fmt.Errorf("listing folders for gmail detection: %w", err)
			}
			im.SetGmailAutoMap(src.FolderAttrs())
		}

		return im.Import(context.Background(), src)
	},
}

func init() {
	f := imapCmd.Flags()
	f.StringVar(&srcHost, "src-host", "", "Source IMAP hostname (required)")
	f.IntVar(&srcPort, "src-port", 993, "Source IMAP port")
	f.BoolVar(&srcTLS, "src-tls", true, "Use TLS for source")
	f.StringVar(&srcUser, "src-user", "", "Source username (required)")
	f.StringVar(&srcPass, "src-pass", "", "Source password (required)")
	f.StringArrayVar(&folderMaps, "folder-map", nil, `Folder name mapping (e.g. "Gmail/Sent Mail=Sent")`)
	f.StringArrayVar(&excludes, "exclude", nil, `Folders to exclude (e.g. "[Gmail]/All Mail")`)
	f.BoolVar(&gmailMode, "gmail", false, "Force Gmail mode (auto-detected for gmail.com/googlemail.com hosts)")
}
