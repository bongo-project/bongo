// Package fetchtest holds test helpers for the remote-account clients: a
// throwaway certificate authority and loopback dial helpers. It is imported
// only by tests.
package fetchtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// CA is a certificate authority that exists for one test.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// NewCA creates a CA.
func NewCA(t testing.TB) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fetch test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &CA{cert: cert, key: key}
}

// Pool returns a root pool that trusts only this CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// ServerConfig issues a server certificate valid for the given DNS names and
// returns a TLS config using it. Loopback IP SANs are included.
func (ca *CA) ServerConfig(t testing.TB, names ...string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "fetch test server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
}

// LoopbackDialer dials every request to one fixed loopback address, whatever
// host and port are asked for. It stands in for netx.MailDialer in tests of the
// protocol clients, which would otherwise be unable to reach a fake server on a
// random port; the address rules are tested in netx.
type LoopbackDialer struct {
	Addr string
	// Hosts records each host the client asked for, so a test can check the
	// name used for TLS verification and dialling.
	Hosts []string
}

// DialContext implements the fetch Dialer interface.
func (d *LoopbackDialer) DialContext(ctx context.Context, host string, _ uint16) (net.Conn, error) {
	d.Hosts = append(d.Hosts, host)
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.Addr)
}
