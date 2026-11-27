package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testPair returns PEM for a self-signed certificate with the given serial
// and expiry, plus its key.
func testPair(t *testing.T, serial int64, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "bongo-test"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

func writePair(t *testing.T, dir string, serial int64, notAfter time.Time) (certFile, keyFile string) {
	t.Helper()
	c, k := testPair(t, serial, notAfter)
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, c, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, k, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func servedSerial(t *testing.T, addr string) (*big.Int, *tls.Conn) {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- test
	if err != nil {
		t.Fatal(err)
	}
	return conn.ConnectionState().PeerCertificates[0].SerialNumber, conn
}

func TestCertHolderSwap(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().Add(24 * time.Hour)
	certFile, keyFile := writePair(t, dir, 1, future)
	h, err := LoadCertHolder("test", certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", h.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 16)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}()
		}
	}()

	s, old := servedSerial(t, ln.Addr().String())
	defer old.Close()
	if s.Int64() != 1 {
		t.Fatalf("serial = %v, want 1", s)
	}

	writePair(t, dir, 2, future)
	changed, err := h.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	s2, nc := servedSerial(t, ln.Addr().String())
	nc.Close()
	if s2.Int64() != 2 {
		t.Fatalf("new handshake serial = %v, want 2", s2)
	}
	if got := old.ConnectionState().PeerCertificates[0].SerialNumber.Int64(); got != 1 {
		t.Fatalf("old connection serial = %d, want 1", got)
	}
	if _, err := old.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := old.Read(buf); err != nil || string(buf) != "hi" {
		t.Fatalf("old connection echo = %q, %v", buf, err)
	}
}

func TestCertHolderBadPairKeepsOld(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-time.Hour)
	tests := []struct {
		name       string
		mutate     func(t *testing.T, certFile, keyFile string)
		wantReason string
		wantFiles  []string
	}{
		{"garbage cert", func(t *testing.T, c, _ string) { _ = os.WriteFile(c, []byte("junk"), 0o600) }, "parsing certificate", []string{"cert.pem"}},
		{"garbage key", func(t *testing.T, _, k string) { _ = os.WriteFile(k, []byte("junk"), 0o600) }, "parsing key", []string{"key.pem"}},
		{"mismatched key", func(t *testing.T, _, k string) {
			_, kp := testPair(t, 9, future)
			_ = os.WriteFile(k, kp, 0o600)
		}, "does not match", []string{"cert.pem", "key.pem"}},
		{"expired", func(t *testing.T, c, k string) {
			cp, kp := testPair(t, 9, past)
			_ = os.WriteFile(c, cp, 0o600)
			_ = os.WriteFile(k, kp, 0o600)
		}, "expired", []string{"cert.pem"}},
		{"missing cert", func(t *testing.T, c, _ string) { _ = os.Remove(c) }, "reading certificate", []string{"cert.pem"}},
		{"missing key", func(t *testing.T, _, k string) { _ = os.Remove(k) }, "reading key", []string{"key.pem"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			certFile, keyFile := writePair(t, dir, 1, future)
			h, err := LoadCertHolder("test", certFile, keyFile)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, certFile, keyFile)
			changed, err := h.Reload()
			if err == nil || changed {
				t.Fatalf("Reload = %v, %v; want error", changed, err)
			}
			var re *CertReloadError
			if !errors.As(err, &re) {
				t.Fatalf("error type %T", err)
			}
			if !strings.Contains(re.Reason, tt.wantReason) {
				t.Errorf("reason %q lacks %q", re.Reason, tt.wantReason)
			}
			if len(re.Files) != len(tt.wantFiles) {
				t.Fatalf("files = %v, want %v", re.Files, tt.wantFiles)
			}
			for i, f := range re.Files {
				if filepath.Base(f) != tt.wantFiles[i] {
					t.Errorf("file %d = %s, want %s", i, f, tt.wantFiles[i])
				}
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), "BEGIN") {
				t.Errorf("error leaks PEM text: %v", err)
			}
			c, _ := h.GetCertificate(nil)
			if c.Leaf.SerialNumber.Int64() != 1 {
				t.Errorf("serving serial %v, want old 1", c.Leaf.SerialNumber)
			}
		})
	}
}

func TestCertHolderSet(t *testing.T) {
	future := time.Now().Add(time.Hour)
	good, _ := tls.X509KeyPair(testPair(t, 5, future))
	good.Leaf, _ = x509.ParseCertificate(good.Certificate[0])
	expired, _ := tls.X509KeyPair(testPair(t, 6, time.Now().Add(-time.Minute)))
	expired.Leaf, _ = x509.ParseCertificate(expired.Certificate[0])
	noLeaf, _ := tls.X509KeyPair(testPair(t, 7, future))
	noLeaf.Leaf = nil

	h := NewStaticCertHolder("acme", &good)
	tests := []struct {
		name    string
		cert    *tls.Certificate
		wantErr bool
	}{
		{"nil", nil, true},
		{"expired", &expired, true},
		{"no leaf", &noLeaf, true},
		{"valid", &good, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := h.Set(tt.cert); (err != nil) != tt.wantErr {
				t.Fatalf("Set err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	if c, _ := h.GetCertificate(nil); c != &good {
		t.Error("holder does not serve the valid certificate")
	}
}

func TestCertHolderStaticReloadNoop(t *testing.T) {
	c, _ := tls.X509KeyPair(testPair(t, 5, time.Now().Add(time.Hour)))
	h := NewStaticCertHolder("static", &c)
	if changed, err := h.Reload(); changed || err != nil {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if h.FileBacked() {
		t.Error("static holder reports files")
	}
}

func TestCertHolderFallbackPicksUpFile(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	fb, _ := tls.X509KeyPair(testPair(t, 3, time.Now().Add(time.Hour)))
	h := NewFallbackCertHolder("imap", certFile, keyFile, &fb)
	if _, err := h.Reload(); err == nil {
		t.Fatal("expected error while files are absent")
	}
	writePair(t, dir, 4, time.Now().Add(time.Hour))
	if changed, err := h.Reload(); err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if c, _ := h.GetCertificate(nil); c.Leaf.SerialNumber.Int64() != 4 {
		t.Errorf("serial = %v", c.Leaf.SerialNumber)
	}
}

func TestCertHolderConcurrent(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writePair(t, dir, 1, time.Now().Add(time.Hour))
	h, err := LoadCertHolder("test", certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if c, err := h.GetCertificate(nil); err != nil || c == nil {
						t.Error("no certificate")
						return
					}
				}
			}
		}()
	}
	for range 20 {
		if _, err := h.Reload(); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestTLSConfigFresh(t *testing.T) {
	c, _ := tls.X509KeyPair(testPair(t, 5, time.Now().Add(time.Hour)))
	h := NewStaticCertHolder("x", &c)
	a, b := h.TLSConfig(), h.TLSConfig()
	if a == b || a.GetCertificate == nil || len(a.Certificates) != 0 || a.MinVersion != tls.VersionTLS12 {
		t.Fatalf("bad config: %+v", a)
	}
}
