package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/importer"
)

var thunderbirdProfile string

var thunderbirdCmd = &cobra.Command{
	Use:   "thunderbird [local-folders-path]",
	Short: "Import from Thunderbird local folders",
	Long: `Auto-discovers Thunderbird's local folder structure and imports all folders.

If no path is given, auto-discovers the Thunderbird profile on the local system.
You can also pass the path to the "Mail/Local Folders" directory directly.

Examples:
  bongo-import thunderbird --dst-user admin@localhost --dst-pass secret
  bongo-import thunderbird ~/Library/Thunderbird/Profiles/abc.default-release/Mail/Local\ Folders --dst-user admin@localhost --dst-pass secret
  bongo-import thunderbird --profile ~/Library/Thunderbird/Profiles/abc.default-release --dst-user admin@localhost --dst-pass secret`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dstUser == "" || dstPass == "" {
			return fmt.Errorf("--dst-user and --dst-pass are required")
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

		var localFoldersDir string

		if len(args) == 1 {
			// Direct path to local folders directory
			localFoldersDir = args[0]
		} else if thunderbirdProfile != "" {
			// Profile directory specified
			dir, err := importer.FindThunderbirdLocalFolders(thunderbirdProfile)
			if err != nil {
				return err
			}
			localFoldersDir = dir
		} else {
			// Auto-discover
			profiles, err := importer.DiscoverThunderbirdProfiles()
			if err != nil {
				return err
			}

			// Try each profile until we find one with local folders
			for _, p := range profiles {
				dir, err := importer.FindThunderbirdLocalFolders(p)
				if err == nil {
					localFoldersDir = dir
					logger.Info("discovered Thunderbird local folders", "path", dir)
					break
				}
			}
			if localFoldersDir == "" {
				return fmt.Errorf("no Thunderbird profile with local folders found (tried %d profiles)", len(profiles))
			}
		}

		src, err := importer.NewThunderbirdSource(localFoldersDir)
		if err != nil {
			return err
		}
		defer src.Close()

		// List discovered folders
		folders, _ := src.Folders()
		logger.Info("discovered folders", "count", len(folders))
		for _, f := range folders {
			logger.Info("  folder", "name", f)
		}

		state, stateErr := importer.NewState(stateFile)
		if stateErr != nil {
			return stateErr
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
	thunderbirdCmd.Flags().StringVar(&thunderbirdProfile, "profile", "", "Path to Thunderbird profile directory")
}
