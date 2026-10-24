package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// DomainDependents counts the live objects that still live on a domain.
type DomainDependents struct {
	Users      int // /user/{id}/profile whose email is on the domain
	Aliases    int // /system/alias/ keys on the domain
	Addresses  int // /system/address/ records on the domain whose user still exists
	Identities int // subset of Addresses with kind "identity" (send-as)
}

// Any reports whether the domain still has dependents.
func (d DomainDependents) Any() bool {
	return d.Users > 0 || d.Aliases > 0 || d.Addresses > 0
}

// DomainDependentCounts scans for users, aliases and live address records on
// the domain. Matching is on the "@domain" suffix, never ".domain", so
// subdomains are not counted. Any scan or decode error is returned so callers
// can fail closed.
func DomainDependentCounts(ps *PebbleStore, domain string) (DomainDependents, error) {
	var out DomainDependents
	suffix := "@" + strings.ToLower(strings.TrimSpace(domain))

	it, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return out, fmt.Errorf("domain dependents: scan users: %w", err)
	}
	err = func() (rErr error) {
		defer closeIter(it, &rErr)
		for it.Next() {
			if !strings.HasSuffix(string(it.Key()), "/profile") {
				continue
			}
			val, vErr := it.Value()
			if vErr != nil {
				return fmt.Errorf("domain dependents: read profile: %w", vErr)
			}
			var p bongov1.UserProfile
			if uErr := proto.Unmarshal(val, &p); uErr != nil {
				return fmt.Errorf("domain dependents: decode profile: %w", uErr)
			}
			if strings.HasSuffix(strings.ToLower(p.Email), suffix) {
				out.Users++
			}
		}
		return nil
	}()
	if err != nil {
		return out, err
	}

	ai, err := ps.PrefixScan([]byte("/system/alias/"))
	if err != nil {
		return out, fmt.Errorf("domain dependents: scan aliases: %w", err)
	}
	err = func() (rErr error) {
		defer closeIter(ai, &rErr)
		for ai.Next() {
			addr := strings.TrimPrefix(string(ai.Key()), "/system/alias/")
			if strings.HasSuffix(strings.ToLower(addr), suffix) {
				out.Aliases++
			}
		}
		return nil
	}()
	if err != nil {
		return out, fmt.Errorf("domain dependents: scan aliases: %w", err)
	}

	recs, err := domainAddressRecords(ps, suffix)
	if err != nil {
		return out, err
	}
	for _, r := range recs {
		if !r.live {
			continue
		}
		out.Addresses++
		if r.kind == "identity" {
			out.Identities++
		}
	}
	return out, nil
}

type domainAddressRecord struct {
	key  []byte
	kind string
	live bool
}

// domainAddressRecords returns every /system/address/ record ending in suffix.
func domainAddressRecords(ps *PebbleStore, suffix string) (out []domainAddressRecord, rErr error) {
	it, err := ps.PrefixScan([]byte("/system/address/"))
	if err != nil {
		return nil, fmt.Errorf("domain addresses: scan: %w", err)
	}
	defer closeIter(it, &rErr)

	for it.Next() {
		addr := strings.TrimPrefix(string(it.Key()), "/system/address/")
		if !strings.HasSuffix(strings.ToLower(addr), suffix) {
			continue
		}
		val, vErr := it.Value()
		if vErr != nil {
			return nil, fmt.Errorf("domain addresses: read %q: %w", addr, vErr)
		}
		var rec bongov1.AddressRecord
		if uErr := proto.Unmarshal(val, &rec); uErr != nil {
			return nil, fmt.Errorf("domain addresses: decode %q: %w", addr, uErr)
		}
		out = append(out, domainAddressRecord{
			key:  append([]byte(nil), it.Key()...),
			kind: rec.Kind,
			live: userProfileExists(ps, rec.UserId),
		})
	}
	return out, nil
}

// DomainKeyExists reports whether a /system/domain/{name} key exists for the
// domain under any capitalisation. Older deployments stored the name as typed.
func DomainKeyExists(ps *PebbleStore, domain string) (exists bool, rErr error) {
	domain = strings.TrimSpace(domain)
	it, err := ps.PrefixScan([]byte("/system/domain/"))
	if err != nil {
		return false, fmt.Errorf("domain key exists: scan: %w", err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		name := strings.TrimPrefix(string(it.Key()), "/system/domain/")
		if !strings.Contains(name, "/") && strings.EqualFold(name, domain) {
			return true, nil
		}
	}
	return false, nil
}

// PurgeDomainConfig removes the domain-owned configuration through Raft, one
// command per key: domain-admin grants, everything under /system/domain/{d}/
// (owner, dkim, quota, usage), the catch-all, send-as claims on the domain
// (/user/{id}/addrverify/{addr}, every user's), and address records whose user
// no longer exists. Records of live users are left alone. It never touches
// the /system/domain/{d} key itself or any blob.
//
// There is no composite Raft command, so a failure part-way leaves the
// remaining keys in place. The first error is returned after every key has
// been attempted, so callers can retry; the failing keys are in the error.
func PurgeDomainConfig(ps *PebbleStore, applier RaftApplier, domain string) error {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "" {
		return errors.New("purge domain config: empty domain")
	}

	var keys [][]byte
	// Legacy deployments stored the domain name as typed, and GenerateDKIM
	// wrote /system/domain/{raw}/dkim, so match the sub-key prefix
	// case-insensitively rather than by the lowercased name alone.
	sub, err := domainSubKeys(ps, d)
	if err != nil {
		return fmt.Errorf("purge domain config: %w", err)
	}
	keys = append(keys, sub...)
	for _, prefix := range []string{"/system/domain_admin/" + d + "/"} {
		grants, err := scanKeys(ps, prefix)
		if err != nil {
			return fmt.Errorf("purge domain config: scan %q: %w", prefix, err)
		}
		keys = append(keys, grants...)
	}
	keys = append(keys, CatchAllKey(d))

	recs, err := domainAddressRecords(ps, "@"+d)
	if err != nil {
		return fmt.Errorf("purge domain config: %w", err)
	}
	for _, r := range recs {
		if !r.live {
			keys = append(keys, r.key)
		}
	}

	// Send-as claims (any user's) for addresses on the domain: they would
	// otherwise stay listed on the settings page of whoever made them.
	claims, err := domainClaimKeys(ps, "@"+d)
	if err != nil {
		return fmt.Errorf("purge domain config: %w", err)
	}
	keys = append(keys, claims...)

	var errs []error
	for _, k := range keys {
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: k}
		if aErr := applier.ApplyCommand(cmd, 5*time.Second); aErr != nil {
			errs = append(errs, fmt.Errorf("delete %q: %w", k, aErr))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("purge domain config %q: %w", d, errors.Join(errs...))
	}
	return nil
}

// domainSubKeys returns every /system/domain/{name}/... key whose name equals
// domain ignoring case.
func domainSubKeys(ps *PebbleStore, domain string) (out [][]byte, rErr error) {
	it, err := ps.PrefixScan([]byte("/system/domain/"))
	if err != nil {
		return nil, fmt.Errorf("scan domain sub-keys: %w", err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		name, _, found := strings.Cut(strings.TrimPrefix(string(it.Key()), "/system/domain/"), "/")
		if found && strings.EqualFold(name, domain) {
			out = append(out, append([]byte(nil), it.Key()...))
		}
	}
	return out, nil
}

// scanKeys returns a copy of every key under prefix.
func scanKeys(ps *PebbleStore, prefix string) (out [][]byte, rErr error) {
	it, err := ps.PrefixScan([]byte(prefix))
	if err != nil {
		return nil, err
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		out = append(out, append([]byte(nil), it.Key()...))
	}
	return out, nil
}

// closeIter closes it and folds a close failure into *dst. The Iterator
// interface exposes no iteration error, so Close is the only place a failed
// scan can surface.
func closeIter(it Iterator, dst *error) {
	if cErr := it.Close(); cErr != nil {
		*dst = errors.Join(*dst, fmt.Errorf("close iterator: %w", cErr))
	}
}

// domainClaimKeys returns every send-as claim key, /user/{id}/addrverify/{addr},
// whose address ends in suffix (e.g. "@example.com"). The key is split into
// exact segments so a mail guid or mailbox name containing "addrverify", or a
// /fwdverify/ key, never matches; the "@" in the suffix keeps subdomains out.
// It is one prefix scan of /user/, the same cost DomainDependentCounts pays.
func domainClaimKeys(ps *PebbleStore, suffix string) (out [][]byte, rErr error) {
	it, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scan claims: %w", err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		parts := strings.SplitN(strings.TrimPrefix(string(it.Key()), "/user/"), "/", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] != "addrverify" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(parts[2]), suffix) {
			out = append(out, append([]byte(nil), it.Key()...))
		}
	}
	return out, nil
}
