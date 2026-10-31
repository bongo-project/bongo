package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/sendmail"
)

// sendmailCmd is "bongo sendmail". Flag parsing is disabled because sendmail's
// flags (-oi, -FCronDaemon, -B8BITMIME) are not POSIX-style; the arguments go
// to internal/sendmail untouched. The sysexits code is the process exit status,
// so it exits here instead of returning an error to Execute (which uses 1).
var sendmailCmd = &cobra.Command{
	Use:                "sendmail [flags] [recipient...]",
	Short:              "Submit a message from stdin to the local server (sendmail-compatible)",
	Long:               "Reads a message on stdin and submits it over SMTP using the account in /etc/bongo/sendmail.conf (or --config). Also runs when the binary is invoked through a link named sendmail.",
	DisableFlagParsing: true,
	SilenceUsage:       true,
	SilenceErrors:      true,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(sendmail.Run(args, os.Stdin, os.Stderr))
	},
}

func init() {
	rootCmd.AddCommand(sendmailCmd)
}
