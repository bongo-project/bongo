package mail

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	netmail "net/mail"
	"os"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/bongo-go/bongo-go/internal/config"
)

// DKIMSigner signs outbound messages with DKIM.
// A nil *DKIMSigner is valid and acts as a passthrough.
type DKIMSigner struct {
	selector string
	domain   string
	key      crypto.Signer
}

// NewDKIMSigner creates a DKIM signer from config.
// Returns nil if no private key is configured (DKIM disabled).
func NewDKIMSigner(cfg *config.SMTPDKIMConfig, domain string) (*DKIMSigner, error) {
	if cfg == nil || cfg.PrivateKeyFile == "" {
		return nil, nil
	}

	pemData, err := os.ReadFile(cfg.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("reading DKIM key: %w", err)
	}

	key, err := parsePrivateKey(pemData)
	if err != nil {
		return nil, fmt.Errorf("parsing DKIM key: %w", err)
	}

	selector := cfg.Selector
	if selector == "" {
		selector = "mail"
	}

	return &DKIMSigner{
		selector: selector,
		domain:   domain,
		key:      key,
	}, nil
}

// NewDKIMSignerFromKey creates a DKIM signer from a pre-loaded key (for testing).
func NewDKIMSignerFromKey(selector, domain string, key crypto.Signer) *DKIMSigner {
	return &DKIMSigner{
		selector: selector,
		domain:   domain,
		key:      key,
	}
}

// NewDKIMSignerFromPEM creates a DKIM signer from a PEM-encoded private key
// string. This is used for per-domain keys stored in DomainRecord.dkim_private_key.
// The PEM must contain an RSA or Ed25519 private key.
func NewDKIMSignerFromPEM(selector, domain, pemData string) (*DKIMSigner, error) {
	if selector == "" {
		selector = "mail"
	}
	key, err := parsePrivateKey([]byte(pemData))
	if err != nil {
		return nil, fmt.Errorf("parsing DKIM key for domain %s: %w", domain, err)
	}
	return &DKIMSigner{
		selector: selector,
		domain:   domain,
		key:      key,
	}, nil
}

// FromRawMessageDomain parses the From header from a raw RFC 5322 message
// and returns the lowercased sending domain. Returns an error if the From
// header is missing, empty, or cannot be parsed to a valid address with a
// domain component.
func FromRawMessageDomain(rawMsg []byte) (string, error) {
	msg, err := netmail.ReadMessage(bytes.NewReader(rawMsg))
	if err != nil {
		return "", fmt.Errorf("parsing message headers: %w", err)
	}
	fromHdr := msg.Header.Get("From")
	if fromHdr == "" {
		return "", fmt.Errorf("no From header")
	}
	domain := FromHeaderDomain(fromHdr)
	if domain == "" {
		return "", fmt.Errorf("cannot parse domain from From header: %q", fromHdr)
	}
	return domain, nil
}

// Sign adds a DKIM-Signature header to the message.
// If the signer is nil, the message is returned unchanged.
func (ds *DKIMSigner) Sign(rawMsg []byte) ([]byte, error) {
	if ds == nil {
		return rawMsg, nil
	}

	opts := &dkim.SignOptions{
		Domain:                 ds.domain,
		Selector:               ds.selector,
		Signer:                 ds.key,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
		HeaderKeys:             []string{"from", "to", "subject", "date", "message-id", "content-type"},
	}

	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(rawMsg), opts); err != nil {
		return nil, fmt.Errorf("DKIM signing: %w", err)
	}

	return signed.Bytes(), nil
}

func parsePrivateKey(pemData []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}

	// Try ed25519.
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if ed, ok := key.(ed25519.PrivateKey); ok {
			return ed, nil
		}
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}

	// Try RSA PKCS1.
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	return nil, fmt.Errorf("unsupported private key type")
}
