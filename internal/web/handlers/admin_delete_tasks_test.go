package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Deleting a user removes their tasks with the rest of /user/{id}/, and only
// theirs. Tasks are written through the real TaskStore so the key under test
// is the one CalDAV uses.
func TestDeleteUserRemovesTasks(t *testing.T) {
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

	tasks := store.NewTaskStore(h.store, applier)
	if _, err := tasks.Create(victimID, &bongov1.Task{Guid: "t1", CalendarId: "tasks", Summary: "Victim task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Create(otherID, &bongov1.Task{Guid: "t1", CalendarId: "tasks", Summary: "Other task"}); err != nil {
		t.Fatal(err)
	}
	victimKey := []byte(fmt.Sprintf("/user/%s/task/t1", victimID))
	if _, err := h.store.Get(victimKey); err != nil {
		t.Fatalf("fixture invalid: task not stored: %v", err)
	}

	rec := deleteUserRequest(t, h, victimID, "some-other-admin-id")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	if _, err := h.store.Get(victimKey); err == nil {
		t.Error("victim task still present after delete")
	}
	if _, err := tasks.Get(otherID, "t1"); err != nil {
		t.Errorf("another user's task was removed: %v", err)
	}
}
