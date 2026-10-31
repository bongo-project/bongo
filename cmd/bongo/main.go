package main

import (
	"os"

	"github.com/bongo-project/bongo/internal/sendmail"
)

func main() {
	// Invoked through a link named sendmail: skip cobra, whose flag parsing
	// cannot handle sendmail's flags.
	if sendmail.IsInvokedAsSendmail(os.Args[0]) {
		os.Exit(sendmail.Run(os.Args[1:], os.Stdin, os.Stderr))
	}
	Execute()
}
