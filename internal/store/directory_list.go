package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// DirectoryListLimit is the most rows one directory listing returns. The
// listings are bounded by stored data, not by request input, so this cap only
// stops a pathological store from producing an unbounded response. Rows are
// sorted before the cap is applied, so the cut is deterministic.
const DirectoryListLimit = 100000

// UserSummary is the operator-facing view of an account. It is a dedicated
// DTO and never embeds bongov1.UserProfile: the profile carries the password
// hash, TOTP secret, recovery codes and session counters, and a DTO with only
// the fields below cannot leak them through JSON.
type UserSummary struct {
	ID           string   `json:"id"`
	Email        string   `json:"email"`
	DisplayName  string   `json:"display_name"`
	Disabled     bool     `json:"disabled"`
	Admin        bool     `json:"admin"`
	AdminDomains []string `json:"admin_domains"`
}

// DomainSummary is the operator-facing view of a hosted domain. DKIM is an
// existence flag only; the DomainRecord holding the private key is never
// decoded into it.
type DomainSummary struct {
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	AliasOf         string `json:"alias_of"`
	CatchAll        string `json:"catch_all"`
	MaxUsers        int64  `json:"max_users"`
	MaxStorageBytes int64  `json:"max_storage_bytes"`
	UsageBytes      int64  `json:"usage_bytes"`
	DKIM            bool   `json:"dkim"`
}

// AddressAliasSummary is one address-alias entry. Target is the stored value
// as is.
type AddressAliasSummary struct {
	Address string `json:"address"`
	Target  string `json:"target"`
}

// NormaliseDirectoryDomain lowercases and trims a --domain filter, strips one
// trailing dot, and reports whether it is acceptable (blank means no filter):
// at most 253 bytes and free of '/', '@', whitespace and control characters.
// The filter is only ever compared against in memory, never turned into a key.
func NormaliseDirectoryDomain(raw string) (string, bool) {
	d := strings.ToLower(strings.TrimSpace(raw))
	if d == "" {
		return "", true
	}
	// One trailing dot (the fully qualified form) is accepted; a filter that
	// is then empty or still dotted must not widen to "no filter".
	d = strings.TrimSuffix(d, ".")
	if d == "" || strings.HasSuffix(d, ".") {
		return "", false
	}
	if len(d) > 253 || strings.ContainsAny(d, "/@") {
		return "", false
	}
	for _, r := range d {
		if r <= ' ' || r == 0x7f {
			return "", false
		}
	}
	return d, true
}

// directoryScan calls fn for every key/value under prefix, failing closed on
// any read error.
func directoryScan(ps *PebbleStore, prefix string, fn func(key string, val []byte) error) (rErr error) {
	it, err := ps.PrefixScan([]byte(prefix))
	if err != nil {
		return fmt.Errorf("scan %s: %w", prefix, err)
	}
	defer closeIter(it, &rErr)
	for it.Next() {
		val, vErr := it.Value()
		if vErr != nil {
			return fmt.Errorf("read %s: %w", string(it.Key()), vErr)
		}
		if err := fn(string(it.Key()), val); err != nil {
			return err
		}
	}
	return nil
}

func domainSuffix(domain string) string {
	if domain == "" {
		return ""
	}
	return "@" + domain
}

func addrOnDomain(addr, suffix string) bool {
	return suffix == "" || strings.HasSuffix(strings.ToLower(addr), suffix)
}

// ListUserSummaries lists accounts from Pebble (the source of truth, not the
// per-node SQLite index), sorted by email. domain, already normalised, limits
// the list to addresses ending in "@domain". The bool reports truncation at
// DirectoryListLimit.
//
// It scans all of /user/, which walks every mail and mailbox key as
// CountUsersForDomain does; acceptable for an operator command.
func ListUserSummaries(ps *PebbleStore, domain string) ([]UserSummary, bool, error) {
	suffix := domainSuffix(domain)

	admins := map[string]map[string]struct{}{} // user ID -> domains
	addAdmin := func(uid, d string) {
		if uid == "" || d == "" {
			return
		}
		if admins[uid] == nil {
			admins[uid] = map[string]struct{}{}
		}
		admins[uid][strings.ToLower(d)] = struct{}{}
	}
	err := directoryScan(ps, "/system/domain_admin/", func(key string, _ []byte) error {
		d, uid, ok := strings.Cut(strings.TrimPrefix(key, "/system/domain_admin/"), "/")
		if ok && !strings.Contains(uid, "/") {
			addAdmin(uid, d)
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("list users: domain admin grants: %w", err)
	}
	err = directoryScan(ps, domainPrefix, func(key string, val []byte) error {
		if d, ok := strings.CutSuffix(strings.TrimPrefix(key, domainPrefix), "/owner"); ok && !strings.Contains(d, "/") {
			addAdmin(string(val), d)
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("list users: domain owners: %w", err)
	}

	var out []UserSummary
	err = directoryScan(ps, "/user/", func(key string, val []byte) error {
		id, rest, ok := strings.Cut(strings.TrimPrefix(key, "/user/"), "/")
		if !ok || rest != "profile" {
			return nil
		}
		var p bongov1.UserProfile
		if err := proto.Unmarshal(val, &p); err != nil {
			return fmt.Errorf("decode profile %s: %w", id, err)
		}
		if !addrOnDomain(p.Email, suffix) {
			return nil
		}
		doms := make([]string, 0, len(admins[id]))
		for d := range admins[id] {
			doms = append(doms, d)
		}
		slices.Sort(doms)
		out = append(out, UserSummary{
			ID: id, Email: p.Email, DisplayName: p.DisplayName,
			Disabled: p.Disabled, Admin: p.IsAdmin, AdminDomains: doms,
		})
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("list users: %w", err)
	}
	slices.SortFunc(out, func(a, b UserSummary) int {
		if c := strings.Compare(strings.ToLower(a.Email), strings.ToLower(b.Email)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	out, trunc := capRows(out)
	return out, trunc, nil
}

func capRows[T any](rows []T) ([]T, bool) {
	if len(rows) > DirectoryListLimit {
		return rows[:DirectoryListLimit], true
	}
	return rows, false
}

// hostedDomain is one /system/domain/{raw} record; name is raw lowercased.
type hostedDomain struct{ name, raw, state string }

// dedupeHostedDomains sorts by name and keeps one record per name. The exact
// lowercase key wins because domainState reads only that key; failing that,
// the lowest raw key, so the result never depends on input order.
func dedupeHostedDomains(domains []hostedDomain) []hostedDomain {
	slices.SortFunc(domains, func(a, b hostedDomain) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if (a.raw == a.name) != (b.raw == b.name) {
			if a.raw == a.name {
				return -1
			}
			return 1
		}
		return strings.Compare(a.raw, b.raw)
	})
	return slices.CompactFunc(domains, func(a, b hostedDomain) bool { return a.name == b.name })
}

// ListDomainSummaries lists hosted domains (plain /system/domain/{d} keys),
// sorted by name. A catch-all whose account is gone is shown as the raw user
// ID with a "(missing account)" marker rather than failing. DKIM is true when
// a /dkim key exists under any casing of the name, since rotations before
// keys were lowercased wrote mixed-case keys.
func ListDomainSummaries(ps *PebbleStore) ([]DomainSummary, bool, error) {
	var domains []hostedDomain
	dkim := map[string]bool{}
	err := directoryScan(ps, domainPrefix, func(key string, val []byte) error {
		name := strings.TrimPrefix(key, domainPrefix)
		if d, ok := strings.CutSuffix(name, "/dkim"); ok && !strings.Contains(d, "/") {
			dkim[strings.ToLower(d)] = true
			return nil
		}
		if name == "" || strings.Contains(name, "/") {
			return nil
		}
		domains = append(domains, hostedDomain{name: strings.ToLower(name), raw: name, state: string(val)})
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("list domains: %w", err)
	}
	domains = dedupeHostedDomains(domains)
	domains, trunc := capRows(domains)

	targets, err := AliasDomains(ps)
	if err != nil {
		return nil, false, fmt.Errorf("list domains: %w", err)
	}
	quotas := NewDomainQuotaStore(ps, nil)

	out := make([]DomainSummary, 0, len(domains))
	for _, d := range domains {
		q, err := quotas.GetQuota(d.name)
		if err != nil {
			return nil, false, fmt.Errorf("list domains: %w", err)
		}
		usage, err := quotas.GetUsage(d.name)
		if err != nil {
			return nil, false, fmt.Errorf("list domains: %w", err)
		}
		catchAll, err := catchAllLabel(ps, d.name)
		if err != nil {
			return nil, false, fmt.Errorf("list domains: %w", err)
		}
		out = append(out, DomainSummary{
			Name: d.name, Enabled: d.state != "disabled", AliasOf: targets[d.name],
			CatchAll: catchAll, MaxUsers: q.MaxUsers, MaxStorageBytes: q.MaxStorageBytes,
			UsageBytes: usage, DKIM: dkim[d.name],
		})
	}
	return out, trunc, nil
}

// catchAllLabel resolves the catch-all user ID of domain to an email.
func catchAllLabel(ps *PebbleStore, domain string) (string, error) {
	data, err := ps.Get(CatchAllKey(domain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("read catch-all for %s: %w", domain, err)
	}
	uid := string(data)
	if uid == "" {
		return "", nil
	}
	pd, err := ps.Get([]byte("/user/" + uid + "/profile"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return uid + " (missing account)", nil
		}
		return "", fmt.Errorf("read catch-all account for %s: %w", domain, err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(pd, &p); err != nil {
		return "", fmt.Errorf("decode catch-all account for %s: %w", domain, err)
	}
	return p.Email, nil
}

// ListAddressAliases lists address-alias entries, sorted by address. These are
// not domain aliases (see ListDomainSummaries). domain, already normalised,
// limits the list to addresses ending in "@domain".
func ListAddressAliases(ps *PebbleStore, domain string) ([]AddressAliasSummary, bool, error) {
	suffix := domainSuffix(domain)
	var out []AddressAliasSummary
	err := directoryScan(ps, "/system/alias/", func(key string, val []byte) error {
		addr := strings.TrimPrefix(key, "/system/alias/")
		if addrOnDomain(addr, suffix) {
			out = append(out, AddressAliasSummary{Address: addr, Target: string(val)})
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("list address aliases: %w", err)
	}
	slices.SortFunc(out, func(a, b AddressAliasSummary) int { return strings.Compare(a.Address, b.Address) })
	out, trunc := capRows(out)
	return out, trunc, nil
}
