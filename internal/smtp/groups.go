package smtp

import (
	"errors"
	netmail "net/mail"
	"net/textproto"
	"strings"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// GroupExpander lists the deliverable members of a distribution group.
// *store.GroupStore is the production implementation.
type GroupExpander interface {
	ExpandMembers(groupID string) (*bongov1.Group, []store.GroupMember, error)
}

// SetGroups sets the expander used for RCPT to a group address. With none set
// (edge and agent nodes) a group address is refused like any unavailable
// mailbox.
func (b *Backend) SetGroups(g GroupExpander) { b.groups = g }

// Groups returns the group expander, or nil.
func (b *Backend) Groups() GroupExpander { return b.groups }

// SetGroupFanoutLimiter sets the per-sender cap on member copies made by group
// expansion. It is separate from the external-recipient budget: a member is a
// local recipient. Share one limiter with web compose so both draw on the same
// allowance. nil disables the cap. The count is in memory and per node.
func (b *Backend) SetGroupFanoutLimiter(l *mail.FanoutLimiter) { b.groupFanout = l }

// GroupFanoutLimiter returns the fan-out limiter, or nil.
func (b *Backend) GroupFanoutLimiter() *mail.FanoutLimiter { return b.groupFanout }

// groupExpansion is one group addressed in the current transaction.
type groupExpansion struct {
	groupID string
	rcpt    string   // the group address as the client gave it
	users   []string // every deliverable member, including ones another recipient already added
}

// groupState is the per-transaction bookkeeping for group expansion. Reset
// clears it between messages.
//
// Only group-derived copies are de-duplicated. A member reached through a
// group is skipped when that user already has an envelope in the transaction
// (direct, or from another group). Direct recipients are never merged with one
// another: each RCPT keeps its own envelope, as before groups existed.
type groupState struct {
	// users holds every local user with an envelope in this transaction. For a
	// user in fanout the value is the index of that group copy in localRcpts;
	// for a direct recipient it is not used.
	users map[string]int
	// direct holds users addressed directly, so the loop check does not drop a
	// copy that was asked for by name.
	direct map[string]bool
	// fanout holds the users whose only envelope is a group copy. They are what
	// the fan-out cap is charged for at DATA.
	fanout map[string]bool
	groups []groupExpansion
	// pending is the number of member copies already peeked against the cap in
	// this transaction. Nothing is charged until DATA.
	pending int
}

func (g *groupState) reset() { *g = groupState{} }

// fanoutKey is the key the fan-out cap counts under: the account for an
// authenticated submission, the source IP otherwise.
//
// MX mail is keyed by source IP, not envelope sender, because a sender
// address costs nothing to forge. Behind a trusted upstream relay (or with
// proxy protocol off behind a TCP proxy) every sender shares the relay's IP and
// so one allowance; that is a limit of the key, not of the cap.
func (s *Session) fanoutKey() string {
	if s.authed && s.userID != "" {
		return "u:" + s.userID
	}
	if s.remoteIP != nil {
		return "ip:" + s.remoteIP.String()
	}
	return "ip:unknown"
}

// memberDSN derives a member copy's DSN parameters from the client's request
// for the group address. ORCPT names the group, so a report says which address
// the sender used. NOTIFY=SUCCESS and DELAY are not passed on: one request
// would otherwise cause a report per member (RFC 3461 section 6.2.7.3 allows
// a group to expand without them). A request left with nothing becomes NEVER,
// so a SUCCESS-only request does not turn into the default failure report.
func memberDSN(d RcptDSN, groupAddr string) RcptDSN {
	out := RcptDSN{ORCPTType: "rfc822", ORCPT: groupAddr}
	out.Notify = d.Notify &^ (mail.NotifySuccess | mail.NotifyDelay)
	if d.Notify != 0 && out.Notify == 0 {
		out.Notify = mail.NotifyNever
	}
	return out
}

func smtpErr(code, class, subject, detail int, msg string) *gosmtp.SMTPError {
	return &gosmtp.SMTPError{Code: code, EnhancedCode: gosmtp.EnhancedCode{class, subject, detail}, Message: msg}
}

// rcptGroup handles RCPT to a group address: it adds one local recipient per
// deliverable member not already in the transaction. Nothing is added unless
// the whole group is accepted, so a refused RCPT never leaves part of a group
// behind.
func (s *Session) rcptGroup(to string, gre *store.GroupRecipientError, dsn RcptDSN) error {
	g, members, err := s.backend.groups.ExpandMembers(gre.GroupID)
	if errors.Is(err, store.ErrGroupNotFound) {
		s.logger.Warn("groups: RCPT rejected: group is gone", "to", to, "group", gre.GroupID)
		return smtpErr(550, 5, 1, 1, "Mailbox unavailable")
	}
	if err != nil {
		s.logger.Error("groups: expansion failed", "to", to, "group", gre.GroupID, "error", err)
		return smtpErr(451, 4, 3, 0, "Temporary local error, please try again later")
	}
	// Anyone may post today; the hook is where the posting policy lands.
	if err := store.GroupPostingAllowed(g, s.postingUserID(), s.from); err != nil {
		s.logger.Warn("groups: RCPT rejected: posting not allowed", "to", to, "group", g.Id, "error", err)
		return smtpErr(550, 5, 7, 1, "Not allowed to post to this address")
	}
	if skipped := len(g.MemberIds) - len(members); skipped > 0 {
		s.logger.Info("groups: skipped members with no live account", "group", g.Id, "skipped", skipped)
	}

	var (
		covered   []string // every deliverable member, for the loop check
		fresh     []store.GroupMember
		overQuota int
	)
	for _, m := range members {
		if _, dup := s.groupState.users[m.UserID]; dup {
			covered = append(covered, m.UserID)
			continue
		}
		if s.backend.quotaChecker != nil {
			over, qErr := s.backend.quotaChecker.RecipientWouldExceedQuota(m.UserID, s.declaredSize)
			if qErr != nil {
				// Fail open, as for a direct recipient.
				s.logger.Warn("groups: quota check error (fail open)", "group", g.Id, "user", m.UserID, "error", qErr)
			} else if over {
				s.logger.Warn("groups: member skipped: over storage quota", "group", g.Id, "user", m.UserID)
				overQuota++
				continue
			}
		}
		covered = append(covered, m.UserID)
		fresh = append(fresh, m)
	}
	if len(covered) == 0 {
		if overQuota > 0 {
			return smtpErr(552, 5, 2, 2, "Mailbox quota exceeded")
		}
		// The same text as every other refusal: no oracle on group contents.
		s.logger.Warn("groups: RCPT rejected: no deliverable member", "to", to, "group", g.Id)
		return smtpErr(550, 5, 1, 1, "Mailbox unavailable")
	}

	// Peek only: the charge is made at DATA, as for the external-recipient
	// budget, so a transaction that fails before then spends nothing. A parallel
	// session can spend the allowance in between; DATA is what commits.
	if l := s.backend.groupFanout; l != nil && len(fresh) > 0 &&
		l.Remaining(s.fanoutKey()) < s.groupState.pending+len(fresh) {
		s.logger.Warn("groups: fan-out limit reached", "group", g.Id, "key", s.fanoutKey(), "members", len(fresh))
		return smtpErr(452, 4, 5, 3, "Too many group recipients, try again later")
	}

	gs := &s.groupState
	md := memberDSN(dsn, to)
	for _, m := range fresh {
		s.addMemberLocal(m, md)
	}
	gs.pending += len(fresh)
	gs.groups = append(gs.groups, groupExpansion{groupID: g.Id, rcpt: to, users: covered})
	s.logger.Debug("groups: RCPT TO (group)", "to", to, "group", g.Id, "members", len(fresh))
	return nil
}

// postingUserID is the account posting, or "" for mail from outside.
func (s *Session) postingUserID() string {
	if s.authed {
		return s.userID
	}
	return ""
}

// addDirectLocal records a directly addressed local recipient. Every direct
// RCPT gets its own envelope, exactly as before groups. The one exception is a
// user a group has already added a copy for: that copy is turned into this
// recipient's envelope in place (its address and DSN parameters replaced), so
// the user still gets one copy and it carries the parameters the client gave
// for the direct address. Rewriting in place is simpler than removing the
// group copy and appending, which would shift the indices the bookkeeping
// holds. The copy is then no longer a fan-out copy, so the cap is not charged
// for it.
func (s *Session) addDirectLocal(userID, to string, dsn RcptDSN) {
	gs := &s.groupState
	gs.init()
	gs.direct[userID] = true
	if idx, ok := gs.users[userID]; ok && gs.fanout[userID] {
		s.localRcpts[idx] = to
		s.localDSN[idx] = dsn
		delete(gs.fanout, userID)
		gs.pending--
		return
	}
	if _, ok := gs.users[userID]; !ok {
		gs.users[userID] = len(s.localRcpts)
	}
	s.addLocal(to, dsn)
}

// addMemberLocal records a member copy added by a group expansion. The caller
// has checked the user has no envelope yet.
func (s *Session) addMemberLocal(m store.GroupMember, dsn RcptDSN) {
	gs := &s.groupState
	gs.init()
	gs.users[m.UserID] = len(s.localRcpts)
	gs.fanout[m.UserID] = true
	s.addLocal(m.Address, dsn)
}

func (g *groupState) init() {
	if g.users == nil {
		g.users = map[string]int{}
		g.fanout = map[string]bool{}
		g.direct = map[string]bool{}
	}
}

// dropLoopedGroups removes the copies a group would deliver when the message
// already carries that group's id, and returns how many member copies were
// dropped. A user still covered by another group in the message, or addressed
// directly, keeps their copy. localRcpts and localDSN stay index-aligned.
func (s *Session) dropLoopedGroups(h netmail.Header) int {
	gs := &s.groupState
	mh := textproto.MIMEHeader(h)
	looped := make([]bool, len(gs.groups))
	found := false
	for i, ge := range gs.groups {
		if store.HeaderHasGroup(mh, ge.groupID) {
			looped[i] = true
			found = true
			s.logger.Info("groups: loop detected, not expanding again", "group", ge.groupID)
		}
	}
	if !found {
		return 0
	}
	live := map[string]bool{}
	for i, ge := range gs.groups {
		if !looped[i] {
			for _, u := range ge.users {
				live[u] = true
			}
		}
	}
	drop := map[int]bool{}
	for i, ge := range gs.groups {
		if !looped[i] {
			continue
		}
		for _, u := range ge.users {
			if idx, ok := gs.users[u]; ok && gs.fanout[u] && !live[u] && !gs.direct[u] {
				drop[idx] = true
			}
		}
	}
	keptGroups := gs.groups[:0]
	for i, ge := range gs.groups {
		if !looped[i] {
			keptGroups = append(keptGroups, ge)
		}
	}
	gs.groups = keptGroups
	if len(drop) == 0 {
		return 0
	}
	rcpts := make([]string, 0, len(s.localRcpts))
	dsns := make([]RcptDSN, 0, len(s.localDSN))
	for i := range s.localRcpts {
		if drop[i] {
			continue
		}
		rcpts = append(rcpts, s.localRcpts[i])
		dsns = append(dsns, s.localDSN[i])
	}
	s.localRcpts, s.localDSN = rcpts, dsns
	for u, idx := range gs.users {
		if drop[idx] {
			delete(gs.users, u)
			delete(gs.fanout, u)
		}
	}
	return len(drop)
}

// fanoutCount is the number of member copies still to be delivered that a
// group expansion added.
func (s *Session) fanoutCount() int { return len(s.groupState.fanout) }

// groupHeaderBlock is the X-Bongo-Group header lines for the groups that are
// expanded for this message, or "". The ids are lower-case hex.
func (s *Session) groupHeaderBlock() string {
	var b strings.Builder
	for _, ge := range s.groupState.groups {
		b.WriteString(store.GroupLoopHeader + ": " + sanitizeHeaderValue(ge.groupID) + "\r\n")
	}
	return b.String()
}
