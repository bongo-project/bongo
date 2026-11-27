package security

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CertHolder serves one TLS certificate through GetCertificate and lets it be
// swapped atomically while connections are open. A handshake in flight keeps
// the certificate it started with; new handshakes get the current one.
//
// A file-backed holder re-reads its files on Reload. A holder built from a
// ready certificate (NewStaticCertHolder) has no files; Set is how another
// source, such as ACME, feeds it a renewal.
//
// Errors from reloading carry a fixed reason and the file names, never library
// text from the key path, so key material cannot reach a log.
type CertHolder struct {
	name     string
	certFile string
	keyFile  string
	cur      atomic.Pointer[tls.Certificate]
	mu       sync.Mutex // serialises loads and Set
	now      func() time.Time
}

// CertReloadError describes why a certificate was refused. Reason is a fixed
// phrase; Files names the files involved.
type CertReloadError struct {
	Reason string
	Files  []string
}

func (e *CertReloadError) Error() string {
	return fmt.Sprintf("%s (%s)", e.Reason, strings.Join(e.Files, ", "))
}

// NewStaticCertHolder wraps a ready certificate. Reload does nothing.
func NewStaticCertHolder(name string, cert *tls.Certificate) *CertHolder {
	h := &CertHolder{name: name, now: time.Now}
	h.cur.Store(cert)
	return h
}

// LoadCertHolder loads certFile and keyFile strictly. An expired certificate
// still loads (a lapsed renewal must not stop a restart) and is logged.
func LoadCertHolder(name, certFile, keyFile string) (*CertHolder, error) {
	h := &CertHolder{name: name, certFile: certFile, keyFile: keyFile, now: time.Now}
	cert, err := h.read()
	if err != nil {
		return nil, err
	}
	if cert.Leaf.NotAfter.Before(h.now()) {
		slog.Warn("tls: certificate has expired", "listener", name, "cert_file", certFile, "not_after", cert.Leaf.NotAfter)
	}
	h.cur.Store(cert)
	return h, nil
}

// NewFallbackCertHolder serves fallback until the configured files can be
// loaded; Reload (and the agent's poll) picks them up when they appear.
func NewFallbackCertHolder(name, certFile, keyFile string, fallback *tls.Certificate) *CertHolder {
	h := NewStaticCertHolder(name, fallback)
	h.certFile, h.keyFile = certFile, keyFile
	return h
}

// Name returns the listener label used in logs.
func (h *CertHolder) Name() string { return h.name }

// Files returns the certificate and key paths; empty for a static holder.
func (h *CertHolder) Files() (certFile, keyFile string) { return h.certFile, h.keyFile }

// FileBacked reports whether Reload has files to read.
func (h *CertHolder) FileBacked() bool { return h.certFile != "" && h.keyFile != "" }

// Current returns the certificate being served.
func (h *CertHolder) Current() *tls.Certificate { return h.cur.Load() }

// GetCertificate implements tls.Config.GetCertificate without locking.
func (h *CertHolder) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c := h.cur.Load()
	if c == nil {
		return nil, errors.New("no certificate available")
	}
	return c, nil
}

// TLSConfig returns a new config per call (servers may clone or mutate it).
// Certificates stays empty so GetCertificate is authoritative.
func (h *CertHolder) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: h.GetCertificate,
	}
}

// Set swaps in a ready certificate, for example one obtained through ACME.
// It refuses a nil, unparsed or expired certificate and keeps the old one.
func (h *CertHolder) Set(cert *tls.Certificate) error {
	if cert == nil || len(cert.Certificate) == 0 {
		return &CertReloadError{Reason: "no certificate given"}
	}
	if cert.Leaf == nil {
		return &CertReloadError{Reason: "certificate not parsed"}
	}
	if cert.Leaf.NotAfter.Before(h.now()) {
		return &CertReloadError{Reason: "certificate expired"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cur.Store(cert)
	return nil
}

// Reload re-reads the files and swaps the certificate in when the pair parses,
// matches and has not expired. On any failure the current certificate stays
// and a *CertReloadError is returned. changed reports whether the served
// certificate now differs from the previous one. A static holder does nothing.
func (h *CertHolder) Reload() (changed bool, err error) {
	if !h.FileBacked() {
		return false, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cert, err := h.read()
	if err != nil {
		return false, err
	}
	if cert.Leaf.NotAfter.Before(h.now()) {
		return false, &CertReloadError{Reason: "certificate expired", Files: []string{h.certFile}}
	}
	old := h.cur.Load()
	h.cur.Store(cert)
	return old == nil || len(old.Certificate) == 0 || string(old.Certificate[0]) != string(cert.Certificate[0]), nil
}

// read loads and checks the pair. Only fixed reasons leave this function.
func (h *CertHolder) read() (*tls.Certificate, error) {
	certPEM, err := os.ReadFile(h.certFile) // #nosec G304 -- operator-configured path
	if err != nil {
		return nil, &CertReloadError{Reason: "reading certificate file", Files: []string{h.certFile}}
	}
	keyPEM, err := os.ReadFile(h.keyFile) // #nosec G304 -- operator-configured path
	if err != nil {
		return nil, &CertReloadError{Reason: "reading key file", Files: []string{h.keyFile}}
	}
	leaf, err := parseLeaf(certPEM)
	if err != nil {
		return nil, &CertReloadError{Reason: "parsing certificate", Files: []string{h.certFile}}
	}
	if !hasPrivateKey(keyPEM) {
		return nil, &CertReloadError{Reason: "parsing key", Files: []string{h.keyFile}}
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, &CertReloadError{Reason: "key does not match certificate", Files: []string{h.certFile, h.keyFile}}
	}
	cert.Leaf = leaf
	return &cert, nil
}

// parseLeaf parses the first certificate in a PEM bundle.
func parseLeaf(certPEM []byte) (*x509.Certificate, error) {
	for {
		var block *pem.Block
		block, certPEM = pem.Decode(certPEM)
		if block == nil {
			return nil, errors.New("no certificate")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
}

// hasPrivateKey reports whether keyPEM holds a private key block that parses
// in one of the supported forms.
func hasPrivateKey(keyPEM []byte) bool {
	for {
		var block *pem.Block
		block, keyPEM = pem.Decode(keyPEM)
		if block == nil {
			return false
		}
		if !strings.HasSuffix(block.Type, "PRIVATE KEY") {
			continue
		}
		if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			return true
		}
		if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return true
		}
		if _, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
			return true
		}
	}
}
