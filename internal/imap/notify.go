package imap

import "sync"

// NotificationType identifies the kind of mailbox event.
type NotificationType int

const (
	NotifNewMail       NotificationType = iota
	NotifFlagChange
	NotifExpunge
	NotifEventReminder
)

// Notification represents a mailbox event for IDLE.
type Notification struct {
	Type        NotificationType
	UID         uint32
	Flags       uint32
	SeqNum      uint32
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
