package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "bongo-go",
	Short: "Bongo mail and calendar server",
	Long:  "A mail and calendar server (IMAP, SMTP, POP3, CalDAV/CardDAV) with an HTMX web UI.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
