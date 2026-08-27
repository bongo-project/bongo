package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// DomainQuotaKey returns the Pebble key for a per-domain quota record.
//
// /system/domain/{domain}/quota contains a DomainQuota protobuf.
// The domain is lowercased to ensure case-insensitive lookups.
// This key contains '/' after the prefix and is therefore skipped by the
// existing allManagedDomains and loadDomains prefix scans.
func DomainQuotaKey(domain string) []byte {
	return []byte("/system/domain/" + strings.ToLower(domain) + "/quota")
}

// DomainUsageKey returns the Pebble key for a per-domain aggregate usage record.
//
// /system/domain/{domain}/usage stores a little-endian int64 byte count
// (total raw_message blob bytes for all users whose primary domain is {domain}).
// Written only by the leader-only usage-recompute job; read by the SMTP RCPT path.
func DomainUsageKey(domain string) []byte {
	return []byte("/system/domain/" + strings.ToLower(domain) + "/usage")
}

// DomainQuotaStore manages per-domain quota limits and recomputed usage in
// Pebble via Raft.
//
// Two keys per domain:
//   - /system/domain/{d}/quota  → DomainQuota protobuf (max_users, max_storage_bytes)
//   - /system/domain/{d}/usage  → little-endian int64 (recomputed aggregate bytes)
//
// Both keys contain '/' and are therefore skipped by the existing /system/domain/
// prefix scans that filter out sub-keys (the guard: strings.Contains(name, "/")).
type DomainQuotaStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewDomainQuotaStore creates a DomainQuotaStore.
func NewDomainQuotaStore(pebble *PebbleStore, applier RaftApplier) *DomainQuotaStore {
	return &DomainQuotaStore{pebble: pebble, applier: applier}
}

// GetQuota retrieves the quota for a domain.
// Returns a zero-value DomainQuota (0=unlimited) when no quota is set.
// Returns (nil, err) on an unexpected Pebble error.
func (s *DomainQuotaStore) GetQuota(domain string) (*bongov1.DomainQuota, error) {
	data, err := s.pebble.Get(DomainQuotaKey(domain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &bongov1.DomainQuota{}, nil
		}
		return nil, fmt.Errorf("domain quota store get %q: %w", domain, err)
	}
	var q bongov1.DomainQuota
	if err := proto.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("domain quota store get %q: unmarshal: %w", domain, err)
	}
	return &q, nil
}

// SetQuota writes the quota for a domain via Raft.
// The domain is lowercased before writing.
func (s *DomainQuotaStore) SetQuota(domain string, q *bongov1.DomainQuota) error {
	data, err := proto.Marshal(q)
	if err != nil {
		return fmt.Errorf("domain quota store set %q: marshal: %w", domain, err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   DomainQuotaKey(domain),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("domain quota store set %q: raft: %w", domain, err)
	}
	return nil
}

// GetUsage retrieves the last recomputed aggregate storage usage for a domain.
// Returns 0 when no usage has been recorded yet (no recompute job has run).
func (s *DomainQuotaStore) GetUsage(domain string) (int64, error) {
	data, err := s.pebble.Get(DomainUsageKey(domain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("domain quota store get usage %q: %w", domain, err)
	}
	if len(data) < 8 {
		return 0, nil
	}
	// The value is stored as a little-endian uint64. A raw value above
	// MaxInt64 would convert to a NEGATIVE int64, and IsOverStorageQuota
	// compares `usage >= MaxStorageBytes` — so a negative usage silently
	// disables the quota for that domain rather than enforcing it. Clamp
	// instead, failing closed: an implausibly large stored value is treated
	// as "at the ceiling", never as "no usage".
	raw := binary.LittleEndian.Uint64(data[:8])
	if raw > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(raw), nil
}

// SetUsage writes the aggregate storage usage for a domain via Raft.
// Called by the leader-only usage-recompute job. Idempotent: always overwrites.
func (s *DomainQuotaStore) SetUsage(domain string, bytes int64) error {
	// Usage is a byte count, so a negative value is meaningless. Storing one
	// would round-trip through GetUsage as a huge uint64 and, before the clamp
	// there, back to a negative int64 that makes every quota check pass. Reject
	// it at the write side too, so the stored value is always sane.
	if bytes < 0 {
		bytes = 0
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(bytes))
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   DomainUsageKey(domain),
		Value: buf[:],
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("domain quota store set usage %q: raft: %w", domain, err)
	}
	return nil
}

// IsOverStorageQuota reports whether a domain's last-recomputed usage is at or
// over its configured max_storage_bytes limit.
//
// Returns (false, nil) when either the quota or usage is unset/zero (unlimited).
// Eventual consistency: usage is updated only when the recompute job runs.
func (s *DomainQuotaStore) IsOverStorageQuota(domain string) (bool, error) {
	q, err := s.GetQuota(domain)
	if err != nil {
		return false, fmt.Errorf("domain quota: check storage quota %q: %w", domain, err)
	}
	if q.MaxStorageBytes <= 0 {
		return false, nil // unlimited
	}
	usage, err := s.GetUsage(domain)
	if err != nil {
		return false, fmt.Errorf("domain quota: get usage %q: %w", domain, err)
	}
	return usage >= q.MaxStorageBytes, nil
}

// RecipientOverQuota implements smtp.QuotaChecker.
//
// It loads the user's profile to derive their primary domain (via
// DomainFromEmail), then calls IsOverStorageQuota. This is called after
// ResolveRecipient has already returned the userID so no duplicate resolution
// occurs.
//
// Returns (false, nil) when the domain has no storage quota set (unlimited) or
// no usage has been recorded. Returns (true, nil) when over quota (permanent
// reject). Returns (false, err) on transient Pebble error (caller fails open).
func (s *DomainQuotaStore) RecipientOverQuota(userID string) (bool, error) {
	data, err := s.pebble.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil // no profile = no quota enforcement (edge case)
		}
		return false, fmt.Errorf("domain quota recipient check: load profile %q: %w", userID, err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return false, fmt.Errorf("domain quota recipient check: unmarshal profile %q: %w", userID, err)
	}
	domain := strings.ToLower(DomainFromEmail(profile.Email))
	return s.IsOverStorageQuota(domain)
}

// CountUsersForDomain counts the number of users whose primary domain equals
// the given domain by scanning /user/{id}/profile entries in Pebble.
//
// It reads the authoritative source (Pebble, replicated by Raft) rather than
// the per-node SQLite users table, which may be stale on followers. Used to
// enforce max_users at user-creation time.
//
// NOTE: that enforcement is check-then-apply, not atomic — two concurrent
// creates can both pass the check. This matches the per-user aggregate limit
// convention (e.g. 100-tags/user) documented in CLAUDE.md.
func (s *DomainQuotaStore) CountUsersForDomain(domain string) (int, error) {
	domain = strings.ToLower(domain)
	iter, err := s.pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, fmt.Errorf("domain quota count users %q: scan: %w", domain, err)
	}
	defer iter.Close()

	count := 0
	for iter.Next() {
		key := string(iter.Key())
		// We only want /user/{id}/profile keys.
		rest := strings.TrimPrefix(key, "/user/")
		if !strings.HasSuffix(rest, "/profile") {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var profile bongov1.UserProfile
		if err := proto.Unmarshal(val, &profile); err != nil {
			continue
		}
		if strings.ToLower(DomainFromEmail(profile.Email)) == domain {
			count++
		}
	}
	return count, nil
}
