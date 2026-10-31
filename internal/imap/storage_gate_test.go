package imap

import (
	"errors"
	"testing"

	"github.com/emersion/go-imap/v2"
)

type fakeStorageGate struct{ low bool }

func (g *fakeStorageGate) Low() bool { return g.low }

func TestAppendRefusedWhenStorageLow(t *testing.T) {
	fq := &fakeQuota{}
	e := newQuotaEnv(t, fq)
	gate := &fakeStorageGate{low: true}
	e.sess.storage = gate

	_, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil)
	var ie *imap.Error
	if !errors.As(err, &ie) {
		t.Fatalf("Append with storage low: want *imap.Error, got %v", err)
	}
	if ie.Type != imap.StatusResponseTypeNo || ie.Code != imap.ResponseCodeUnavailable {
		t.Errorf("got %s [%s], want NO [UNAVAILABLE]", ie.Type, ie.Code)
	}
	if n := e.blobFiles(t); n != 0 {
		t.Errorf("blob files after refused APPEND = %d, want 0", n)
	}
	if n := e.mailKeys(t); n != 0 {
		t.Errorf("mail keys after refused APPEND = %d, want 0", n)
	}
	if fq.calls != 0 {
		t.Errorf("quota consulted %d times before the storage gate", fq.calls)
	}

	// Positive control: same session once space is back.
	gate.low = false
	if _, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil); err != nil {
		t.Fatalf("Append with storage ok: %v", err)
	}
	if n := e.messageCount(t); n != 1 {
		t.Errorf("messages = %d, want 1", n)
	}
}

func TestAppendNilStorageGateAccepts(t *testing.T) {
	e := newQuotaEnv(t, nil)
	if _, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestServerSetStorageGate(t *testing.T) {
	var s Server
	g := &fakeStorageGate{low: true}
	s.SetStorageGate(g)
	if p := s.storage.Load(); p == nil || !(*p).Low() {
		t.Fatal("gate not stored")
	}
	s.SetStorageGate(nil)
	if s.storage.Load() != nil {
		t.Fatal("nil must clear the gate")
	}
}
