package handlers

import (
	"time"

	"github.com/bongo-project/bongo/internal/acmecert"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// ACMEStatusReader reports the stored ACME certificate's expiry for the
// dashboard. issued is false when none exists yet. It reads only.
type ACMEStatusReader interface {
	ACMEExpiry() (notAfter time.Time, issued bool, err error)
}

// SetACMEStatus wires the dashboard's certificate notice. Nil (ACME off)
// hides it.
func (h *AdminHandler) SetACMEStatus(r ACMEStatusReader) { h.acmeStatus = r }

// acmeNotice builds the dashboard notice. With ACME on, it warns when no
// certificate has been issued or the expiry is within 14 days.
func (h *AdminHandler) acmeNotice(now time.Time) templates.ACMENotice {
	if h.acmeStatus == nil {
		return templates.ACMENotice{}
	}
	notAfter, issued, err := h.acmeStatus.ACMEExpiry()
	if err != nil || !issued {
		// An unreadable record is as good as none: the operator must look.
		return templates.ACMENotice{Enabled: true, Warn: true}
	}
	return templates.ACMENotice{
		Enabled:  true,
		Issued:   true,
		NotAfter: notAfter.UTC().Format("2006-01-02"),
		Warn:     acmecert.ExpiryWarning(notAfter, now),
	}
}
