// Package acmecert obtains and renews the node certificate over ACME
// (RFC 8555) with the TLS-ALPN-01 challenge (RFC 8737).
//
// It does not use autocert: autocert issues during a TLS handshake, so
// unauthenticated traffic would drive issuance and cluster writes, it renews
// on every node, and its cache would turn a handshake into a Raft write.
// Here only the leader's Manager issues, on a timer, and a handshake does
// nothing but read.
//
// Every key and certificate lives in the Store (Pebble through Raft in the
// agent), is replicated and is included in backups, like DKIM keys. Nothing
// in this package logs key material, PEM or DER, the account URI or a
// challenge token.
package acmecert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Store is the replicated key-value store behind /system/acme/. Get returns
// (nil, nil) for an absent key. Put and Delete are single Raft commands in
// the agent; a Put on a node that is not the leader fails.
type Store interface {
	Get(key string) ([]byte, error)
	Put(key string, value []byte) error
	Delete(key string) error
	// List returns the keys under prefix.
	List(prefix string) ([]string, error)
}

// CertKey is the key of the issued certificate.
const CertKey = "/system/acme/cert"

// StateKey holds the leader's backoff state: 16 bytes, big-endian failure
// count then retry-at unix seconds (not a protobuf). Written only by the
// leader's manager, deleted after a success.
const StateKey = "/system/acme/state"

const (
	accountPrefix   = "/system/acme/account/"
	challengePrefix = "/system/acme/challenge/"
)

// AccountKey returns the key of the account for one CA directory. Keying by
// directory means moving from staging to production makes a new account.
func AccountKey(directoryURL string) string {
	sum := sha256.Sum256([]byte(directoryURL))
	return accountPrefix + hex.EncodeToString(sum[:])[:32]
}

// ChallengeKey returns the key of the TLS-ALPN-01 certificate for a domain.
func ChallengeKey(domain string) string {
	return challengePrefix + strings.ToLower(domain)
}

// encodeState and decodeState handle the StateKey value.
func encodeState(failures int, retryAt time.Time) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[:8], uint64(max(failures, 0)))
	binary.BigEndian.PutUint64(b[8:], uint64(max(retryAt.Unix(), 0)))
	return b
}

func decodeState(b []byte) (failures int, retryAt time.Time, ok bool) {
	if len(b) != 16 {
		return 0, time.Time{}, false
	}
	n := binary.BigEndian.Uint64(b[:8])
	if n == 0 || n > 1<<20 {
		return 0, time.Time{}, false
	}
	secs := binary.BigEndian.Uint64(b[8:])
	if secs > 1<<40 {
		return 0, time.Time{}, false
	}
	return int(n), time.Unix(int64(secs), 0), true
}

// Fixed reasons: errors from the key path never carry library text.
var (
	ErrBadRecord = errors.New("acme: stored record is not valid")
	ErrKeyMatch  = errors.New("acme: key does not match certificate")
)

// newKey makes a fresh ECDSA P-256 key and its PKCS#8 DER.
func newKey() (*ecdsa.PrivateKey, []byte, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding key: %w", err)
	}
	return k, der, nil
}

// EncodeCertificate builds the stored record. chain is the leaf first.
func EncodeCertificate(chain [][]byte, keyDER []byte, domains []string, directoryURL string, issuedAt time.Time) ([]byte, error) {
	if len(chain) == 0 {
		return nil, ErrBadRecord
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil, ErrBadRecord
	}
	d := slices.Clone(domains)
	slices.Sort(d)
	return proto.Marshal(&bongov1.AcmeCertificate{
		ChainDer:     chain,
		KeyDer:       keyDER,
		Domains:      d,
		NotAfter:     leaf.NotAfter.Unix(),
		IssuedAt:     issuedAt.Unix(),
		DirectoryUrl: directoryURL,
	})
}

// DecodeCertificate parses a stored record.
func DecodeCertificate(b []byte) (*bongov1.AcmeCertificate, error) {
	var rec bongov1.AcmeCertificate
	if err := proto.Unmarshal(b, &rec); err != nil {
		return nil, ErrBadRecord
	}
	return &rec, nil
}

// TLSCertificate turns a record into a certificate ready for a CertHolder:
// the chain parses, the PKCS#8 key parses and matches the leaf. The returned
// error is one of the fixed errors above.
func TLSCertificate(rec *bongov1.AcmeCertificate) (*tls.Certificate, error) {
	if rec == nil || len(rec.GetChainDer()) == 0 || len(rec.GetKeyDer()) == 0 {
		return nil, ErrBadRecord
	}
	leaf, err := x509.ParseCertificate(rec.GetChainDer()[0])
	if err != nil {
		return nil, ErrBadRecord
	}
	for _, der := range rec.GetChainDer()[1:] {
		if _, err := x509.ParseCertificate(der); err != nil {
			return nil, ErrBadRecord
		}
	}
	key, err := x509.ParsePKCS8PrivateKey(rec.GetKeyDer())
	if err != nil {
		return nil, ErrBadRecord
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, ErrBadRecord
	}
	pub, ok := signer.Public().(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(leaf.PublicKey) {
		return nil, ErrKeyMatch
	}
	return &tls.Certificate{
		Certificate: rec.GetChainDer(),
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

// LoadCertificate reads and parses the stored certificate. It returns
// (nil, nil, nil) when none has been issued.
func LoadCertificate(s Store) (*bongov1.AcmeCertificate, *tls.Certificate, error) {
	raw, err := s.Get(CertKey)
	if err != nil {
		return nil, nil, fmt.Errorf("reading certificate: %w", err)
	}
	if raw == nil {
		return nil, nil, nil
	}
	rec, err := DecodeCertificate(raw)
	if err != nil {
		return nil, nil, err
	}
	cert, err := TLSCertificate(rec)
	if err != nil {
		return rec, nil, err
	}
	return rec, cert, nil
}

// SortedCopy returns a sorted copy of names.
func SortedCopy(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)
	return out
}
