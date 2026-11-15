package store

import (
	"errors"
	"fmt"
	"net/textproto"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// GroupLoopHeader is stamped on the copies a group expansion delivers. A
// message that already carries this header with the group's id is not expanded
// again, which breaks a loop where a member forwards group mail back to the
// group.
//
// The header is plain text in mail a member can forward, so a person who learns
// a group id can suppress that group's expansion by adding it. The effect is
// limited to that one group's delivery; the Received hop limit remains the
// backstop for loops that do not involve it.
const GroupLoopHeader = "X-Bongo-Group"

// GroupMember is one deliverable member of an expanded group.
type GroupMember struct {
	UserID  string
	Address string // the member's primary address, lower case
}

// ExpandMembers returns the group and its deliverable members in member-list
// order. A member whose profile is gone, who is disabled or who is a resource
// is left out; the caller logs the difference between len(group.MemberIds) and
// the result. A missing group returns ErrGroupNotFound.
//
// Address is the profile address, which ResolveRecipient maps back to the
// member, so the pipeline's deliver stage resolves the envelope recipient to
// the same user.
func (s *GroupStore) ExpandMembers(groupID string) (*bongov1.Group, []GroupMember, error) {
	g, err := s.GetGroup(groupID)
	if err != nil {
		return nil, nil, err
	}
	members := make([]GroupMember, 0, len(g.MemberIds))
	for _, id := range g.MemberIds {
		data, err := s.pebble.Get([]byte("/user/" + id + "/profile"))
		if errors.Is(err, ErrNotFound) || (err == nil && len(data) == 0) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("reading profile %s: %w", id, err)
		}
		var p bongov1.UserProfile
		if err := proto.Unmarshal(data, &p); err != nil {
			return nil, nil, fmt.Errorf("decoding profile %s: %w", id, err)
		}
		addr := NormaliseAddress(p.Email)
		if p.Disabled || p.Kind == "resource" || addr == "" {
			continue
		}
		members = append(members, GroupMember{UserID: id, Address: addr})
	}
	return g, members, nil
}

// ErrGroupPostingRestricted is returned when the group's posting policy does
// not let the sender post. Read failures are wrapped errors that do not match
// it, so a caller can tell a refusal from a fault.
var ErrGroupPostingRestricted = errors.New("posting to this group is restricted")

// postingAllowed decides the posting policy for an authenticated account.
// userID is empty for mail from outside (MX), which no policy but "anyone"
// admits. userEmail is the account's primary address; only "domain" reads it.
// An unknown policy refuses, so a value written by a newer binary fails closed.
func postingAllowed(g *bongov1.Group, userID, userEmail string) error {
	switch g.PostingPolicy {
	case "", GroupPolicyAnyone:
		return nil
	case GroupPolicyOwners:
		if userID != "" && slices.Contains(g.OwnerIds, userID) {
			return nil
		}
	case GroupPolicyMembers:
		if userID != "" && (slices.Contains(g.MemberIds, userID) || slices.Contains(g.OwnerIds, userID)) {
			return nil
		}
	case GroupPolicyDomain:
		if userID == "" || g.Domain == "" {
			break
		}
		_, domain, ok := strings.Cut(NormaliseAddress(userEmail), "@")
		if ok && domain != "" && domain == strings.ToLower(g.Domain) {
			return nil
		}
	}
	return ErrGroupPostingRestricted
}

// PostingAllowed is the check every expansion site makes before it delivers to
// a group. userID is the authenticated account, or "" for mail from outside.
// It takes no address on purpose: the envelope sender and the From header cost
// nothing to forge, so neither may decide.
//
// "domain" means the account's primary address is in the group's domain.
// Send-as addresses, aliases and alias domains do not count. A missing profile
// is a refusal; a read fault is a wrapped error that is not
// ErrGroupPostingRestricted.
func (s *GroupStore) PostingAllowed(g *bongov1.Group, userID string) error {
	email := ""
	if g.PostingPolicy == GroupPolicyDomain && userID != "" {
		data, err := s.pebble.Get([]byte("/user/" + userID + "/profile"))
		if errors.Is(err, ErrNotFound) || (err == nil && len(data) == 0) {
			return ErrGroupPostingRestricted
		}
		if err != nil {
			return fmt.Errorf("reading profile %s: %w", userID, err)
		}
		var p bongov1.UserProfile
		if err := proto.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("decoding profile %s: %w", userID, err)
		}
		if p.Disabled {
			return ErrGroupPostingRestricted
		}
		email = p.Email
	}
	return postingAllowed(g, userID, email)
}

// HeaderHasGroup reports whether h carries GroupLoopHeader naming groupID, in
// any instance of the header. Values are compared trimmed and lower case.
func HeaderHasGroup(h textproto.MIMEHeader, groupID string) bool {
	want := strings.ToLower(strings.TrimSpace(groupID))
	if want == "" {
		return false
	}
	for _, v := range h.Values(GroupLoopHeader) {
		if strings.ToLower(strings.TrimSpace(v)) == want {
			return true
		}
	}
	return false
}
