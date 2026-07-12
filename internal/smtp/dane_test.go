package smtp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func testCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mx.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cert
}

func TestTLSAMatches(t *testing.T) {
	cert := testCert(t)

	// DANE-EE (usage 3), SPKI selector (1), SHA-256 (1) association for this cert.
	good := &dns.TLSA{Hdr: dns.RR_Header{Name: "_25._tcp.mx.example.com.", Rrtype: dns.TypeTLSA}}
	if err := good.Sign(3, 1, 1, cert); err != nil {
		t.Fatalf("sign TLSA: %v", err)
	}
	if !tlsaMatches([]*dns.TLSA{good}, []*x509.Certificate{cert}) {
		t.Error("DANE-EE TLSA computed from the cert should match it")
	}

	// A different cert's association must not match.
	other := &dns.TLSA{Hdr: good.Hdr}
	if err := other.Sign(3, 1, 1, testCert(t)); err != nil {
		t.Fatalf("sign other: %v", err)
	}
	if tlsaMatches([]*dns.TLSA{other}, []*x509.Certificate{cert}) {
		t.Error("a TLSA for a different cert must not match")
	}

	// PKIX usages (1) are ignored for SMTP DANE even if the association matches.
	pkixEE := &dns.TLSA{Hdr: good.Hdr}
	if err := pkixEE.Sign(1, 1, 1, cert); err != nil {
		t.Fatalf("sign pkix: %v", err)
	}
	if tlsaMatches([]*dns.TLSA{pkixEE}, []*x509.Certificate{cert}) {
		t.Error("PKIX-EE (usage 1) should be ignored per RFC 7672")
	}

	if tlsaMatches([]*dns.TLSA{good}, nil) {
		t.Error("empty chain must not match")
	}
}
