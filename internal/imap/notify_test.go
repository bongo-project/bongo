package imap

import (
	"sync"
	"testing"
	"time"
)

func TestNotifier_SubscribeUnsubscribe(t *testing.T) {
	n := NewNotifier()

	ch := n.Subscribe("user1", "INBOX")
	if ch == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	// Verify channel has buffer.
	if cap(ch) != 64 {
		t.Errorf("channel capacity = %d, want 64", cap(ch))
	}

	n.Unsubscribe(ch)

	// After unsubscribe, channel should be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after unsubscribe")
		}
	default:
		t.Error("channel not closed after unsubscribe")
	}
}

func TestNotifier_UnsubscribeUnknownChannel(t *testing.T) {
	n := NewNotifier()
	ch := make(chan Notification, 64)
	// Should not panic.
	n.Unsubscribe(ch)
}

func TestNotifier_NotifyFanOut(t *testing.T) {
	n := NewNotifier()

	ch1 := n.Subscribe("user1", "INBOX")
	ch2 := n.Subscribe("user1", "INBOX")
	defer n.Unsubscribe(ch1)
	defer n.Unsubscribe(ch2)

	notif := Notification{
		Type:        NotifNewMail,
		UID:         42,
		ExistsCount: 10,
	}
	n.Notify("user1", "INBOX", notif)

	// Both subscribers should receive the notification.
	for i, ch := range []chan Notification{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Type != NotifNewMail {
				t.Errorf("subscriber %d: type = %d, want %d", i, got.Type, NotifNewMail)
			}
			if got.UID != 42 {
				t.Errorf("subscriber %d: UID = %d, want 42", i, got.UID)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for notification", i)
		}
	}
}

func TestNotifier_NotifyDifferentMailbox(t *testing.T) {
	n := NewNotifier()

	inbox := n.Subscribe("user1", "INBOX")
	sent := n.Subscribe("user1", "Sent")
	defer n.Unsubscribe(inbox)
	defer n.Unsubscribe(sent)

	n.Notify("user1", "INBOX", Notification{Type: NotifNewMail, UID: 1})

	// INBOX subscriber should get it.
	select {
	case <-inbox:
	case <-time.After(time.Second):
		t.Fatal("inbox subscriber timed out")
	}

	// Sent subscriber should NOT get it.
	select {
	case <-sent:
		t.Fatal("sent subscriber should not have received notification")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNotifier_NotifyDifferentUser(t *testing.T) {
	n := NewNotifier()

	ch1 := n.Subscribe("user1", "INBOX")
	ch2 := n.Subscribe("user2", "INBOX")
	defer n.Unsubscribe(ch1)
	defer n.Unsubscribe(ch2)

	n.Notify("user1", "INBOX", Notification{Type: NotifNewMail, UID: 1})

	select {
	case <-ch1:
	case <-time.After(time.Second):
		t.Fatal("user1 subscriber timed out")
	}

	select {
	case <-ch2:
		t.Fatal("user2 should not have received user1's notification")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNotifier_BufferOverflowDrops(t *testing.T) {
	n := NewNotifier()
	ch := n.Subscribe("user1", "INBOX")
	defer n.Unsubscribe(ch)

	// Fill the buffer (capacity 64).
	for i := 0; i < 64; i++ {
		n.Notify("user1", "INBOX", Notification{Type: NotifNewMail, UID: uint32(i)})
	}

	// This notification should be dropped (buffer full), not block.
	done := make(chan struct{})
	go func() {
		n.Notify("user1", "INBOX", Notification{Type: NotifNewMail, UID: 999})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked on full buffer")
	}
}

func TestNotifier_ConcurrentAccess(t *testing.T) {
	n := NewNotifier()
	var wg sync.WaitGroup

	// Concurrent subscribe/notify/unsubscribe.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ch := n.Subscribe("user1", "INBOX")
			for j := 0; j < 10; j++ {
				n.Notify("user1", "INBOX", Notification{Type: NotifNewMail, UID: uint32(j)})
			}
			n.Unsubscribe(ch)
		}(i)
	}

	wg.Wait()
}

func TestNotifier_AllNotificationTypes(t *testing.T) {
	n := NewNotifier()
	ch := n.Subscribe("user1", "INBOX")
	defer n.Unsubscribe(ch)

	types := []NotificationType{NotifNewMail, NotifFlagChange, NotifExpunge}
	for _, nt := range types {
		n.Notify("user1", "INBOX", Notification{Type: nt, UID: 1})
	}

	for _, expected := range types {
		select {
		case got := <-ch:
			if got.Type != expected {
				t.Errorf("expected type %d, got %d", expected, got.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for notification type %d", expected)
		}
	}
}
