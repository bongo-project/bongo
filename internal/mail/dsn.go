package mail

import (
	"fmt"
	"strings"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// NOTIFY bits (RFC 3461 section 4.1). A mask of 0 means the sender gave no
// NOTIFY, which is not the same as any of the bits: it keeps the behaviour
// from before DSN support (a failure report, no delay or success report).
const (
	NotifySuccess uint32 = 1 << iota
	NotifyFailure
	NotifyDelay
	NotifyNever
)

// NotifyWantsFailure reports whether a failure report is wanted. NOTIFY=NEVER
// and any NOTIFY list without FAILURE (SUCCESS alone, DELAY alone) mean none
// (RFC 3461 section 4.1); no NOTIFY at all keeps the default report.
func NotifyWantsFailure(mask uint32) bool {
	return mask == 0 || mask&NotifyFailure != 0
}

// NotifyWantsDelay reports whether a delay report was asked for.
func NotifyWantsDelay(mask uint32) bool { return mask&NotifyDelay != 0 }

// NotifyWantsSuccess reports whether a delivery report was asked for.
func NotifyWantsSuccess(mask uint32) bool { return mask&NotifySuccess != 0 }

// recipientDSN returns the DSN record for addr (compared case-insensitively),
// or nil. It is looked up by address because OutboundMessage.Recipients is
// pruned as addresses fail, so a parallel list would shift.
func recipientDSN(msg *bongov1.OutboundMessage, addr string) *bongov1.RecipientDSN {
	addr = strings.ToLower(strings.TrimSpace(addr))
	for _, r := range msg.GetRecipientDsn() {
		if strings.ToLower(strings.TrimSpace(r.GetAddress())) == addr {
			return r
		}
	}
	return nil
}

// RecipientNotify returns addr's NOTIFY mask on msg; 0 when none was given.
func RecipientNotify(msg *bongov1.OutboundMessage, addr string) uint32 {
	return recipientDSN(msg, addr).GetNotify()
}

// orcptField formats an Original-Recipient value for a 7bit
// message/delivery-status part. RFC 3464 section 2.3.1 carries the decoded
// address, not the xtext form used in SMTP. A UTF-8 address is written in the
// RFC 6533 utf-8-addr-xtext form, because the part has to stay 7bit. Anything
// that is still not printable ASCII is dropped.
func orcptField(r *bongov1.RecipientDSN) string {
	if r == nil || r.GetOrcpt() == "" {
		return ""
	}
	typ := strings.ToLower(r.GetOrcptType())
	addr := r.GetOrcpt()
	switch typ {
	case "rfc822":
	case "utf-8":
		addr = utf8AddrXtext(addr)
	default:
		return ""
	}
	addr = SanitizeHeaderValue(addr)
	if addr == "" || !isASCII(addr) {
		return ""
	}
	return typ + "; " + addr
}

// utf8AddrXtext encodes addr per RFC 6533 section 3: characters outside QCHAR
// (and '+', '=', '\') become \x{HEX}.
func utf8AddrXtext(addr string) string {
	var b strings.Builder
	for _, r := range addr {
		if r >= 33 && r <= 126 && r != '+' && r != '=' && r != '\\' {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "\\x{%X}", r)
	}
	return b.String()
}
