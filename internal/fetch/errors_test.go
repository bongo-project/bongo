package fetch

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/fetch/credcrypt"
	"github.com/bongo-project/bongo/internal/fetch/fetchx"
	"github.com/bongo-project/bongo/internal/netx"
)

func TestCode(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fetchx.ErrAuth, CodeAuthFailed},
		{fmt.Errorf("wrapped: %w", fetchx.ErrTLS), CodeTLSFailed},
		{fetchx.ErrFolder, CodeFolderMissing},
		{fetchx.ErrNoUIDL, CodeNoUIDL},
		{fetchx.ErrTooLarge, CodeTooLarge},
		{ErrQuota, CodeQuotaExceeded},
		{credcrypt.ErrKeyUnknown, CodeNeedsPassword},
		{credcrypt.ErrCredentialInvalid, CodeNeedsPassword},
		{credcrypt.ErrNoKey, CodeKeyUnavailable},
		// Everything about reaching the server is one code.
		{netx.ErrForbiddenAddress, CodeConnectFailed},
		{netx.ErrForbiddenPort, CodeConnectFailed},
		{netx.ErrInvalidMailHost, CodeConnectFailed},
		{fetchx.ErrConnect, CodeConnectFailed},
		{fetchx.ErrTimeout, CodeConnectFailed},
		{context.DeadlineExceeded, CodeConnectFailed},
		{os.ErrDeadlineExceeded, CodeConnectFailed},
		{&net.DNSError{Err: "no such host", Name: "x.example"}, CodeConnectFailed},
		{&net.OpError{Op: "dial", Err: errors.New("refused")}, CodeConnectFailed},
		{fetchx.ErrProtocol, CodeProtocolError},
		{fetchx.ErrTransient, CodeProtocolError},
		{fetchx.ErrBudget, CodeProtocolError},
		{x509.UnknownAuthorityError{}, CodeTLSFailed},
		{errors.New("something else"), CodeInternal},
	}
	for _, tt := range tests {
		if got := Code(tt.err); got != tt.want {
			t.Errorf("Code(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
	// Every code Code can return is listed.
	for _, tt := range tests {
		if c := Code(tt.err); c != "" && !containsStr(Codes, c) {
			t.Errorf("code %q missing from Codes", c)
		}
	}
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestPortFor(t *testing.T) {
	tests := []struct {
		proto, sec string
		want       uint16
	}{
		{ProtocolIMAP, SecurityTLS, 993},
		{ProtocolIMAP, SecurityStartTLS, 143},
		{ProtocolPOP3, SecurityTLS, 995},
		{ProtocolPOP3, SecurityStartTLS, 110},
		{ProtocolIMAP, "plain", 0},
		{ProtocolIMAP, "none", 0},
		{"smtp", SecurityTLS, 0},
		{"", "", 0},
	}
	for _, tt := range tests {
		if got := PortFor(tt.proto, tt.sec); got != tt.want {
			t.Errorf("PortFor(%q, %q) = %d, want %d", tt.proto, tt.sec, got, tt.want)
		}
	}
}

// The ports PortFor yields are the four the dialer accepts, so a stored account
// can always be dialled and nothing else can.
func TestPortForMatchesDialerPorts(t *testing.T) {
	d := netx.NewMailDialer(netx.MailDialerOptions{AllowPrivateNetworks: true})
	for _, proto := range []string{ProtocolIMAP, ProtocolPOP3} {
		for _, sec := range []string{SecurityTLS, SecurityStartTLS} {
			port := PortFor(proto, sec)
			_, err := d.DialContext(context.Background(), "127.0.0.1", port)
			if errors.Is(err, netx.ErrForbiddenPort) {
				t.Errorf("%s/%s: port %d refused by the dialer", proto, sec, port)
			}
		}
	}
}

// The go-imap DebugWriter writes every command, LOGIN included. It must not be
// set anywhere in the package.
func TestDebugWriterNeverSet(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if strings.Contains(code, "DebugWriter") {
				t.Errorf("%s:%d sets DebugWriter", e.Name(), i+1)
			}
		}
	}
}

// InsecureSkipVerify must not appear in the package's code either.
func TestVerificationNeverSkipped(t *testing.T) {
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, _ := os.ReadFile(e.Name())
		for i, line := range strings.Split(string(b), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if strings.Contains(code, "InsecureSkipVerify") {
				t.Errorf("%s:%d mentions InsecureSkipVerify", e.Name(), i+1)
			}
		}
	}
}
