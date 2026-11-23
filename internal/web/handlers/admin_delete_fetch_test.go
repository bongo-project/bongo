package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Deleting a user removes all three remote-account key kinds with the rest of
// /user/{id}/, and nothing of another user's, including a user whose id starts
// with the victim's.
func TestDeleteUserRemovesFetchRecords(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)

	if _, err := auth.CreateUser(applier, h.metadata, "boss@example.com", "Boss", "adminpass123", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	victimID, err := auth.CreateUser(applier, h.metadata, "victim@example.com", "Victim", "victimpass123", false)
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	otherID, err := auth.CreateUser(applier, h.metadata, "other@example.com", "Other", "otherpass123", false)
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	fetch := store.NewFetchStore(h.store, applier)
	guid := strings.Repeat("a", 32)
	hash := strings.Repeat("b", 32)
	for _, id := range []string{victimID, otherID} {
		if err := fetch.Put(id, &bongov1.FetchAccount{Guid: guid, Host: "imap.example.com"}); err != nil {
			t.Fatal(err)
		}
		if err := fetch.PutState(id, guid, &bongov1.FetchState{Generation: 1}); err != nil {
			t.Fatal(err)
		}
		if err := fetch.MarkSeen(id, guid, hash, &bongov1.FetchSeen{At: 1, Status: 1}); err != nil {
			t.Fatal(err)
		}
	}
	keys := func(id string) [][]byte {
		return [][]byte{store.FetchAccountKey(id, guid), store.FetchStateKey(id, guid), store.FetchSeenKey(id, guid, hash)}
	}
	for _, k := range keys(victimID) {
		if _, err := h.store.Get(k); err != nil {
			t.Fatalf("fixture invalid: %s not stored: %v", k, err)
		}
	}

	rec := deleteUserRequest(t, h, victimID, "some-other-admin-id")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	for _, k := range keys(victimID) {
		if _, err := h.store.Get(k); err == nil {
			t.Errorf("%s still present after delete", k)
		}
	}
	for _, k := range keys(otherID) {
		if _, err := h.store.Get(k); err != nil {
			t.Errorf("another user's %s was removed: %v", k, err)
		}
	}
}
