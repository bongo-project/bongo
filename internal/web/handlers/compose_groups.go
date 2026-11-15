package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// groupExpander lists the deliverable members of a distribution group.
// *store.GroupStore satisfies it.
type groupExpander interface {
	ExpandMembers(groupID string) (*bongov1.Group, []store.GroupMember, error)
	PostingAllowed(g *bongov1.Group, userID string) error
}

// SetGroups sets the expander used for recipients that are group addresses.
// Without one, a message addressed to a group is refused: it must never be
// handed to the outbound queue, which would send it out and back in through
// this server's own MX.
func (h *ComposeHandler) SetGroups(g groupExpander) { h.groups = g }

// Groups returns the group expander, or nil.
func (h *ComposeHandler) Groups() groupExpander { return h.groups }

// SetGroupFanoutLimiter sets the per-account cap on member copies made by
// group expansion, shared with SMTP submission. nil disables it.
func (h *ComposeHandler) SetGroupFanoutLimiter(l *mail.FanoutLimiter) { h.groupFanout = l }

// GroupFanoutLimiter returns the fan-out limiter, or nil.
func (h *ComposeHandler) GroupFanoutLimiter() *mail.FanoutLimiter { return h.groupFanout }

// composeGroup is a recipient that is a group address.
type composeGroup struct{ addr, id string }

// composeMember is one member copy a group expansion adds.
type composeMember struct{ addr, userID string }

var (
	errGroupsUnavailable = errors.New("group delivery is not configured")
	errGroupEmpty        = errors.New("group has no member who can receive mail")
)

// groupOf reports the group a bare address belongs to. It keeps the resolver
// error that resolveLocalUser discards, which is how a group address is told
// apart from an external one.
func (h *ComposeHandler) groupOf(rcpt string) (string, bool) {
	if h.resolver == nil {
		return "", false
	}
	_, err := h.resolver.ResolveRecipient(rcpt)
	var gre *store.GroupRecipientError
	if errors.As(err, &gre) {
		return gre.GroupID, true
	}
	return "", false
}

// groupFanoutKey is the key the fan-out cap counts under for an account. SMTP
// submission uses the same key.
func groupFanoutKey(userID string) string { return "u:" + userID }

// expandedGroup is one group address and every deliverable member it lists,
// including members another recipient also reaches.
type expandedGroup struct {
	addr, id string
	members  []composeMember
}

// groupOverQuotaError reports a group none of whose members can take the
// message: every member it would add is over storage quota and no other
// recipient already reaches one. SMTP answers 552 for the same case.
type groupOverQuotaError struct{ addr string }

func (e *groupOverQuotaError) Error() string {
	return "every deliverable member of " + e.addr + " is over storage quota"
}

// groupRestrictedError reports a group whose posting policy refuses the sender.
type groupRestrictedError struct{ addr, groupID string }

func (e *groupRestrictedError) Error() string {
	return "posting to " + e.addr + " is restricted"
}

// expandGroups lists the members of each distinct group. A group that is
// gone, refused by the posting policy or has no live member is an error; no
// member copies are chosen here (see pickMembers).
//
// The posting policy is decided on senderID, the session account. The From
// field may be a send-as address a user can set freely, so it is not passed.
// A refusal fails the whole send before any write, as for a group that is gone
// or has no live member.
func (h *ComposeHandler) expandGroups(senderID string, groups []composeGroup) ([]expandedGroup, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	if h.groups == nil {
		return nil, errGroupsUnavailable
	}
	var out []expandedGroup
	seen := map[string]bool{}
	for _, g := range groups {
		if seen[g.id] {
			continue
		}
		seen[g.id] = true
		grp, ms, eerr := h.groups.ExpandMembers(g.id)
		if errors.Is(eerr, store.ErrGroupNotFound) {
			h.logger.Warn("groups: compose to a group that is gone", "group", g.id)
			return nil, errGroupEmpty
		}
		if eerr != nil {
			return nil, fmt.Errorf("expanding group %s: %w", g.id, eerr)
		}
		if perr := h.groups.PostingAllowed(grp, senderID); perr != nil {
			if errors.Is(perr, store.ErrGroupPostingRestricted) {
				// The stored address, not what the user typed.
				return nil, &groupRestrictedError{addr: grp.Address, groupID: grp.Id}
			}
			return nil, fmt.Errorf("checking posting policy for group %s: %w", g.id, perr)
		}
		if skipped := len(grp.MemberIds) - len(ms); skipped > 0 {
			h.logger.Info("groups: skipped members with no live account", "group", grp.Id, "skipped", skipped)
		}
		if len(ms) == 0 {
			return nil, errGroupEmpty
		}
		eg := expandedGroup{addr: g.addr, id: grp.Id}
		for _, m := range ms {
			eg.members = append(eg.members, composeMember{addr: m.Address, userID: m.UserID})
		}
		out = append(out, eg)
	}
	return out, nil
}

// pickMembers chooses the member copies to add, the way SMTP RCPT does: a
// member already reached (a user in have, or a member of an earlier group) is
// not added again, and a member skip reports (over quota) is left out. A
// group with no member left either way is a *groupOverQuotaError. have is not
// modified. skip may be nil.
func pickMembers(groups []expandedGroup, have map[string]bool, skip func(composeMember) bool) ([]composeMember, error) {
	reached := maps.Clone(have)
	if reached == nil {
		reached = map[string]bool{}
	}
	var members []composeMember
	for _, g := range groups {
		covered := 0
		for _, m := range g.members {
			if reached[m.userID] {
				covered++
				continue
			}
			if skip != nil && skip(m) {
				continue
			}
			covered++
			reached[m.userID] = true
			members = append(members, m)
		}
		if covered == 0 {
			return nil, &groupOverQuotaError{addr: g.addr}
		}
	}
	return members, nil
}

// localUserSet is the set of user IDs the direct local recipients reach.
func (h *ComposeHandler) localUserSet(direct []string) map[string]bool {
	have := map[string]bool{}
	for _, d := range direct {
		if id := h.resolveLocalUser(d); id != "" {
			have[id] = true
		}
	}
	return have
}

// groupIDsOf returns the ids of groups, for the loop header.
func groupIDsOf(groups []expandedGroup) []string {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.id
	}
	return ids
}

// groupQuotaText is the refusal for a group none of whose members can take
// the message.
func groupQuotaText(r *http.Request, group string) string {
	return fmt.Sprintf(i18n.T(middleware.LocaleFromContext(r.Context()), "compose.errGroupQuota"), group)
}

// putStamped stores raw with the group loop header for each id in front. It is
// the copy member envelopes name: the Sent copy and any external relay keep the
// original, so group ids stay out of both.
func (h *ComposeHandler) putStamped(raw []byte, ids []string) (string, error) {
	var b bytes.Buffer
	for _, id := range ids {
		b.WriteString(store.GroupLoopHeader + ": " + id + "\r\n")
	}
	b.Write(raw)
	hash, err := h.blobs.Put(b.Bytes())
	if err != nil {
		return "", fmt.Errorf("storing group copy: %w", err)
	}
	return hash, nil
}

// memberAddrs returns the addresses of members.
func memberAddrs(members []composeMember) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = m.addr
	}
	return out
}

// memberEnvID is the spool envelope id for a member copy of one send, derived
// like a direct local recipient's so a retry places it once.
func memberEnvID(userID, sendID, addr string) string {
	return derivedSendID(userID, sendID, "local:"+strings.ToLower(addr)).String()
}

func isGroupRestricted(err error) bool {
	_, ok := errors.AsType[*groupRestrictedError](err)
	return ok
}

// groupLister lists the groups of a domain. *store.GroupStore satisfies it.
type groupLister interface {
	ListGroups(domain string) ([]*bongov1.Group, error)
}

// matchGroups returns up to limit groups on the caller's own domain whose
// address, or a word of whose display name, starts with q. A caller with no
// usable domain gets none, so a failure never widens the audience.
func (h *ComposeHandler) matchGroups(q, callerEmail string, limit int) []*bongov1.Group {
	lister, ok := h.groups.(groupLister)
	if !ok || lister == nil {
		return nil
	}
	domain := domainOf(callerEmail)
	q = strings.ToLower(strings.TrimSpace(q))
	if domain == "" || q == "" {
		return nil
	}
	groups, err := lister.ListGroups(domain)
	if err != nil {
		return nil
	}
	var out []*bongov1.Group
	for _, g := range groups {
		if g.Domain != domain || !groupMatches(g, q) {
			continue
		}
		out = append(out, g)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func groupMatches(g *bongov1.Group, q string) bool {
	if strings.HasPrefix(strings.ToLower(g.Address), q) {
		return true
	}
	for _, word := range strings.Fields(strings.ToLower(g.DisplayName)) {
		if strings.HasPrefix(word, q) {
			return true
		}
	}
	return false
}
