package handlers

import (
	"log/slog"
	"strings"

	"google.golang.org/protobuf/proto"

	internalmail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// LoadStoredDKIMKeys scans /system/domain/ in Pebble and registers a per-domain
// DKIM signer for every key with a /dkim suffix that has DkimEnabled set.
//
// This is called at agent startup (before the HTTP server and queue worker start)
// so that stored per-domain keys survive a restart. Only the /dkim sub-keys are
// processed; plain domain enable/disable keys (which hold "enabled"/"disabled"
// bytes, not proto bytes) are explicitly skipped.
//
// Errors (e.g. a corrupt proto entry) are logged and skipped; they do not abort
// the startup load.
func LoadStoredDKIMKeys(ps *store.PebbleStore, reg *internalmail.DKIMRegistry, logger *slog.Logger) {
	if ps == nil || reg == nil {
		return
	}

	iter, err := ps.PrefixScan([]byte("/system/domain/"))
	if err != nil {
		logger.Warn("dkim: startup load: PrefixScan failed", "error", err)
		return
	}
	defer iter.Close()

	for iter.Next() {
		key := string(iter.Key())
		// Only process /system/domain/{domain}/dkim keys.
		if !strings.HasSuffix(key, "/dkim") {
			continue
		}

		// Extract domain name from key: /system/domain/{domain}/dkim
		trimmed := strings.TrimPrefix(key, "/system/domain/")
		domain := strings.TrimSuffix(trimmed, "/dkim")
		if domain == "" {
			continue
		}

		val, err := iter.Value()
		if err != nil {
			logger.Warn("dkim: startup load: failed to read value", "key", key, "error", err)
			continue
		}

		var rec bongov1.DomainRecord
		if err := proto.Unmarshal(val, &rec); err != nil {
			logger.Warn("dkim: startup load: failed to unmarshal DomainRecord", "domain", domain, "error", err)
			continue
		}

		if !rec.DkimEnabled || rec.DkimPrivateKey == "" {
			continue
		}

		selector := rec.DkimSelector
		if selector == "" {
			selector = "mail"
		}

		signer, err := internalmail.NewDKIMSignerFromPEM(selector, domain, rec.DkimPrivateKey)
		if err != nil {
			// Log only domain and selector, never the private key.
			logger.Warn("dkim: startup load: failed to create signer", "domain", domain, "selector", selector, "error", err)
			continue
		}

		reg.Add(domain, signer)
		logger.Info("dkim: startup load: registered signer", "domain", domain, "selector", selector)
	}
}
