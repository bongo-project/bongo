package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/importer"
)

var emlFolder string

var emlCmd = &cobra.Command{
	Use:   "eml [files...]",
	Short: "Import individual .eml files",
	Long:  `Reads one or more .eml files and imports each via IMAP APPEND.`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dstUser == "" || dstPass == "" {
			return fmt.Errorf("--dst-user and --dst-pass are required")
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

		src := importer.NewEMLSource(args, emlFolder)
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

func init() {
	emlCmd.Flags().StringVar(&emlFolder, "folder", "INBOX", "Target mailbox for imported messages")
}
