package store

import (
	"errors"
	"fmt"
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
// (raw_message bytes of all mail plus inline ical_raw and vcard_raw bytes of all
// events and contacts, for users whose primary domain is {domain}).
// Written only by the leader-only usage-recompute job (which also writes
// /user/{id}/usage from the same scan, so the users of a domain sum to its
// total); read by the SMTP RCPT,
// IMAP APPEND and CalDAV/CardDAV PUT paths.
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
	return decodeUsage(data), nil
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
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   DomainUsageKey(domain),
		Value: encodeUsage(bytes),
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
	return s.wouldExceedStorageQuota(domain, 0)
}

// storageWouldExceed is the one piece of quota arithmetic, shared by the domain
// and the per-user check. A limit of zero or less means no limit. Otherwise it
// reports whether usage is already at or over the limit, or whether adding
// addBytes would push it past (an exact fit is allowed). A negative add counts
// as zero. The subtraction form cannot overflow when usage is clamped to
// MaxInt64.
func storageWouldExceed(limit, usage, addBytes int64) bool {
	if limit <= 0 {
		return false
	}
	if addBytes < 0 {
		addBytes = 0
	}
	if usage >= limit {
		return true
	}
	return addBytes > limit-usage
}

// UserStorageQuota returns a user's own storage limit (UserProfile.quota_bytes,
// 0 for none) and their last recomputed usage. A missing profile, or a zero or
// negative limit, gives (0, 0, nil). It reads Pebble on every call, so IMAP
// QUOTA and the delivery check never disagree. It ignores the domain quota.
func UserStorageQuota(ps *PebbleStore, userID string) (limit, usage int64, err error) {
	profile, err := loadQuotaProfile(ps, userID)
	if err != nil {
		return 0, 0, err
	}
	if profile == nil || profile.QuotaBytes <= 0 {
		return 0, 0, nil
	}
	usage, err = GetUserUsage(ps, userID)
	if err != nil {
		return 0, 0, err
	}
	return profile.QuotaBytes, usage, nil
}

// loadQuotaProfile returns the user's profile, or nil when there is none.
func loadQuotaProfile(ps *PebbleStore, userID string) (*bongov1.UserProfile, error) {
	data, err := ps.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("domain quota recipient check: load profile %q: %w", userID, err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("domain quota recipient check: unmarshal profile %q: %w", userID, err)
	}
	return &profile, nil
}

// wouldExceedStorageQuota reports whether the domain is at or over its limit,
// or whether adding addBytes would push it past the limit.
func (s *DomainQuotaStore) wouldExceedStorageQuota(domain string, addBytes int64) (bool, error) {
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
	return storageWouldExceed(q.MaxStorageBytes, usage, addBytes), nil
}

// RecipientOverQuota is RecipientWouldExceedQuota with addBytes == 0. Production
// callers weigh the message size and use RecipientWouldExceedQuota directly.
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
	return s.RecipientWouldExceedQuota(userID, 0)
}

// RecipientWouldExceedQuota is the size-aware form of RecipientOverQuota: it
// reports true when the user is already at or over a limit, or when storing
// addBytes more would exceed it. With addBytes == 0 it is exactly
// RecipientOverQuota.
//
// Two limits apply: the user's own (UserProfile.quota_bytes, 0 for none) and
// the domain's max_storage_bytes. Either one refuses. Both compare against the
// figure the leader's usage recompute last wrote, which runs every
// usageRecomputeInterval (10 minutes), so a figure can be about that old, or
// one pass older when a pass is split over more than 5000 users. A user who
// frees space stays refused until the next pass, and a new user counts as 0
// until the first one. Concurrent writers can overshoot. This is a best-effort
// guard with no per-message Raft write. A user under their own limit can still
// be refused because the domain is full.
func (s *DomainQuotaStore) RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error) {
	profile, err := loadQuotaProfile(s.pebble, userID)
	if err != nil {
		return false, err
	}
	if profile == nil {
		return false, nil // no profile = no quota enforcement (edge case)
	}
	if profile.QuotaBytes > 0 {
		usage, err := GetUserUsage(s.pebble, userID)
		if err != nil {
			return false, fmt.Errorf("domain quota recipient check: %w", err)
		}
		if storageWouldExceed(profile.QuotaBytes, usage, addBytes) {
			return true, nil
		}
	}
	domain := strings.ToLower(DomainFromEmail(profile.Email))
	return s.wouldExceedStorageQuota(domain, addBytes)
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
