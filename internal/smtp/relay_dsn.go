package smtp

import (
	"errors"
	"fmt"
	"strings"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// maxENVIDLen is the RFC 3461 section 4.4 limit on an ENVID value.
const maxENVIDLen = 100

// envelopeDSN is the DSN part of a queued message's envelope (RFC 3461) in the
// form the SMTP client sends it. It is built once per delivery and only read
// afterwards.
//
// Values are cleaned here, not trusted: go-smtp refuses a malformed parameter
// with a client-side error that would surface as a temporary failure and loop
// the retries until the message expired. One bad stored parameter must never
// block delivery, so it is omitted instead.
type envelopeDSN struct {
	ret   gosmtp.DSNReturn
	envid string
	rcpts map[string]rcptDSN // keyed by lower-cased address
}

type rcptDSN struct {
	notify uint32
	typ    gosmtp.DSNAddressType
	orcpt  string
}

// newEnvelopeDSN reads msg's DSN fields. It returns nil when msg carries none,
// which sendToHostOpts treats as plain delivery.
func newEnvelopeDSN(msg *bongov1.OutboundMessage) *envelopeDSN {
	if msg == nil {
		return nil
	}
	env := &envelopeDSN{rcpts: make(map[string]rcptDSN, len(msg.GetRecipientDsn()))}
	switch strings.ToUpper(msg.GetDsnRet()) {
	case "FULL":
		env.ret = gosmtp.DSNReturnFull
	case "HDRS":
		env.ret = gosmtp.DSNReturnHeaders
	}
	if id := msg.GetDsnEnvid(); id != "" && len(id) <= maxENVIDLen && printableASCII(id) {
		env.envid = id
	}
	for _, r := range msg.GetRecipientDsn() {
		key := strings.ToLower(strings.TrimSpace(r.GetAddress()))
		if key == "" {
			continue
		}
		d := rcptDSN{notify: r.GetNotify()}
		switch strings.ToLower(r.GetOrcptType()) {
		case "rfc822":
			if printableASCII(r.GetOrcpt()) {
				d.typ, d.orcpt = gosmtp.DSNAddressTypeRFC822, r.GetOrcpt()
			}
		case "utf-8":
			if !hasControl(r.GetOrcpt()) {
				d.typ, d.orcpt = gosmtp.DSNAddressTypeUTF8, r.GetOrcpt()
			}
		}
		env.rcpts[key] = d
	}
	return env
}

// mailParams returns the RET and ENVID for the MAIL command.
func (e *envelopeDSN) mailParams() (gosmtp.DSNReturn, string) {
	return e.ret, e.envid
}

// rcptOptions returns the NOTIFY and ORCPT options for rcpt, or nil when it
// has none. NEVER travels alone: RFC 3461 forbids combining it with other
// values.
func (e *envelopeDSN) rcptOptions(rcpt string) *gosmtp.RcptOptions {
	d, ok := e.rcpts[strings.ToLower(strings.TrimSpace(rcpt))]
	if !ok {
		return nil
	}
	opts := &gosmtp.RcptOptions{}
	switch {
	case d.notify&mail.NotifyNever != 0:
		opts.Notify = []gosmtp.DSNNotify{gosmtp.DSNNotifyNever}
	default:
		if d.notify&mail.NotifySuccess != 0 {
			opts.Notify = append(opts.Notify, gosmtp.DSNNotifySuccess)
		}
		if d.notify&mail.NotifyFailure != 0 {
			opts.Notify = append(opts.Notify, gosmtp.DSNNotifyFailure)
		}
		if d.notify&mail.NotifyDelay != 0 {
			opts.Notify = append(opts.Notify, gosmtp.DSNNotifyDelayed)
		}
	}
	opts.OriginalRecipientType, opts.OriginalRecipient = d.typ, d.orcpt
	if len(opts.Notify) == 0 && opts.OriginalRecipient == "" {
		return nil
	}
	return opts
}

// wantsSuccess returns the addresses among accepted whose NOTIFY includes
// SUCCESS. NEVER overrides it.
func (e *envelopeDSN) wantsSuccess(accepted []string) []string {
	var out []string
	for _, a := range accepted {
		d := e.rcpts[strings.ToLower(strings.TrimSpace(a))]
		if d.notify&mail.NotifyNever == 0 && mail.NotifyWantsSuccess(d.notify) {
			out = append(out, a)
		}
	}
	return out
}

func printableASCII(s string) bool {
	for _, r := range s {
		if r < ' ' || r > '~' {
			return false
		}
	}
	return true
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			return true
		}
	}
	return false
}

// replyError keeps the wire form of a remote reply. go-smtp moves a leading
// RFC 3463 enhanced code out of the message and its Error() prints only
// "SMTP error 451: ...", which would lose the specific status the bounce and
// delay reports parse from the stored reason. Unwrap returns the *SMTPError
// so errors.As still finds the reply code.
type replyError struct{ se *gosmtp.SMTPError }

func (e *replyError) Error() string {
	ec := e.se.EnhancedCode
	if ec != gosmtp.NoEnhancedCode {
		return fmt.Sprintf("%03d %d.%d.%d %s", e.se.Code, ec[0], ec[1], ec[2], e.se.Message)
	}
	return fmt.Sprintf("%03d %s", e.se.Code, e.se.Message)
}

func (e *replyError) Unwrap() error { return e.se }

// wireReply wraps a go-smtp *SMTPError (anywhere in err's chain) as a
// replyError; other errors pass through unchanged.
func wireReply(err error) error {
	var se *gosmtp.SMTPError
	if err != nil && errors.As(err, &se) {
		return &replyError{se: se}
	}
	return err
}
