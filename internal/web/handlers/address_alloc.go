package handlers

import (
	"errors"
	"reflect"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

// pebbleKeyApplier gives a plain applier the GetKey read that auth's address
// checks use to consult the /system/address/ and /system/alias/ Pebble keys.
type pebbleKeyApplier struct {
	store.RaftApplier
	ps *store.PebbleStore
}

func (p pebbleKeyApplier) GetKey(key []byte) ([]byte, error) { return p.ps.Get(key) }

// addressAllocationError reports whether address may be newly allocated to
// ownerID (empty for no owner). It returns nil when free, an error wrapping
// auth.ErrAccountExists when an account, alias or other owner's record holds
// it, and any other error for a failed read (callers must fail closed).
func addressAllocationError(ps *store.PebbleStore, md *store.MetadataManager, applier store.RaftApplier, address, ownerID string) error {
	if ps == nil || md == nil || applier == nil {
		return errors.New("address ownership stores not available")
	}
	return auth.CheckAddressFreeFor(pebbleKeyApplier{RaftApplier: applier, ps: ps}, md, address, ownerID)
}

// keyGetter is the read side of the Pebble store.
type keyGetter interface {
	Get(key []byte) ([]byte, error)
}

// domainHostedLocally reports whether this server hosts the domain, i.e. a
// /system/domain/{domain} key exists. Catch-all domains are hosted domains.
func domainHostedLocally(ps keyGetter, domain string) (bool, error) {
	if ps == nil || (reflect.ValueOf(ps).Kind() == reflect.Pointer && reflect.ValueOf(ps).IsNil()) {
		return false, errors.New("pebble store not available")
	}
	return store.DomainHosted(ps, domain)
}
