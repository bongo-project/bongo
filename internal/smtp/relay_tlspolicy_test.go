package smtp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log/slog"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-go/bongo-go/internal/config"
)

// selfSignedFor mints a throwaway certificate for host, used by the STARTTLS
// mock below. It is returned alongside the parsed leaf so a test can derive a
// TLSA record that genuinely matches it.
func selfSignedFor(t *testing.T, host string) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf
}

// startMockSMTPTLS is startMockSMTP with STARTTLS advertised, so the relay's
// TLS path is actually exercised rather than skipped.
func startMockSMTPTLS(t *testing.T, backend *mockRelayBackend, cert tls.Certificate) string {
	t.Helper()

	srv := gosmtp.NewServer(backend)
	srv.Addr = "127.0.0.1:0"
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock smtp listen: %v", err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().String()
}

// TestRelayRequireTLSNeverFallsBackToPlaintext covers MTA-STS enforce and DANE:
// when policy requires authenticated TLS, delivery must defer rather than send
// the message in the clear.
//
// relay_mtasts_test.go only asserts POLICY DERIVATION (mtaStsPolicyForMX) --
// that the right requireTLS flag comes out of a policy file. Nothing exercised
// sendToHost's enforcement of that flag, and the existing relay mocks offer no
// STARTTLS at all, so deleting the enforcement branches left every test green
// while the relay quietly downgraded to plaintext for exactly the domains that
// asked it not to.
func TestRelayRequireTLSNeverFallsBackToPlaintext(t *testing.T) {
	backend := &mockRelayBackend{messages: make(chan mockRelayMessage, 10)}
	addr := startMockSMTP(t, backend) // deliberately no STARTTLS support
	relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

	raw := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: t\r\n\r\nhi")

	t.Run("policy requires TLS and server offers none", func(t *testing.T) {
		_, err := relay.sendToHost(context.Background(), addr, "alice@example.com",
			[]string{"bob@external.com"}, raw, deliveryTLSPolicy{requireTLS: true})
		if err == nil {
			t.Fatal("relay delivered over plaintext to a host whose policy requires TLS")
		}
		if !strings.Contains(err.Error(), "TLS") {
			t.Errorf("error = %q, want it to name the TLS policy", err)
		}
		select {
		case msg := <-backend.messages:
			t.Fatalf("message was transmitted in the clear: %+v", msg)
		default:
		}
	})

	// Control: without the policy, the same server accepts the message. This is
	// what proves the failure above comes from the TLS policy and not from the
	// mock being broken.
	t.Run("no policy means opportunistic delivery still works", func(t *testing.T) {
		_, err := relay.sendToHost(context.Background(), addr, "alice@example.com",
			[]string{"bob@external.com"}, raw, deliveryTLSPolicy{})
		if err != nil {
			t.Fatalf("opportunistic delivery failed: %v", err)
		}
		select {
		case <-backend.messages:
		case <-time.After(2 * time.Second):
			t.Fatal("message was not delivered")
		}
	})
}

// TestRelayDANEVerifiesCertificateAgainstTLSA covers the VerifyConnection
// closure wired up when TLSA records are present.
//
// dane_test.go unit-tests tlsaMatches directly, which passes whether or not the
// relay ever calls it. That matters more here than elsewhere: the DANE path
// sets InsecureSkipVerify, so if the TLSA substitute check is removed the
// connection is left with NO certificate validation of any kind -- strictly
// worse than not doing DANE.
func TestRelayDANEVerifiesCertificateAgainstTLSA(t *testing.T) {
	cert, leaf := selfSignedFor(t, "localhost")
	raw := []byte("From: alice@example.com\r\nTo: bob@external.com\r\nSubject: t\r\n\r\nhi")

	// A TLSA record that genuinely matches the server's certificate:
	// usage 3 (DANE-EE), selector 0 (full cert), matching 1 (SHA-256).
	matching := &dns.TLSA{Usage: 3, Selector: 0, MatchingType: 1}
	if err := matching.Sign(3, 0, 1, leaf); err != nil {
		t.Fatalf("derive matching TLSA: %v", err)
	}

	// Same shape, wrong digest.
	mismatched := &dns.TLSA{
		Usage: 3, Selector: 0, MatchingType: 1,
		Certificate: strings.Repeat("ab", 32),
	}

	tests := []struct {
		name    string
		tlsa    []*dns.TLSA
		wantErr bool
	}{
		{"certificate matches the TLSA record", []*dns.TLSA{matching}, false},
		{"certificate matches no TLSA record", []*dns.TLSA{mismatched}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &mockRelayBackend{messages: make(chan mockRelayMessage, 10)}
			addr := startMockSMTPTLS(t, backend, cert)
			relay := NewRelay(&config.SMTPRelay{Enabled: true, Host: addr}, "mail.example.test", slog.Default())

			_, err := relay.sendToHost(context.Background(), addr, "alice@example.com",
				[]string{"bob@external.com"}, raw,
				deliveryTLSPolicy{requireTLS: true, daneTLSA: tt.tlsa})

			if tt.wantErr {
				if err == nil {
					t.Fatal("relay accepted a certificate matching no TLSA record; " +
						"with InsecureSkipVerify set this means no validation at all")
				}
				select {
				case msg := <-backend.messages:
					t.Fatalf("message was sent to an unauthenticated server: %+v", msg)
				default:
				}
				return
			}
			if err != nil {
				t.Fatalf("delivery failed against a matching TLSA record: %v", err)
			}
		})
	}
}
