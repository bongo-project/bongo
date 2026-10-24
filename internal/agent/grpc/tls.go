package agentgrpc

import (
	"crypto/tls"
	"fmt"

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

// serverTLSConfig builds the listener-side TLS configuration. It fails,
// naming the config key, when any file is not configured or cannot be
// loaded, so a caller never ends up listening without mutual TLS.
func serverTLSConfig(f TLSFiles) (*tls.Config, error) {
	for _, k := range []struct{ key, val string }{
		{"agent_server.tls.cert_file", f.CertFile},
		{"agent_server.tls.key_file", f.KeyFile},
		{"agent_server.tls.ca_file", f.CAFile},
	} {
		if k.val == "" {
			return nil, fmt.Errorf("%s is not configured: agent gRPC requires mutual TLS", k.key)
		}
	}
	cert, err := tls.LoadX509KeyPair(f.CertFile, f.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("agent_server.tls: loading cert/key: %w", err)
	}
	pool, err := security.LoadCAPool(f.CAFile)
	if err != nil {
		return nil, fmt.Errorf("agent_server.tls.ca_file %s: %w", f.CAFile, err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
