package imap

import (
	"sync"

	"github.com/bongo-project/bongo/internal/store"
)

// NotificationType identifies the kind of mailbox event.
type NotificationType int

const (
	NotifNewMail NotificationType = iota
	NotifFlagChange
	NotifExpunge
	NotifEventReminder
)

// Notification represents a mailbox event for IDLE.
//
// It identifies a message by UID and carries no sequence number on purpose.
// Sequence numbers are positions in the list a particular client has been told
// the mailbox contains, so they are a property of a session, not of the event:
// two clients whose views have diverged hold different numbers for this same
// message. A producer -- which is the store, and so has no session -- cannot
// compute one that is right for more than one recipient, and a wrong one makes
// a client expunge a message it should have kept. Each consumer translates the
// UID against its own view when it delivers (see session.reportedSeqNum).
type Notification struct {
	Type        NotificationType
	UID         uint32
	Flags       uint32
	ExistsCount uint32

	// Event reminder fields (only set when Type == NotifEventReminder).
	EventTitle    string
	EventStart    int64
	MinutesBefore int32
}

// Notifier is a fan-out notification hub for IMAP IDLE.
type Notifier struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Notification]struct{} // key: "userID:mailbox"
}

// NewNotifier creates a new Notifier.
func NewNotifier() *Notifier {
	return &Notifier{
		subscribers: make(map[string]map[chan Notification]struct{}),
	}
}

func subKey(userID, mailbox string) string {
	return userID + ":" + mailbox
}

// Subscribe returns a channel that receives notifications for the given mailbox.
func (n *Notifier) Subscribe(userID, mailbox string) chan Notification {
	n.mu.Lock()
	defer n.mu.Unlock()

	key := subKey(userID, mailbox)
	ch := make(chan Notification, 64)
	if n.subscribers[key] == nil {
		n.subscribers[key] = make(map[chan Notification]struct{})
	}
	n.subscribers[key][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber channel.
func (n *Notifier) Unsubscribe(ch chan Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for key, subs := range n.subscribers {
		if _, ok := subs[ch]; ok {
			delete(subs, ch)
			close(ch)
			if len(subs) == 0 {
				delete(n.subscribers, key)
			}
			return
		}
	}
}

// Notify sends a notification to all subscribers of a mailbox.
func (n *Notifier) Notify(userID, mailbox string, notif Notification) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	key := subKey(userID, mailbox)
	for ch := range n.subscribers[key] {
		select {
		case ch <- notif:
		default:
			// Drop if the subscriber's buffer is full.
		}
	}
}

// StoreNotifier lets the store publish mailbox events into this hub without
// importing it. internal/imap imports internal/store, so the arrow can only
// point this way; store.MailboxNotifier is the interface at that consumer and
// this is the adapter the agent injects.
//
// Emitting from the store rather than from the IMAP session is what makes a
// change visible whichever protocol made it: marking a message read in the web
// UI, in CalDAV-adjacent tooling or over IMAP all funnel through the same
// MailboxStore methods, whereas a session-level producer would only ever see
// its own protocol's traffic.
type StoreNotifier struct {
	notifier *Notifier
}

// Compile-time check that the adapter still satisfies the store's interface.
var _ store.MailboxNotifier = (*StoreNotifier)(nil)

// NewStoreNotifier wraps n. A nil n is accepted and yields a silent sink, so a
// role that never starts an IMAP hub needs no special-casing at the call site.
func NewStoreNotifier(n *Notifier) *StoreNotifier {
	return &StoreNotifier{notifier: n}
}

func (sn *StoreNotifier) publish(userID, mailbox string, notif Notification) {
	if sn == nil || sn.notifier == nil {
		return
	}
	sn.notifier.Notify(userID, mailbox, notif)
}

// NotifyNewMail implements store.MailboxNotifier.
func (sn *StoreNotifier) NotifyNewMail(userID, mailbox string, uid, existsCount uint32) {
	sn.publish(userID, mailbox, Notification{
		Type:        NotifNewMail,
		UID:         uid,
		ExistsCount: existsCount,
	})
}

// NotifyFlagChange implements store.MailboxNotifier.
func (sn *StoreNotifier) NotifyFlagChange(userID, mailbox string, uid, flags uint32) {
	sn.publish(userID, mailbox, Notification{
		Type:  NotifFlagChange,
		UID:   uid,
		Flags: flags,
	})
}

// NotifyExpunge implements store.MailboxNotifier.
func (sn *StoreNotifier) NotifyExpunge(userID, mailbox string, uid uint32) {
	sn.publish(userID, mailbox, Notification{
		Type: NotifExpunge,
		UID:  uid,
	})
}
