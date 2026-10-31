package sendmail

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
)

// phase is the step of the SMTP conversation an error came from. The same
// reply maps to different exit codes by phase: "550 5.7.1" is "relaying denied"
// at RCPT (recipient refused) but "From not owned" at DATA (permission).
type phase int

const (
	phaseConnect phase = iota // dial, TLS, greeting, EHLO
	phaseAuth
	phaseMail
	phaseRcpt
	phaseData
)

// Submitter sends prepared messages through a Bongo submission listener.
type Submitter struct {
	Config *Config
	// RootCAs replaces the system pool when set (tests).
	RootCAs *x509.CertPool
}

// Submit connects, authenticates and sends p. The password is only ever given
// to the SASL client; no debug writer is attached to the connection, so it
// cannot appear in any output. If any recipient is refused the transaction is
// abandoned and nothing is delivered.
func (s *Submitter) Submit(p *Prepared) error {
	cfg := s.Config
	tlsCfg := &tls.Config{
		ServerName:         strings.Trim(cfg.Host, "[]"),
		MinVersion:         tls.VersionTLS12,
		RootCAs:            s.RootCAs,
		InsecureSkipVerify: cfg.InsecureSkipVerify, // #nosec G402 -- loopback only, enforced by Config.validate
	}
	addr := net.JoinHostPort(strings.Trim(cfg.Host, "[]"), strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: 30 * time.Second}

	var c *gosmtp.Client
	if cfg.TLS == "implicit" {
		conn, err := (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).Dial("tcp", addr)
		if err != nil {
			return failure(phaseConnect, err)
		}
		c = gosmtp.NewClient(conn)
	} else {
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return failure(phaseConnect, err)
		}
		// Fails, rather than falling back to plaintext, when STARTTLS is not
		// offered. AUTH is never sent over an unencrypted connection.
		if c, err = gosmtp.NewClientStartTLS(conn, tlsCfg); err != nil {
			_ = conn.Close()
			return failure(phaseConnect, err)
		}
	}
	defer c.Close()

	if err := c.Hello(helloName()); err != nil {
		return failure(phaseConnect, err)
	}
	if ok, _ := c.Extension("AUTH"); !ok {
		return &ExitError{Code: ExitUnavailable, Msg: "server does not offer authentication"}
	}
	if err := c.Auth(sasl.NewPlainClient("", cfg.Username, cfg.Secret)); err != nil {
		return failure(phaseAuth, err)
	}
	if err := c.Mail(p.Sender, nil); err != nil {
		return failure(phaseMail, err)
	}
	for _, r := range p.Rcpts {
		if err := c.Rcpt(r, nil); err != nil {
			// Abort so nothing is delivered to the recipients accepted so far.
			// Best-effort: the exit code reports the refusal either way.
			_ = c.Reset()
			_ = c.Quit()
			return &ExitError{Code: classify(phaseRcpt, err), Msg: "recipient " + r + " refused: " + describe(err)}
		}
	}
	w, err := c.Data()
	if err != nil {
		return failure(phaseData, err)
	}
	// The data writer is a textproto.DotWriter: it dot-stuffs and converts any
	// bare LF itself, so p.Data must not be stuffed here.
	if _, err := w.Write(p.Data); err != nil {
		_ = w.Close()
		return failure(phaseData, err)
	}
	if err := w.Close(); err != nil {
		return failure(phaseData, err)
	}
	_ = c.Quit() // the message is accepted; a failed QUIT changes nothing
	return nil
}

func helloName() string {
	h, err := os.Hostname()
	if err != nil || h == "" || strings.ContainsAny(h, " \r\n") {
		return "localhost"
	}
	return h
}

// failure wraps err with the sysexits code for the phase and a message that
// carries the server's reply text.
func failure(ph phase, err error) error {
	return &ExitError{Code: classify(ph, err), Msg: describe(err)}
}

func describe(err error) string {
	var se *gosmtp.SMTPError
	if errors.As(err, &se) {
		msg := strings.ReplaceAll(se.Message, "\n", " ")
		if se.EnhancedCode != (gosmtp.EnhancedCode{}) {
			return fmt.Sprintf("server replied %d %d.%d.%d %s", se.Code, se.EnhancedCode[0], se.EnhancedCode[1], se.EnhancedCode[2], msg)
		}
		return fmt.Sprintf("server replied %d %s", se.Code, msg)
	}
	return err.Error()
}

// classify maps a failure in a phase to a sysexits code.
func classify(ph phase, err error) int {
	var se *gosmtp.SMTPError
	if !errors.As(err, &se) {
		// Not a server reply: DNS, refused connection, TLS verification, a
		// missing STARTTLS, or a dropped connection.
		if ph == phaseConnect {
			return ExitUnavailable
		}
		return ExitTempFail
	}
	switch {
	case se.Code == 421:
		return ExitUnavailable
	case se.Code >= 400 && se.Code < 500:
		return ExitTempFail
	}
	perm := se.EnhancedCode[0] == 5 && se.EnhancedCode[1] == 7 // 5.7.x policy
	switch ph {
	case phaseAuth:
		return ExitNoPerm
	case phaseRcpt:
		return ExitNoUser
	case phaseMail:
		switch {
		case perm || se.Code == 553 || se.Code == 550:
			return ExitNoPerm
		case se.Code == 552 || se.Code == 501 || se.Code == 555:
			return ExitDataErr
		}
	case phaseData:
		switch {
		case perm:
			return ExitNoPerm
		case se.Code == 501 || (se.Code == 550 && se.EnhancedCode[1] == 6):
			return ExitDataErr // malformed header
		case se.Code == 552 && se.EnhancedCode == (gosmtp.EnhancedCode{5, 3, 4}):
			return ExitDataErr // message or header too large
		case se.Code == 552 && se.EnhancedCode == (gosmtp.EnhancedCode{}):
			return ExitDataErr // too large, server without enhanced codes
		}
	}
	return ExitUnavailable
}
