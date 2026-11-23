package fetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"

	"github.com/bongo-project/bongo/internal/fetch/credcrypt"
	"github.com/bongo-project/bongo/internal/fetch/fetchx"
	"github.com/bongo-project/bongo/internal/netx"
)

// The error sentinels, re-exported from fetchx so callers import one package.
var (
	ErrAuth      = fetchx.ErrAuth
	ErrTLS       = fetchx.ErrTLS
	ErrFolder    = fetchx.ErrFolder
	ErrNoUIDL    = fetchx.ErrNoUIDL
	ErrProtocol  = fetchx.ErrProtocol
	ErrTransient = fetchx.ErrTransient
	ErrTooLarge  = fetchx.ErrTooLarge
	ErrBudget    = fetchx.ErrBudget
	ErrTimeout   = fetchx.ErrTimeout
	ErrConnect   = fetchx.ErrConnect

	// ErrQuota is the worker's code for a recipient over quota; it is defined
	// here so Code and the UI share one list of codes.
	ErrQuota = errors.New("over quota")
)

// The state and UI codes. A code is all that is ever stored, logged or shown
// about a failure.
const (
	CodeConnectFailed  = "connect_failed"
	CodeTLSFailed      = "tls_failed"
	CodeAuthFailed     = "auth_failed"
	CodeFolderMissing  = "folder_missing"
	CodeNoUIDL         = "no_uidl"
	CodeProtocolError  = "protocol_error"
	CodeTooLarge       = "too_large"
	CodeQuotaExceeded  = "quota_exceeded"
	CodeNeedsPassword  = "needs_password"
	CodeKeyUnavailable = "key_unavailable"
	CodeInternal       = "internal"
)

// Codes lists every code, for tests and the UI's message table.
var Codes = []string{
	CodeConnectFailed, CodeTLSFailed, CodeAuthFailed, CodeFolderMissing, CodeNoUIDL,
	CodeProtocolError, CodeTooLarge, CodeQuotaExceeded, CodeNeedsPassword,
	CodeKeyUnavailable, CodeInternal,
}

// Code maps an error to its code. Everything about reaching the server
// (resolution, refusal, a forbidden address or port, a timeout) is one code,
// connect_failed, so a user cannot probe the server's network by comparing
// failures. A nil error is "".
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fetchx.ErrAuth):
		return CodeAuthFailed
	case errors.Is(err, fetchx.ErrTLS):
		return CodeTLSFailed
	case errors.Is(err, fetchx.ErrFolder):
		return CodeFolderMissing
	case errors.Is(err, fetchx.ErrNoUIDL):
		return CodeNoUIDL
	case errors.Is(err, fetchx.ErrTooLarge):
		return CodeTooLarge
	case errors.Is(err, ErrQuota):
		return CodeQuotaExceeded
	case errors.Is(err, credcrypt.ErrKeyUnknown), errors.Is(err, credcrypt.ErrCredentialInvalid):
		return CodeNeedsPassword
	case errors.Is(err, credcrypt.ErrNoKey):
		return CodeKeyUnavailable
	case errors.Is(err, fetchx.ErrConnect), errors.Is(err, fetchx.ErrTimeout),
		errors.Is(err, netx.ErrForbiddenAddress), errors.Is(err, netx.ErrForbiddenPort), errors.Is(err, netx.ErrInvalidMailHost),
		errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return CodeConnectFailed
	case errors.Is(err, fetchx.ErrProtocol), errors.Is(err, fetchx.ErrBudget), errors.Is(err, fetchx.ErrTransient):
		return CodeProtocolError
	}
	var ne net.Error
	var dns *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &dns) || errors.As(err, &opErr) || (errors.As(err, &ne) && ne.Timeout()) {
		return CodeConnectFailed
	}
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) {
		return CodeTLSFailed
	}
	return CodeInternal
}
