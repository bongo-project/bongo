package store

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// MailboxNotifier receives mailbox change events so that anything watching a
// mailbox -- IMAP IDLE sessions, the web SSE stream -- learns about a mutation
// no matter which protocol made it.
//
// It is declared here, at the consumer, rather than in internal/imap, because
// internal/imap already imports this package and the dependency can only run
// one way. internal/imap supplies the implementation (imap.StoreNotifier) and
// the agent injects it, exactly as RaftApplier is injected.
//
// Every method is fire-and-forget: a notification is an optimisation over the
// client's next poll, never the record of the change, so an implementation must
// not block and a caller must not fail a write because notification failed.
type MailboxNotifier interface {
	// NotifyNewMail announces that uid arrived in mailbox, which now holds
	// existsCount messages.
	NotifyNewMail(userID, mailbox string, uid, existsCount uint32)

	// NotifyFlagChange announces that uid's flags are now flags.
	NotifyFlagChange(userID, mailbox string, uid, flags uint32)

	// NotifyExpunge announces that uid is no longer in mailbox.
	//
	// It deliberately carries no sequence number. A sequence number is defined
	// relative to what one client has been told the mailbox contains, so two
	// clients with different views hold different numbers for this same
	// message; a number computed here would be wrong for all but one of them
	// and would make the others drop a message they should have kept. Each
	// consumer translates the UID against its own view at delivery time.
	NotifyExpunge(userID, mailbox string, uid uint32)
}

// SetNotifier installs the mailbox event sink used by appliers that apply
// mailbox commands to this node's Pebble WITHOUT going through Raft. On a
// clustered deployment the Raft FSM holds the notifier instead (see
// cluster.FSM.SetNotifier), because the FSM is the only place that runs on
// every node.
//
// Nil is the normal state on a role that has no IMAP fan-out hub (edge nodes,
// read-only tooling), and every emission site is guarded, so leaving it unset
// degrades to silence rather than failing writes.
func (ms *MailboxStore) SetNotifier(n MailboxNotifier) {
	ms.notifier = n
}

// Notifier returns the sink installed by SetNotifier, or nil. It exists for
// appliers outside this package, which have to reach the notifier the store was
// wired with in order to call PrepareNotify.
func (ms *MailboxStore) Notifier() MailboxNotifier {
	return ms.notifier
}

// slowMailboxScanThreshold is the point at which counting a mailbox is
// considered anomalous. The count exists only to fill in an IMAP EXISTS, and it
// runs inside the apply path, where raft serialises every writer behind it.
const slowMailboxScanThreshold = 2 * time.Second

// PendingNotify is a notification dispatch that has captured everything it
// needs from BEFORE a command was applied.
//
// The pre-image is why this is two calls rather than one. A plain PUT of a
// MailboxMessage is a flag change only if it actually changes the flags: IMAP
// keyword updates (imap.session.applyKeywords) and the web tag handlers rewrite
// the same message with the same flags, and web mail.go re-asserts \Seen every
// time a message is opened. Announcing those as flag changes sends every idling
// client an untagged FETCH that says nothing. Once the write has landed the
// flags it replaced are gone, so they have to be read first.
type PendingNotify struct {
	store     *PebbleStore
	notifier  MailboxNotifier
	cmd       *bongov1.RaftCommand
	logger    *slog.Logger
	prevFlags uint32
	hasPrev   bool
}

// PrepareNotify captures the pre-apply state cmd is about to overwrite. Call it
// BEFORE applying cmd, and Emit afterwards -- and only if the apply succeeded,
// so a rejected write announces nothing.
//
// It is the single entry point for mailbox notifications. Every applier that
// writes mailbox state to a local Pebble uses it, so the events a client sees
// do not depend on which applier its node happens to run.
func PrepareNotify(ps *PebbleStore, n MailboxNotifier, cmd *bongov1.RaftCommand, logger *slog.Logger) PendingNotify {
	p := PendingNotify{store: ps, notifier: n, cmd: cmd, logger: logger}
	if n == nil || ps == nil || cmd == nil || cmd.Op != bongov1.Op_OP_PUT {
		return p
	}
	userID, mailbox, uid, ok := parseMailboxMsgKey(cmd.Key)
	if !ok {
		return p
	}
	prev, err := readMailboxMessage(ps, userID, mailbox, uid)
	if err != nil {
		return p
	}
	p.prevFlags, p.hasPrev = prev.Flags, true
	return p
}

// Emit publishes the notifications implied by the command, given the result of
// applying it. result is nil for anything that is not a synthetic mailbox
// command.
//
// Expunges go out before arrivals so that a move reaches a client watching both
// mailboxes in the order the mailboxes changed.
func (p PendingNotify) Emit(result *MailboxCommandResult) {
	if p.notifier == nil {
		return
	}

	if result != nil {
		for _, ref := range result.Expunged {
			p.notifier.NotifyExpunge(ref.UserID, ref.Mailbox, ref.UID)
		}
		if nm := result.NewMail; nm != nil {
			p.emitArrival(MailboxMessageRef{UserID: nm.UserID, Mailbox: nm.Mailbox, UID: nm.UID})
		}
		for _, ref := range result.Arrivals {
			p.emitArrival(ref)
		}
		return
	}

	if p.cmd == nil {
		return
	}
	userID, mailbox, uid, ok := parseMailboxMsgKey(p.cmd.Key)
	if !ok {
		return
	}

	switch p.cmd.Op {
	case bongov1.Op_OP_DELETE:
		p.notifier.NotifyExpunge(userID, mailbox, uid)
	case bongov1.Op_OP_PUT:
		// No pre-image means no message was there to change. An arrival is
		// announced from a synthetic command's result, where the UID that was
		// allocated is known; inventing one here would announce a message twice
		// during Raft log replay against an already-populated store.
		if !p.hasPrev {
			return
		}
		var msg bongov1.MailboxMessage
		if err := proto.Unmarshal(p.cmd.Value, &msg); err != nil {
			return
		}
		if msg.Flags == p.prevFlags {
			return
		}
		p.notifier.NotifyFlagChange(userID, mailbox, uid, msg.Flags)
	}
}

// emitArrival announces a message that has just landed, looking up the count
// the announcement has to carry for an IMAP EXISTS.
func (p PendingNotify) emitArrival(ref MailboxMessageRef) {
	p.notifier.NotifyNewMail(ref.UserID, ref.Mailbox, ref.UID, p.countMessages(ref.UserID, ref.Mailbox))
}

// countMessages counts the messages in a mailbox. A failed scan yields zero
// rather than suppressing the notification: the arrival itself is the news, and
// a client that gets a count it disbelieves re-lists the mailbox.
func (p PendingNotify) countMessages(userID, mailbox string) uint32 {
	start := time.Now()
	var total uint32

	iter, err := p.store.PrefixScan(mailboxMsgPrefix(userID, mailbox))
	if err != nil {
		p.warn("store: mailbox message count scan failed", "user", userID, "mailbox", mailbox, "error", err)
		return 0
	}
	for iter.Next() {
		total++
	}
	if err := iter.Close(); err != nil {
		p.warn("store: failed to close mailbox count iterator", "user", userID, "mailbox", mailbox, "error", err)
	}

	if elapsed := time.Since(start); elapsed >= slowMailboxScanThreshold {
		// This scan is O(mailbox size) and runs inside the apply path for every
		// arrival, purely to produce an IMAP EXISTS count.
		p.warn("store: mailbox message count scan was slow",
			"user", userID,
			"mailbox", mailbox,
			"messages", total,
			"duration_ms", elapsed.Milliseconds(),
		)
	}
	return total
}

func (p PendingNotify) warn(msg string, args ...any) {
	if p.logger == nil {
		return
	}
	p.logger.Warn(msg, args...)
}

// parseMailboxMsgKey splits /user/{userID}/mailbox/{mailbox}/msg/{uid}.
//
// It is deliberately structural rather than a substring test: a mailbox may be
// named "msg" or "mail", so the separator is matched at a known position rather
// than wherever it first appears.
func parseMailboxMsgKey(key []byte) (userID, mailbox string, uid uint32, ok bool) {
	rest, found := strings.CutPrefix(string(key), "/user/")
	if !found {
		return "", "", 0, false
	}
	userID, rest, found = strings.Cut(rest, "/")
	if !found || userID == "" {
		return "", "", 0, false
	}
	rest, found = strings.CutPrefix(rest, "mailbox/")
	if !found {
		return "", "", 0, false
	}
	// Last, not first: a mailbox called "msg" yields ".../mailbox/msg/msg/0001".
	sep := strings.LastIndex(rest, "/msg/")
	if sep <= 0 {
		return "", "", 0, false
	}
	n, err := strconv.ParseUint(rest[sep+len("/msg/"):], 10, 32)
	if err != nil {
		return "", "", 0, false
	}
	return userID, rest[:sep], uint32(n), true
}
