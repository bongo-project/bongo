package imap

import (
	"crypto/tls"
	"log/slog"
	"testing"
)

func TestNewServer_NilTLS(t *testing.T) {
	logger := slog.Default()
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, true, logger)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.server == nil {
		t.Error("plain server is nil")
	}
	if s.tlsServer != nil {
		t.Error("TLS server should be nil when no TLS config provided")
	}
}

func TestNewServer_WithTLS(t *testing.T) {
	logger := slog.Default()
	tlsCfg := &tls.Config{} //nolint:gosec // test only

	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, tlsCfg, true, logger)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.server == nil {
		t.Error("plain server is nil")
	}
	if s.tlsServer == nil {
		t.Error("TLS server should not be nil when TLS config provided")
	}
}

func TestServer_PlainAddrBeforeListen(t *testing.T) {
	logger := slog.Default()
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, true, logger)

	addr := s.PlainAddr()
	if addr != "" {
		t.Errorf("PlainAddr before Listen should be empty, got %q", addr)
	}
}

func TestServer_CloseWithoutListen(t *testing.T) {
	logger := slog.Default()
	s := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, true, logger)

	if err := s.Close(); err != nil {
		t.Errorf("Close without Listen should not error, got %v", err)
	}
}
