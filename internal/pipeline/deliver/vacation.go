package deliver

import (
	"bufio"
	"bytes"
	"errors"
	"hash/fnv"
	"io"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	bongomail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/store"
)

// Vacation replies (RFC 3834).
//
// A reply is decided per recipient after the final destination of the message
// is known, but it is sent only after the recipient loop has finished, next to
// the forwards. A DeliverMail failure returns an error and the message is
// retried; sending first would repeat the reply on every retry. The tracking
// record also makes that retry safe: the retry finds the record and suppresses.
//
// Vacation never changes delivery. A user who forwards without keeping a copy
// still gets replies: forwarding is not a rule deletion, and the sender still
// reached this mailbox's owner.
//
// Delivery is at-least-once and the interval is best-effort across nodes: the
// tracking read-then-write is serialised per node (vacationLocks), not between
// nodes, and a follower may read a record that has not replicated yet. The
// per-user hourly cap bounds the damage. A sender whose envelope address
// changes per message (SRS rewriting) defeats the interval; the cap is the
// backstop there too.

// VacationAccounts resolves an address to a user ID for primary addresses and
// aliases only: never through a catch-all, a send-as identity or the
// postmaster tiers. Satisfied by *store.RecipientResolver.
type VacationAccounts interface {
	ResolveAccount(email string) (string, error)
}

// VacationProfiles returns a user's primary address. Satisfied by
// *store.UserDomains.
type VacationProfiles interface {
	PrimaryEmail(userID string) (string, error)
}

// VacationTracker reads a stored tracking record. Satisfied by
// *store.PebbleStore; store.ErrNotFound means no record.
type VacationTracker interface {
	Get(key []byte) ([]byte, error)
}

// VacationConfig is everything the deliver stage needs to send a reply. The
// stage replies only when every field except DKIM and Now is set (fail
// closed).
type VacationConfig struct {
	Accounts VacationAccounts
	Profiles VacationProfiles
	Tracker  VacationTracker
	// Blobs holds the reply message until the outbound queue has sent it.
	Blobs *store.BlobStore
	// Local takes a reply addressed to a local user, through the inbound
	// pipeline as bounces do.
	Local bongomail.Deliverer
	// Limiter caps replies per user ID per hour.
	Limiter ForwardLimiter
	// DKIM signs a reply to a local sender so it passes local DMARC. Nil sends
	// it unsigned.
	DKIM *bongomail.DKIMRegistry
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// vacationLockCount is the number of per-node mutexes tracking check-then-write
// is spread over. A fixed array keeps memory bounded however many senders
// write in.
const vacationLockCount = 64

type vacationState struct {
	VacationConfig
	locks [vacationLockCount]sync.Mutex
}

func (v *vacationState) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *vacationState) complete() bool {
	return v.Accounts != nil && v.Profiles != nil && v.Tracker != nil &&
		v.Blobs != nil && v.Local != nil && v.Limiter != nil
}

func (v *vacationState) lockFor(key []byte) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write(key)
	return &v.locks[h.Sum32()%vacationLockCount]
}

// SetVacation enables vacation replies. Without it, or with an incomplete
// config, no reply is ever sent.
func (p *DeliverProcessor) SetVacation(c VacationConfig) {
	p.vacation = &vacationState{VacationConfig: c}
}

// VacationEnabled reports whether the stage can send replies, for wiring tests.
func (p *DeliverProcessor) VacationEnabled() bool {
	return p.vacation != nil && p.vacation.complete()
}

// vacationFacts is everything the suppression decision depends on, gathered
// by the caller so the decision itself is pure and table-testable.
type vacationFacts struct {
	prefs *bongov1.UserPreferences
	now   time.Time

	// envSender is the envelope sender as received; header is the ORIGINAL
	// header block (not the stamped copy).
	envSender string
	header    textproto.MIMEHeader

	// ownAddressInToCc: one of the user's own addresses is in To or Cc.
	ownAddressInToCc bool
	// senderIsSelf: the envelope sender is the user's own address.
	senderIsSelf bool
	// senderAuthenticated: see senderAuthenticated.
	senderAuthenticated bool

	// discarded: a delete rule removed the normal destination.
	discarded bool
	// finalMailbox is where the message is filed, as decided by the rules
	// stage and the spam score.
	finalMailbox string
	// spam: the message is spam, junk or quarantined (forwardedAsSpam).
	spam bool
}

// vacationDecision reports whether a reply may be sent and, when not, the
// first condition that suppressed it. The conditions are RFC 3834 section 2
// and the suppression rules below. It checks nothing stored: the reply
// interval and the hourly cap are applied afterwards, since they need I/O.
func vacationDecision(f vacationFacts) (reply bool, reason string) {
	if !store.VacationActive(f.prefs, f.now) {
		return false, "vacation not active"
	}
	if f.discarded {
		return false, "deleted by a rule"
	}
	if mb := strings.ToLower(f.finalMailbox); mb == "junk" || mb == "trash" {
		return false, "filed to " + f.finalMailbox
	}
	if f.spam {
		return false, "spam or quarantined"
	}
	addr, ok := vacationSenderAddress(f.envSender)
	if !ok {
		return false, "no usable envelope sender"
	}
	if vacationRoleLocalPart(addr) {
		return false, "role or list address sender"
	}
	if f.senderIsSelf {
		return false, "sender is the recipient"
	}
	// An authenticated sender, not a bare SPF pass: an authenticated local
	// submitter's SPF result describes the client's IP, not the sender's domain.
	if !f.senderAuthenticated {
		return false, "sender not authenticated"
	}
	if f.header == nil {
		return false, "unreadable headers"
	}
	for _, v := range f.header["Auto-Submitted"] {
		if !autoSubmittedIsNo(v) {
			return false, "auto-submitted"
		}
	}
	for _, v := range f.header["Precedence"] {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "bulk", "list", "junk":
			return false, "precedence " + strings.ToLower(strings.TrimSpace(v))
		}
	}
	for _, h := range []string{"List-Id", "List-Post", "List-Unsubscribe"} {
		if _, present := f.header[h]; present {
			return false, "list mail"
		}
	}
	if !f.ownAddressInToCc {
		return false, "not addressed to the user"
	}
	return true, ""
}

// vacationSenderAddress parses an envelope sender into a bare address. The
// null sender, a non-address and anything without a local part and domain
// return false.
func vacationSenderAddress(sender string) (string, bool) {
	sender = strings.TrimSpace(sender)
	if sender == "" || sender == "<>" {
		return "", false
	}
	a, err := mail.ParseAddress(sender)
	if err != nil {
		return "", false
	}
	at := strings.LastIndexByte(a.Address, '@')
	if at <= 0 || at == len(a.Address)-1 {
		return "", false
	}
	return a.Address, true
}

// vacationRoleLocalPart reports whether the local part names a daemon, a list
// owner or a no-reply mailbox.
func vacationRoleLocalPart(addr string) bool {
	local := strings.ToLower(addr[:strings.LastIndexByte(addr, '@')])
	switch local {
	case "mailer-daemon", "postmaster", "noreply", "no-reply":
		return true
	}
	return strings.HasPrefix(local, "owner-") || strings.HasSuffix(local, "-request")
}

// autoSubmittedIsNo reports whether one Auto-Submitted value is the token
// "no": case-insensitive, after dropping comments and parameters. Anything
// else, including an empty or unparsable value, is automatic.
func autoSubmittedIsNo(v string) bool {
	var sb strings.Builder
	depth := 0
	for _, r := range v {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0:
			sb.WriteRune(r)
		}
	}
	tok, _, _ := strings.Cut(sb.String(), ";")
	return strings.EqualFold(strings.TrimSpace(tok), "no")
}

// readOriginalHeader parses the header block of the original message. It
// returns nil when the block is malformed: a line the parser stops at could
// hide an Auto-Submitted header after it, so the caller fails closed.
func readOriginalHeader(raw []byte) textproto.MIMEHeader {
	h, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw))).ReadMIMEHeader()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	return h
}

// vacationMaxAddressChecks bounds the lookups made for one message's To and Cc.
const vacationMaxAddressChecks = 50

// vacationCandidate is a reply the decision allowed, waiting for the end of the
// recipient loop.
type vacationCandidate struct {
	userID string
	rcpt   string // the envelope recipient the message arrived for
	sender string // validated envelope sender
	// senderUserID is the local account the sender resolves to, or "" for an
	// external sender.
	senderUserID string
	prefs        *bongov1.UserPreferences
	origMsgID    string
	origSubject  string
}

// vacationBatch holds the headers parsed once per message.
type vacationBatch struct {
	header textproto.MIMEHeader
	toCc   []string
	spam   bool
}

// newVacationBatch parses what is shared by every recipient of one message.
func newVacationBatch(raw []byte, spam bool) *vacationBatch {
	b := &vacationBatch{header: readOriginalHeader(raw), spam: spam}
	if b.header != nil {
		for _, h := range []string{"To", "Cc"} {
			for _, v := range b.header[h] {
				list, err := mail.ParseAddressList(v)
				if err != nil {
					continue
				}
				for _, a := range list {
					b.toCc = append(b.toCc, a.Address)
				}
			}
		}
	}
	return b
}

// vacationCandidateFor decides whether recipient userID gets a reply and
// returns the candidate, or nil. It is called once per resolved recipient,
// before the skipLocal `continue`, so account forwarding without a copy still
// replies.
func (p *DeliverProcessor) vacationCandidateFor(env *bongov1.QueueEnvelope, b *vacationBatch, userID, rcpt string,
	prefs *bongov1.UserPreferences, finalMailbox string) *vacationCandidate {
	v := p.vacation
	if v == nil || !v.complete() || prefs == nil {
		return nil
	}
	// Cheap checks first: most users have vacation off, and then nothing else
	// (no lookups, no header scan) is done.
	now := v.now()
	if !store.VacationActive(prefs, now) {
		return nil
	}
	f := vacationFacts{
		prefs:               prefs,
		now:                 now,
		envSender:           env.GetSender(),
		header:              b.header,
		senderAuthenticated: senderAuthenticated(env),
		discarded:           env.GetDiscardNormal(),
		finalMailbox:        finalMailbox,
		spam:                b.spam,
	}
	sender, senderOK := vacationSenderAddress(env.GetSender())
	var senderUser string
	if senderOK {
		senderUser = p.vacationSenderUser(v, userID, sender)
		f.senderIsSelf = senderUser == userID
	}
	f.ownAddressInToCc = p.vacationAddressedTo(v, userID, b.toCc)

	if ok, reason := vacationDecision(f); !ok {
		p.logger.Debug("deliver: vacation reply suppressed", "id", env.Id, "user", userID, "reason", reason)
		return nil
	}
	// An address on a hosted domain that no account owns gets no reply: there
	// is nowhere to deliver it and it must not leave the server.
	if senderUser == "" && p.hostedSenderDomain(sender) {
		p.logger.Debug("deliver: vacation reply suppressed", "id", env.Id, "user", userID, "reason", "sender on a hosted domain has no account")
		return nil
	}
	c := &vacationCandidate{userID: userID, rcpt: rcpt, sender: sender, senderUserID: senderUser, prefs: prefs}
	if ids := b.header["Message-Id"]; len(ids) > 0 {
		c.origMsgID = ids[0]
	}
	if subs := b.header["Subject"]; len(subs) > 0 {
		c.origSubject = subs[0]
	}
	return c
}

// vacationSenderUser returns the user ID the sender address belongs to: an
// account through its primary address or alias, or an identity userID may send
// as (so mail from one's own send-as address counts as one's own). "" means an
// external sender.
func (p *DeliverProcessor) vacationSenderUser(v *vacationState, userID, sender string) string {
	if id, err := v.Accounts.ResolveAccount(sender); err == nil && id != "" {
		return id
	}
	if p.authorizer != nil {
		if ok, err := p.authorizer.CanSendAs(userID, sender); err == nil && ok {
			return userID
		}
	}
	return ""
}

// vacationAddressedTo reports whether one of the user's own addresses (primary,
// alias or a send-as identity) is in addrs. It deliberately does not use
// ResolveRecipient: through a catch-all every unknown address on the domain
// would count as the owner's, and postmaster@ as the domain owner's.
func (p *DeliverProcessor) vacationAddressedTo(v *vacationState, userID string, addrs []string) bool {
	for i, a := range addrs {
		if i >= vacationMaxAddressChecks {
			break
		}
		if id, err := v.Accounts.ResolveAccount(a); err == nil && id == userID {
			return true
		}
		if p.authorizer != nil {
			if ok, err := p.authorizer.CanSendAs(userID, a); err == nil && ok {
				return true
			}
		}
	}
	return false
}

func (p *DeliverProcessor) hostedSenderDomain(sender string) bool {
	if p.hostedDomain == nil {
		return false
	}
	return p.hostedDomain(strings.ToLower(sender[strings.LastIndexByte(sender, '@')+1:]))
}

// sendVacationReplies sends the replies collected by the recipient loop, at
// most one per user and sender. A message addressed to several of one user's
// addresses yields a candidate per address; the tracking record written for
// the first may not be readable yet on a follower, so it cannot be relied on
// to stop the second.
func (p *DeliverProcessor) sendVacationReplies(env *bongov1.QueueEnvelope, cands []*vacationCandidate) {
	seen := make(map[string]bool, len(cands))
	for _, c := range cands {
		k := c.userID + "\x00" + strings.ToLower(c.sender)
		if seen[k] {
			continue
		}
		seen[k] = true
		p.sendVacationReply(env, c)
	}
}

// sendVacationReply applies the interval and the hourly cap, records the reply
// and sends it.
//
// Order matters. The tracking record is written before the reply is queued,
// and a failed write sends nothing: failing closed is what stops a loop when
// the store is unhealthy. The hourly cap is checked before the write, so a
// capped reply leaves no record and its sender is not silenced for the
// interval without ever having had an answer. If the queue write fails after
// the record is written, that one reply is lost (logged and counted); it is
// not retried, since a retry could repeat a reply that did get through.
func (p *DeliverProcessor) sendVacationReply(env *bongov1.QueueEnvelope, c *vacationCandidate) {
	v := p.vacation
	key := store.VacationSentKey(c.userID, c.sender)
	interval := time.Duration(store.VacationIntervalDays(c.prefs)) * 24 * time.Hour

	if !p.reserveVacationReply(env, v, c, key, interval) {
		return
	}

	msg, err := p.buildVacationReply(c, v.now())
	if err != nil {
		p.logger.Warn("deliver: vacation reply not built", "id", env.Id, "user", c.userID, "error", err)
		observability.VacationRepliesTotal.WithLabelValues("enqueue_failed").Inc()
		return
	}
	if err := p.dispatchVacationReply(v, c, msg); err != nil {
		p.logger.Warn("deliver: vacation reply lost after its record was written",
			"id", env.Id, "user", c.userID, "error", err)
		observability.VacationRepliesTotal.WithLabelValues("enqueue_failed").Inc()
		return
	}
	observability.VacationRepliesTotal.WithLabelValues("sent").Inc()
	p.logger.Info("deliver: vacation reply sent", "id", env.Id, "user", c.userID, "local", c.senderUserID != "")
}

// reserveVacationReply is the serialised part: read the record, apply the
// cap and write the record. It reports whether the reply may go out.
func (p *DeliverProcessor) reserveVacationReply(env *bongov1.QueueEnvelope, v *vacationState, c *vacationCandidate,
	key []byte, interval time.Duration) bool {
	mu := v.lockFor(key)
	mu.Lock()
	defer mu.Unlock()

	now := v.now()
	switch val, err := v.Tracker.Get(key); {
	case err == nil:
		// A record that does not decode is treated as absent and overwritten.
		if at, derr := store.DecodeVacationSent(val); derr == nil {
			// The lower bound ignores a record from the far future (clock
			// skew or corruption), which would otherwise silence a sender
			// until the sweep removes it.
			if age := now.Sub(at); age > -time.Hour && age < interval {
				p.logger.Debug("deliver: vacation reply suppressed", "id", env.Id, "user", c.userID, "reason", "already replied within the interval")
				return false
			}
		}
	case errors.Is(err, store.ErrNotFound):
	default:
		p.logger.Warn("deliver: vacation tracking unreadable, no reply", "id", env.Id, "user", c.userID, "error", err)
		observability.VacationRepliesTotal.WithLabelValues("track_failed").Inc()
		return false
	}

	if !v.Limiter.Allow(c.userID) {
		observability.VacationRepliesTotal.WithLabelValues("rate_limited").Inc()
		p.logger.Warn("deliver: vacation reply limit reached, reply skipped", "id", env.Id, "user", c.userID)
		return false
	}

	put := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: store.EncodeVacationSent(now)}
	if err := p.applier.ApplyCommand(put, 5*time.Second); err != nil {
		p.logger.Warn("deliver: vacation tracking write failed, no reply", "id", env.Id, "user", c.userID, "error", err)
		observability.VacationRepliesTotal.WithLabelValues("track_failed").Inc()
		return false
	}
	return true
}

// dispatchVacationReply sends msg: to an external sender through the outbound
// queue with the null envelope sender, to a local one through the inbound
// pipeline (signed, as bounces are).
func (p *DeliverProcessor) dispatchVacationReply(v *vacationState, c *vacationCandidate, msg []byte) error {
	if c.senderUserID != "" {
		signed, err := v.DKIM.Sign(msg, p.logger)
		if err != nil {
			// Never fails the reply: an unsigned one is still delivered.
			p.logger.Warn("deliver: vacation reply DKIM signing failed, sending unsigned", "user", c.userID, "error", err)
			signed = msg
		}
		return v.Local.Deliver("", []string{c.sender}, bytes.NewReader(signed))
	}
	if p.queuer == nil {
		return errors.New("no outbound queue")
	}
	hash, err := v.Blobs.Put(msg)
	if err != nil {
		return err
	}
	return p.queuer.Enqueue(&bongov1.OutboundMessage{
		Id:          uuid.New().String(),
		SenderId:    c.userID,
		SenderEmail: "", // null sender: a failure of this reply can never bounce
		Recipients:  []string{c.sender},
		BlobHash:    hash,
		Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_AUTOREPLY,
	})
}
