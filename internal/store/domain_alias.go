package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// A domain alias is a hosted domain (its own /system/domain/{alias} key) that
// also carries /system/domain/{alias}/alias_of holding the target domain. Mail
// to local@alias that no tier resolves is retried once as local@target; see
// RecipientResolver. Aliasing grants receipt only, never send-as.

const domainPrefix = "/system/domain/"

// DomainAliasOfKey returns the Pebble key holding the target of an alias domain.
func DomainAliasOfKey(domain string) []byte {
	return []byte(domainPrefix + strings.ToLower(strings.TrimSpace(domain)) + "/alias_of")
}

// validAliasName reports whether name can be used as a domain in a Pebble key.
// A "/" would address another sub-key such as /system/domain/x/owner.
func validAliasName(name string) bool {
	return name != "" && !strings.Contains(name, "/")
}

// DomainAliasOf returns the target of alias domain d. ok is false when d has
// no alias_of key, d is empty or contains "/", or the stored target is empty or
// contains "/". An error is a read failure other than not-found.
func DomainAliasOf(ps domainGetter, d string) (target string, ok bool, err error) {
	d = strings.ToLower(strings.TrimSpace(d))
	if !validAliasName(d) {
		return "", false, nil
	}
	data, err := ps.Get(DomainAliasOfKey(d))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read alias_of for %s: %w", d, err)
	}
	target = strings.ToLower(strings.TrimSpace(string(data)))
	if !validAliasName(target) {
		return "", false, nil
	}
	return target, true, nil
}

// DomainAliases returns the sorted names of the domains whose alias_of is
// target. Scan errors are returned so callers fail closed.
func DomainAliases(ps *PebbleStore, target string) ([]string, error) {
	all, err := AliasDomains(ps)
	if err != nil {
		return nil, err
	}
	target = strings.ToLower(strings.TrimSpace(target))
	var out []string
	for name, t := range all {
		if t == target {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// AliasDomains returns every alias_of entry as alias -> target, for the admin
// listing and DomainAliases. Entries with an invalid target are skipped.
func AliasDomains(ps *PebbleStore) (out map[string]string, rErr error) {
	out = map[string]string{}
	it, err := ps.PrefixScan([]byte(domainPrefix))
	if err != nil {
		return nil, fmt.Errorf("scan domain aliases: %w", err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		name, ok := strings.CutSuffix(strings.TrimPrefix(string(it.Key()), domainPrefix), "/alias_of")
		if !ok || name == "" || strings.Contains(name, "/") {
			continue
		}
		val, vErr := it.Value()
		if vErr != nil {
			return nil, fmt.Errorf("read alias_of for %s: %w", name, vErr)
		}
		if t := strings.ToLower(strings.TrimSpace(string(val))); validAliasName(t) {
			out[strings.ToLower(name)] = t
		}
	}
	return out, nil
}
