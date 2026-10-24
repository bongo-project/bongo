package handlers

import (
	"testing"
	"time"
)

// A waiter must keep the map entry alive: with two waiters queued behind a
// holder, a third caller must still block on the same lock after the first
// unlock rather than get a fresh one.
func TestLockSendAsAddr_WaiterKeepsEntryAlive(t *testing.T) {
	const addr = "lock-test@example.net"

	unlockA := lockSendAsAddr(addr)

	bHolds := make(chan func())
	go func() { bHolds <- lockSendAsAddr(addr) }()

	// Let B register its reference and block on the lock.
	waitRefs := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			sendAsAddrLocks.mu.Lock()
			l := sendAsAddrLocks.m[addr]
			got := 0
			if l != nil {
				got = l.refs
			}
			sendAsAddrLocks.mu.Unlock()
			if got == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("refs never reached %d", n)
	}
	waitRefs(2)

	unlockA()
	unlockB := <-bHolds

	cGot := make(chan func(), 1)
	go func() { cGot <- lockSendAsAddr(addr) }()
	select {
	case u := <-cGot:
		u()
		t.Fatal("third caller acquired the lock while the second held it")
	case <-time.After(200 * time.Millisecond):
	}

	unlockB()
	select {
	case u := <-cGot:
		u()
	case <-time.After(2 * time.Second):
		t.Fatal("third caller never acquired the lock after release")
	}

	sendAsAddrLocks.mu.Lock()
	n := len(sendAsAddrLocks.m)
	sendAsAddrLocks.mu.Unlock()
	if n != 0 {
		t.Fatalf("lock entries leaked: %d", n)
	}
}
