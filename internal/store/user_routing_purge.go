package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// PurgeUserRouting removes the system-level routing entries that point at a
// user being deleted and that live outside /user/{id}/: legacy
// /system/alias/{addr} keys whose value is the user's email, and
// /system/catchall/{domain} keys whose value is the user's ID.
//
// The legacy alias value is the target's email, not an ID, so once the address
// is re-registered the stale alias resolves to the NEW account and hands it the
// old alias's mail. A resolver liveness check cannot catch that; the key has to
// go. Values are matched case-insensitively with whitespace trimmed, because
// older data was not normalised; only a whole-value match counts, never a
// shared suffix.
//
// Partial-failure semantics: each key is its own Raft command (5s timeout), so
// the purge is NOT atomic. It attempts every key, returns the ones removed and
// the first error per failed key joined together, and never aborts early. A
// leftover catch-all is harmless (the resolver rejects a dead owner with
// ErrRecipientUnavailable). A leftover legacy alias is the one residual risk if
// the address is later reused; an operator must remove it by hand (the address orphan report lists leftover legacy aliases and catch-alls, but repair does not delete them).
func PurgeUserRouting(ps *PebbleStore, applier RaftApplier, userID, email string) (aliases, catchAlls []string, err error) {
	if ps == nil || applier == nil || userID == "" {
		return nil, nil, nil
	}
	email = strings.TrimSpace(email)

	var errs []error
	del := func(key string) bool {
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: []byte(key)}
		if dErr := applier.ApplyCommand(cmd, 5*time.Second); dErr != nil {
			errs = append(errs, fmt.Errorf("purge user routing: delete %s: %w", key, dErr))
			return false
		}
		return true
	}

	aliasKeys, sErr := scanKeysByValue(ps, "/system/alias/", func(v string) bool {
		return email != "" && strings.EqualFold(strings.TrimSpace(v), email)
	})
	if sErr != nil {
		errs = append(errs, sErr)
	}
	for _, k := range aliasKeys {
		if del(k) {
			aliases = append(aliases, strings.TrimPrefix(k, "/system/alias/"))
		}
	}

	caKeys, sErr := scanKeysByValue(ps, "/system/catchall/", func(v string) bool {
		return strings.TrimSpace(v) == userID
	})
	if sErr != nil {
		errs = append(errs, sErr)
	}
	for _, k := range caKeys {
		if del(k) {
			catchAlls = append(catchAlls, strings.TrimPrefix(k, "/system/catchall/"))
		}
	}
	return aliases, catchAlls, errors.Join(errs...)
}

// scanKeysByValue returns the keys under prefix whose value satisfies match.
func scanKeysByValue(ps *PebbleStore, prefix string, match func(string) bool) (keys []string, rErr error) {
	it, err := ps.PrefixScan([]byte(prefix))
	if err != nil {
		return nil, fmt.Errorf("purge user routing: scan %s: %w", prefix, err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		val, vErr := it.Value()
		if vErr != nil {
			return keys, fmt.Errorf("purge user routing: read %s: %w", it.Key(), vErr)
		}
		if match(string(val)) {
			keys = append(keys, string(it.Key()))
		}
	}
	return keys, nil
}
