// Package sendmail implements "bongo sendmail": a drop-in for the classic
// sendmail submission interface that hands the message to a Bongo server as an
// authenticated SMTP submission client (port 465 implicit TLS, or 587 with
// mandatory STARTTLS). Cron, PHP's sendmail_path and similar callers use it.
package sendmail

import (
	"errors"
	"fmt"
)

// Exit codes follow sysexits.h.
const (
	ExitOK          = 0
	ExitUsage       = 64
	ExitDataErr     = 65
	ExitNoUser      = 67
	ExitUnavailable = 69
	ExitTempFail    = 75
	ExitNoPerm      = 77
	ExitConfig      = 78
)

// ExitError carries a sysexits code with the message shown on stderr. The
// message must never contain the account password.
type ExitError struct {
	Code int
	Msg  string
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *ExitError) Unwrap() error { return e.Err }

func usageErr(format string, a ...any) error {
	return &ExitError{Code: ExitUsage, Msg: fmt.Sprintf(format, a...)}
}

func dataErr(format string, a ...any) error {
	return &ExitError{Code: ExitDataErr, Msg: fmt.Sprintf(format, a...)}
}

func configErr(format string, a ...any) error {
	return &ExitError{Code: ExitConfig, Msg: fmt.Sprintf(format, a...)}
}

// exitCode returns the sysexits code for err.
func exitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitUnavailable
}
