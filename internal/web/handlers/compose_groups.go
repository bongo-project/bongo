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

// expandGroups lists the members of each distinct group. A group that is
// gone, refused by the posting policy or has no live member is an error; no
// member copies are chosen here (see pickMembers).
//
// The posting policy is not enforced yet; GroupPostingAllowed is where it will
// be.
func (h *ComposeHandler) expandGroups(senderID, from string, groups []composeGroup) ([]expandedGroup, error) {
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
		if perr := store.GroupPostingAllowed(grp, senderID, from); perr != nil {
			return nil, perr
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
