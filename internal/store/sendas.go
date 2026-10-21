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
// submission: the user's primary address, or an AddressRecord owned by the
// user with CanSend and Verified both set.
//
// primaryEmail may be empty when the caller has no cached primary. A nil
// AddressStore (edge/agent nodes) permits only the primary address.
func CanSendAs(as *AddressStore, userID, primaryEmail, addr string) (bool, error) {
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
	return CanSendAs(p.Addresses, userID, strings.TrimSpace(prof.GetEmail()), addr)
}
