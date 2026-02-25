package main

import (
	"github.com/spf13/cobra"
)

var (
	dstHost string
	dstPort int
	dstTLS  bool
	dstUser string
	dstPass string
	stateFile string
	dryRun    bool
)

var rootCmd = &cobra.Command{
	Use:   "bongo-import",
	Short: "Import email into bongo-go from various sources",
	Long: `bongo-import migrates email into a bongo-go server via IMAP APPEND.

Supported sources:
  imap         Import from another IMAP server
  mbox         Import from mbox files
  maildir      Import from Maildir directories
  eml          Import from individual .eml files
  thunderbird  Auto-discover and import Thunderbird local folders
  directory    Recursively scan a directory for mail, contacts, and calendar files

All messages are written to the destination via IMAP APPEND, preserving
flags, folders, and internal dates.`,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&dstHost, "dst-host", "localhost", "Destination IMAP hostname")
	pf.IntVar(&dstPort, "dst-port", 1143, "Destination IMAP port")
	pf.BoolVar(&dstTLS, "dst-tls", false, "Use TLS for destination")
	pf.StringVar(&dstUser, "dst-user", "", "Destination username (required)")
	pf.StringVar(&dstPass, "dst-pass", "", "Destination password (required)")
	pf.StringVar(&stateFile, "state", "", "Path to JSON state file for resume")
	pf.BoolVar(&dryRun, "dry-run", false, "List what would be imported without writing")

	rootCmd.AddCommand(imapCmd)
	rootCmd.AddCommand(mboxCmd)
	rootCmd.AddCommand(maildirCmd)
	rootCmd.AddCommand(emlCmd)
	rootCmd.AddCommand(thunderbirdCmd)
	rootCmd.AddCommand(directoryCmd)
}
