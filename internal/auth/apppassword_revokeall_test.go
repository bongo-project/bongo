package auth

import (
	"errors"
	"fmt"
	"testing"
)

func TestMaskAppPassword(t *testing.T) {
	if got, want := MaskAppPassword("abcdef"), "abcdef-******-******-******"; got != want {
		t.Fatalf("MaskAppPassword = %q, want %q", got, want)
	}
}

// failDeleteRecords fails the nth Delete, so RevokeAll's partial-failure
// contract (stop, report the count, leave the rest working) is observable.
type failDeleteRecords struct {
	AppPasswordRecords
	deletes int
	failAt  int
}

func (f *failDeleteRecords) Delete(userID, id string) error {
	f.deletes++
	if f.deletes == f.failAt {
		return errors.New("raft timeout")
	}
	return f.AppPasswordRecords.Delete(userID, id)
}

func TestRevokeAll(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	e.user(t, "u2", "b@example.com", "pw")
	// A successful attempt records its use in a detached write.
	defer e.apps.Wait()
	var pws []string
	for _, l := range []string{"a", "b", "c"} {
		pws = append(pws, e.create(t, "u1", l))
	}
	other := e.create(t, "u2", "other")

	// Control: each credential works before the revoke, so the failures
	// below are the revoke's doing.
	for _, pw := range pws {
		if _, _, err := e.attempt("a@example.com", pw); err != nil {
			t.Fatalf("control: credential refused before revoke: %v", err)
		}
	}

	n, err := e.apps.RevokeAll("u1")
	if err != nil || n != 3 {
		t.Fatalf("RevokeAll = %d, %v; want 3, nil", n, err)
	}
	for _, pw := range pws {
		if _, _, err := e.attempt("a@example.com", pw); err == nil {
			t.Errorf("revoked credential still authenticates")
		}
	}
	if _, _, err := e.attempt("a@example.com", "pw"); err != nil {
		t.Errorf("account password stopped working: %v", err)
	}
	if _, _, err := e.attempt("b@example.com", other); err != nil {
		t.Errorf("another user's credential was revoked: %v", err)
	}
	if n, err := e.apps.RevokeAll("u1"); err != nil || n != 0 {
		t.Errorf("repeat RevokeAll = %d, %v", n, err)
	}
}

type fakeLinks struct {
	deleted []string
	err     error
}

func (f *fakeLinks) DeleteAll(userID string) error {
	f.deleted = append(f.deleted, userID)
	return f.err
}

// Revoke all does not move the session epoch, so a pending device link must go
// with the credentials; if the links cannot be removed nothing is revoked.
func TestRevokeAllDeletesDeviceLinksFirst(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	e.create(t, "u1", "a")
	links := &fakeLinks{}
	e.apps.SetDeviceLinks(links)

	if n, err := e.apps.RevokeAll("u1"); err != nil || n != 1 {
		t.Fatalf("RevokeAll = %d, %v", n, err)
	}
	if len(links.deleted) != 1 || links.deleted[0] != "u1" {
		t.Errorf("device links deleted for %v, want [u1]", links.deleted)
	}

	e.create(t, "u1", "b")
	links.err = errors.New("raft timeout")
	n, err := e.apps.RevokeAll("u1")
	if err == nil || n != 0 {
		t.Fatalf("RevokeAll with a failing link delete = %d, %v; want 0 and an error", n, err)
	}
	if list, _ := e.apps.List("u1"); len(list) != 1 {
		t.Errorf("%d credentials left, want the 1 that was not touched", len(list))
	}
}

func TestAtLimit(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	for i := range MaxAppPasswordsPerUser {
		if at, err := e.apps.AtLimit("u1"); err != nil || at {
			t.Fatalf("at %d credentials: AtLimit = %v, %v", i, at, err)
		}
		e.create(t, "u1", fmt.Sprintf("d%d", i))
	}
	if at, err := e.apps.AtLimit("u1"); err != nil || !at {
		t.Errorf("at the cap: AtLimit = %v, %v", at, err)
	}
	if at, _ := e.apps.AtLimit("u2"); at {
		t.Error("another user reported at the cap")
	}
}

func TestRevokeAllPartialFailure(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	for _, l := range []string{"a", "b", "c"} {
		e.create(t, "u1", l)
	}
	f := &failDeleteRecords{AppPasswordRecords: e.recs, failAt: 2}
	svc := NewAppPasswords(f, nil)
	n, err := svc.RevokeAll("u1")
	if err == nil || n != 1 {
		t.Fatalf("RevokeAll = %d, %v; want 1 and an error", n, err)
	}
	if list, _ := e.apps.List("u1"); len(list) != 2 {
		t.Errorf("%d credentials left, want 2", len(list))
	}
	// A repeat finishes the job.
	f.failAt = 0
	if n, err := svc.RevokeAll("u1"); err != nil || n != 2 {
		t.Errorf("repeat = %d, %v", n, err)
	}
}
