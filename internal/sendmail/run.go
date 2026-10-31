package sendmail

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"
)

// Env holds the seams tests replace.
type Env struct {
	Now     func() time.Time
	RootCAs *x509.CertPool
}

// IsInvokedAsSendmail reports whether argv0 names the sendmail link.
func IsInvokedAsSendmail(argv0 string) bool {
	return filepath.Base(argv0) == "sendmail"
}

// Run executes sendmail with args (without argv[0]) and returns the exit code.
// It never calls os.Exit and never writes the password anywhere.
func Run(args []string, stdin io.Reader, stderr io.Writer) int {
	return RunWith(args, stdin, stderr, Env{})
}

// RunWith is Run with replaceable dependencies.
func RunWith(args []string, stdin io.Reader, stderr io.Writer, env Env) int {
	if env.Now == nil {
		env.Now = time.Now
	}
	err := run(args, stdin, env)
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		fmt.Fprintf(stderr, "sendmail: %s\n", ee.Msg)
	} else {
		fmt.Fprintf(stderr, "sendmail: %v\n", err)
	}
	return exitCode(err)
}

func run(args []string, stdin io.Reader, env Env) error {
	opts, err := ParseArgs(args)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(opts.ConfigPath)
	if err != nil {
		return err
	}
	p, err := Prepare(stdin, opts, cfg, env.Now(), nil)
	if err != nil {
		return err
	}
	return (&Submitter{Config: cfg, RootCAs: env.RootCAs}).Submit(p)
}
