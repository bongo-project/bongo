package store

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CanSendAs reports whether userID may send mail whose From address is addr.
// It is the single send-as rule shared by web compose and authenticated SMTP
// submission: an address the user owns (CanSendAsOwn), or the address of a
// shared inbox the user is a member of. Membership only widens sending: it
// never makes the shared address the user's own (see CanSendAsOwn).
//
// primaryEmail may be empty when the caller has no cached primary. A nil
// AddressStore (edge/agent nodes) permits only the primary address.
func CanSendAs(as *AddressStore, userID, primaryEmail, addr string) (bool, error) {
	own, err := CanSendAsOwn(as, userID, primaryEmail, addr)
	if err != nil || own {
		return own, err
	}
	addr = NormaliseAddress(addr)
	if as == nil || as.pebble == nil || addr == "" {
		return false, nil
	}
	rec, ok, err := as.Get(addr)
	if err != nil {
		return false, fmt.Errorf("send-as lookup %q: %w", addr, err)
	}
	if !ok || rec.Kind == AddressKindGroup || rec.UserId == userID || !rec.CanSend || !rec.Verified {
		return false, nil
	}
	// A shared inbox's address is open to its members. The member list is read
	// from the account's profile on every call; folder rights never count.
	return sharedMember(as.pebble, rec.UserId, userID)
}

// CanSendAsOwn reports whether addr is the user's own sending address: their
// primary address, or an AddressRecord owned by the user with CanSend and
// Verified both set. It excludes shared inbox addresses reached only through
// membership, so callers that ask "is this me?" (reply-all, invitations,
// vacation, attendee counting) are not widened by membership.
func CanSendAsOwn(as *AddressStore, userID, primaryEmail, addr string) (bool, error) {
	addr = NormaliseAddress(addr)
	if addr == "" {
		return false, nil
	}
	if primaryEmail != "" && addr == NormaliseAddress(primaryEmail) {
		return true, nil
	}
	if as == nil {
		return false, nil
	}
	rec, ok, err := as.Get(addr)
	if err != nil {
		return false, fmt.Errorf("send-as lookup %q: %w", addr, err)
	}
	// A group address is never a sender: a group record has no owner, and
	// the kind is checked explicitly so that stays true if that changes.
	if ok && rec.Kind == AddressKindGroup {
		return false, nil
	}
	return ok && rec.UserId == userID && rec.CanSend && rec.Verified, nil
}

// SendAsPolicy adapts CanSendAs for the SMTP submission path. The primary
// address is read from the user's profile rather than trusted from the AUTH
// username, which may differ in case from the stored address.
type SendAsPolicy struct {
	Addresses *AddressStore
	Pebble    *PebbleStore

	// get reads a Pebble key. It is nil in production (Pebble.Get is used);
	// tests set it to inject read failures, since a closed PebbleStore panics.
	get func(key []byte) ([]byte, error)
}

// CanSendAs reports whether userID may send as addr. A missing profile means
// the account is gone and nothing is allowed; any other store error is
// returned so the caller can fail closed with a retryable status.
func (p *SendAsPolicy) CanSendAs(userID, addr string) (bool, error) {
	return p.check(userID, addr, CanSendAs)
}

// CanSendAsOwn is CanSendAs without shared inbox membership: whether addr is one
// of the user's own addresses.
func (p *SendAsPolicy) CanSendAsOwn(userID, addr string) (bool, error) {
	return p.check(userID, addr, CanSendAsOwn)
}

func (p *SendAsPolicy) check(userID, addr string, rule func(*AddressStore, string, string, string) (bool, error)) (bool, error) {
	if p == nil || p.Pebble == nil || userID == "" {
		return false, errors.New("send-as policy unavailable")
	}
	get := p.get
	if get == nil {
		get = p.Pebble.Get
	}
	data, err := get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("load profile for send-as: %w", err)
	}
	var prof bongov1.UserProfile
	if err := proto.Unmarshal(data, &prof); err != nil {
		return false, fmt.Errorf("decode profile for send-as: %w", err)
	}
	return rule(p.Addresses, userID, strings.TrimSpace(prof.GetEmail()), addr)
}
