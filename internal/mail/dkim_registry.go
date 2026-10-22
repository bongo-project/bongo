package mail

import (
	"log/slog"
	"strings"
	"sync"
)

// DKIMRegistry is a concurrency-safe mapping of lowercased From domain to
// DKIMSigner. A nil DKIMSigner entry means no signing for that domain.
//
// Typical usage:
//
//  1. Call Seed to register the primary/configured domain so that existing
//     single-domain behaviour is preserved byte-for-byte.
//  2. Call Add for additional per-domain keys loaded from Pebble/DomainRecord.
//  3. Call Sign in the queue worker instead of calling DKIMSigner.Sign directly.
type DKIMRegistry struct {
	mu      sync.RWMutex
	signers map[string]*DKIMSigner
	seeded  map[string]bool // domains whose signer came from Seed (static config)
}

// NewDKIMRegistry returns an initialised, empty registry.
func NewDKIMRegistry() *DKIMRegistry {
	return &DKIMRegistry{
		signers: make(map[string]*DKIMSigner),
		seeded:  make(map[string]bool),
	}
}

// Seed registers the primary domain's signer. Intended for the config-built
// signer so that existing behaviour (same selector, canonicalization,
// HeaderKeys, rsa-sha256) is preserved without re-creating it.
// Seed is NOT thread-safe against concurrent Get/Sign calls; call it during
// initialisation before starting the worker goroutines.
func (r *DKIMRegistry) Seed(domain string, signer *DKIMSigner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := strings.ToLower(domain)
	r.signers[d] = signer
	r.seeded[d] = true
}

// Add registers a per-domain signer. Safe to call concurrently.
func (r *DKIMRegistry) Add(domain string, signer *DKIMSigner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := strings.ToLower(domain)
	r.signers[d] = signer
	delete(r.seeded, d)
}

// Remove drops the signer for a domain. Removing an unknown domain is a no-op.
// A signer registered through Seed is kept: it comes from static config, not
// Pebble, and nothing would put it back until restart.
// The registry is per-node memory: other nodes keep their signer until restart.
func (r *DKIMRegistry) Remove(domain string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := strings.ToLower(domain)
	if r.seeded[d] {
		return
	}
	delete(r.signers, d)
}

// Get returns the signer for the given domain, or nil if no key is
// configured for that domain. Safe to call concurrently.
func (r *DKIMRegistry) Get(domain string) *DKIMSigner {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.signers[strings.ToLower(domain)]
}

// Sign signs rawMsg using the signer for the From header domain.
//
//   - If the From header is missing, unparseable or holds anything other
//     than exactly one address (StrictFromDomain), the message is returned
//     unsigned and a structured skip log is emitted.
//   - If no signer is registered for the From domain, the message is returned
//     unsigned and a skip log is emitted (no DKIM-Signature is ever added with
//     the wrong d=).
//   - If signing fails (e.g. key error), an error-level log is emitted and the
//     message is returned unsigned (never dropped).
//   - A nil *DKIMRegistry is valid and acts as a passthrough (no signing).
func (r *DKIMRegistry) Sign(rawMsg []byte, logger *slog.Logger) ([]byte, error) {
	if r == nil {
		return rawMsg, nil
	}

	fromDomain, err := StrictFromDomain(rawMsg)
	if err != nil {
		// Only the failure class is logged, never header content.
		logger.Warn("dkim: skipping signing - From header is not a single address",
			"reason", err.Error(),
		)
		return rawMsg, nil
	}

	signer := r.Get(fromDomain)
	if signer == nil {
		logger.Warn("dkim: skipping signing - no key configured for domain",
			"domain", fromDomain,
		)
		return rawMsg, nil
	}

	signed, err := signer.Sign(rawMsg)
	if err != nil {
		logger.Error("dkim: signing failed, sending unsigned",
			"domain", fromDomain,
			"error", err,
		)
		return rawMsg, nil
	}

	return signed, nil
}
