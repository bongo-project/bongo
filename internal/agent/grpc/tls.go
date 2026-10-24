package agentgrpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	"github.com/bongo-project/bongo/internal/security"
)

// TLSFiles names the PEM files that secure the agent gRPC listeners. All
// three are required: the listeners are mutual TLS only and there is no
// plaintext fallback.
type TLSFiles struct {
	CertFile string // server certificate (needs serverAuth and clientAuth EKU)
	KeyFile  string // private key for CertFile
	CAFile   string // CA that must have signed every client certificate
}

// loadTLSFiles checks that all three files are configured, then loads the
// certificate pair and the CA pool. Every failure names the config key, so
// neither a listener nor a dialler is ever built without mutual TLS.
func loadTLSFiles(f TLSFiles) (tls.Certificate, *x509.CertPool, error) {
	for _, k := range []struct{ key, val string }{
		{"agent_server.tls.cert_file", f.CertFile},
		{"agent_server.tls.key_file", f.KeyFile},
		{"agent_server.tls.ca_file", f.CAFile},
	} {
		if k.val == "" {
			return tls.Certificate{}, nil, fmt.Errorf("%s is not configured: agent gRPC requires mutual TLS", k.key)
		}
	}
	cert, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("agent_server.tls: loading cert/key: %w", err)
	}
	pool, err := security.LoadCAPool(f.CAFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("agent_server.tls.ca_file %s: %w", f.CAFile, err)
	}
	return cert, pool, nil
}

// serverTLSConfig builds the listener-side TLS configuration. It fails,
// naming the config key, when any file is not configured or cannot be
// loaded, so a caller never ends up listening without mutual TLS.
func serverTLSConfig(f TLSFiles) (*tls.Config, error) {
	cert, pool, err := loadTLSFiles(f)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// clientTLSConfig builds the dialler-side TLS configuration for addr
// ("host:port"). The server certificate is verified against ca_file using
// the dialled host as the name, so it needs a matching IP or DNS SAN. The
// same cert/key pair is presented as the client certificate.
func clientTLSConfig(f TLSFiles, addr string) (*tls.Config, error) {
	cfg, err := clientTLSBase(f)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("agent grpc: dial address %q: %w", addr, err)
	}
	cfg.ServerName = host
	return cfg, nil
}

// clientTLSBase is clientTLSConfig without a server name, for callers that
// clone it per dial.
func clientTLSBase(f TLSFiles) (*tls.Config, error) {
	cert, pool, err := loadTLSFiles(f)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// requireSpecificHost rejects an empty, unparsable or wildcard listen host so
// the agent gRPC listeners fail closed instead of binding every interface.
func requireSpecificHost(host string) error {
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("listen host %q must be a specific IP address, not empty or a wildcard (set cluster.bind_addr or server.advertise_addr)", host)
	}
	return nil
}
