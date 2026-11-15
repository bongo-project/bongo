package handlers

import (
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

type groupFailApplier struct{ *directApplierTags }

func (a groupFailApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if store.IsGroupSyntheticCommand(cmd.Key) {
		return errors.New("injected group failure")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

// Deleting a user removes them from every group's member and owner lists and
// the member index, and leaves other members alone.
func TestDeleteUserLeavesGroups(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	bossID, err := auth.CreateUser(applier, md, "boss@keep.test", "Boss", "adminpass123", true)
	if err != nil {
		t.Fatal(err)
	}
	aID, err := auth.CreateUser(applier, md, "a@d.test", "A", "passpass123", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.Set([]byte("/system/domain/keep.test"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	groups := store.NewGroupStore(h.store, applier)
	g, err := groups.CreateGroup(&bongov1.Group{Address: "team@keep.test", MemberIds: []string{aID, bossID}, OwnerIds: []string{aID}})
	if err != nil {
		t.Fatal(err)
	}

	if w := deleteUserRequest(t, h, aID, bossID); w.Code >= 400 {
		t.Fatalf("DeleteUser = %d", w.Code)
	}
	got, err := groups.GetGroup(g.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MemberIds) != 1 || got.MemberIds[0] != bossID || len(got.OwnerIds) != 0 {
		t.Errorf("group after delete = members %v owners %v", got.MemberIds, got.OwnerIds)
	}
	if mine, _ := groups.GroupsForUser(aID); len(mine) != 0 {
		t.Errorf("deleted user still indexed in %d groups", len(mine))
	}
}

// The group cascade is best-effort: the user is gone either way.
func TestDeleteUserGroupCascadeFailureIsBestEffort(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	bossID, _ := auth.CreateUser(applier, md, "boss@keep.test", "Boss", "adminpass123", true)
	aID, _ := auth.CreateUser(applier, md, "a@d.test", "A", "passpass123", false)
	h.applier = groupFailApplier{applier}

	w := deleteUserRequest(t, h, aID, bossID)
	if loc := w.Header().Get("Location"); strings.Contains(loc, "with-errors") || w.Code >= 400 {
		t.Fatalf("code=%d location=%q, want a plain success", w.Code, loc)
	}
	if _, err := h.store.Get([]byte("/user/" + aID + "/profile")); err == nil {
		t.Error("user not deleted")
	}
}
