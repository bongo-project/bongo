package imap

import (
	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/store"
)

// quotaRootUser is the one quota root: the user's own storage limit
// (UserProfile.quota_bytes). The domain quota is never reported to an ordinary
// user, so a user under their own limit can still be refused an APPEND because
// the domain is full while GETQUOTA shows room.
const quotaRootUser = "user"

var errNoSuchQuotaRoot = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeNonExistent,
	Text: "No such quota root",
}

// QuotaResourceTypes implements imapserver.SessionQuota. Only STORAGE is
// tracked. SETQUOTA is refused by the library and QUOTASET is not advertised.
func (s *session) QuotaResourceTypes() []imap.QuotaResourceType {
	return []imap.QuotaResourceType{imap.QuotaResourceStorage}
}

// userQuotaData reads the user's figure straight from the store, not through
// s.quota, which is nil in dev and in tests and would let GETQUOTA disagree
// with what the delivery check enforces. It returns nil when the user has no
// quota.
func (s *session) userQuotaData() (*imap.QuotaData, error) {
	limit, usage, err := store.UserStorageQuota(s.pebble, s.userID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	return &imap.QuotaData{
		Root: quotaRootUser,
		Resources: map[imap.QuotaResourceType]imap.QuotaResourceData{
			imap.QuotaResourceStorage: {Usage: ceilKiB(usage), Limit: ceilKiB(limit)},
		},
	}, nil
}

// GetQuota implements imapserver.SessionQuota. RFC 9208 needs at least one
// limited resource in a root, so a user without a quota has no root to name.
func (s *session) GetQuota(root string) (*imap.QuotaData, error) {
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	if root != quotaRootUser {
		return nil, errNoSuchQuotaRoot
	}
	data, err := s.userQuotaData()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errNoSuchQuotaRoot
	}
	return data, nil
}

// GetQuotaRoot implements imapserver.SessionQuota. Other users' and shared
// names get no root, so another account's figures are never shown.
func (s *session) GetQuotaRoot(mailbox string) ([]imap.QuotaData, error) {
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	// A shared folder goes through the resolver: not found without the lookup
	// right, and no root with it, so another account's figures are never shown.
	if s.ns().IsOtherUsersName(mailbox) {
		if _, err := s.resolveMailbox(mailbox, errNoSuchMailbox); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if s.namespaces.OrDefaults().Reserves(mailbox) {
		return nil, nil
	}
	if _, err := s.mailboxes.GetMailbox(s.userID, mailbox); err != nil {
		return nil, mailboxLookupError(mailbox, err, errNoSuchMailbox)
	}
	data, err := s.userQuotaData()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	return []imap.QuotaData{*data}, nil
}

// ceilKiB converts octets to the 1024-octet units RFC 9208 uses for STORAGE,
// rounding up. Usage can be clamped to MaxInt64, so the usual (n+1023)/1024
// would wrap.
func ceilKiB(n int64) int64 {
	if n <= 0 {
		return 0
	}
	k := n / 1024
	if n%1024 != 0 {
		k++
	}
	return k
}
