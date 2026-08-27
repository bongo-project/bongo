package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/importer"
)

var maildirCmd = &cobra.Command{
	Use:   "maildir <path>",
	Short: "Import from a Maildir directory",
	Long: `Reads a Maildir directory (with cur/, new/, tmp/ subdirectories) and imports
all messages via IMAP APPEND. Maildir++ subfolders (.Sent, .Drafts, etc.)
are auto-discovered. Flags are mapped from filename suffixes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dstUser == "" || dstPass == "" {
			return fmt.Errorf("--dst-user and --dst-pass are required")
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

		src := importer.NewMaildirSource(args[0])
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

		return im.Import(context.Background(), src)
	},
}
