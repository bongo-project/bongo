package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type schedApplier struct {
	ps   *PebbleStore
	fail bool
}

func (a *schedApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if a.fail {
		return errors.New("raft down")
	}
	if cmd.Op == bongov1.Op_OP_PUT {
		return a.ps.Set(cmd.Key, cmd.Value)
	}
	return a.ps.Delete(cmd.Key)
}

func newSchedInbox(t *testing.T) (*ScheduleInboxStore, *schedApplier) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	ap := &schedApplier{ps: ps}
	s := NewScheduleInboxStore(ps, ap)
	var tick int64
	s.now = func() time.Time { tick++; return time.Unix(1000, tick) }
	return s, ap
}

func hexID(i int) string { return fmt.Sprintf("%016x", i) }

func TestScheduleInboxPlaceDedupesAndOrders(t *testing.T) {
	s, _ := newSchedInbox(t)
	for _, i := range []int{3, 1, 2} {
		if err := s.Place("u1", hexID(i), []byte(fmt.Sprint("item", i))); err != nil {
			t.Fatal(err)
		}
	}
	// A retried delivery places the same id again.
	if err := s.Place("u1", hexID(1), []byte("dup")); err != nil {
		t.Fatal(err)
	}
	items, err := s.List("u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (retry must not duplicate)", len(items))
	}
	if string(items[0].Data) != "item3" || string(items[2].Data) != "item2" {
		t.Errorf("not in arrival order: %q %q %q", items[0].Data, items[1].Data, items[2].Data)
	}
	if other, _ := s.List("u2"); len(other) != 0 {
		t.Errorf("another user's inbox has %d items", len(other))
	}
}

func TestScheduleInboxCap(t *testing.T) {
	s, _ := newSchedInbox(t)
	for i := range MaxScheduleInboxItems {
		if err := s.Place("u1", hexID(i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := s.List("u1")
	if len(items) != MaxScheduleInboxItems {
		t.Fatalf("at the cap: %d items, want %d", len(items), MaxScheduleInboxItems)
	}
	if err := s.Place("u1", hexID(MaxScheduleInboxItems), []byte("new")); err != nil {
		t.Fatal(err)
	}
	items, _ = s.List("u1")
	if len(items) != MaxScheduleInboxItems {
		t.Fatalf("over the cap: %d items, want %d", len(items), MaxScheduleInboxItems)
	}
	if string(items[len(items)-1].Data) != "new" {
		t.Error("newest item was not kept")
	}
	if got := items[0].Name[21:]; got != hexID(1) {
		t.Errorf("oldest kept = %s, want the second one placed (the first is dropped)", got)
	}
}

func TestScheduleInboxGetDeleteValidation(t *testing.T) {
	s, ap := newSchedInbox(t)
	if err := s.Place("u1", hexID(7), []byte("body")); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List("u1")
	it, err := s.Get("u1", items[0].Name)
	if err != nil || string(it.Data) != "body" {
		t.Fatalf("Get = %v, %v", it, err)
	}
	if _, err := s.Get("u2", items[0].Name); !errors.Is(err, ErrScheduleItemNotFound) {
		t.Errorf("another user's Get = %v, want not found", err)
	}
	for _, bad := range []string{"", "../profile", "x", items[0].Name + "/y"} {
		if _, err := s.Get("u1", bad); !errors.Is(err, ErrScheduleItemNotFound) {
			t.Errorf("Get(%q) = %v, want not found", bad, err)
		}
		if err := s.Delete("u1", bad); !errors.Is(err, ErrScheduleItemNotFound) {
			t.Errorf("Delete(%q) = %v, want refusal", bad, err)
		}
	}
	if err := s.Place("u1", "../x", nil); err == nil {
		t.Error("Place accepted an invalid id")
	}
	ap.fail = true
	if err := s.Delete("u1", items[0].Name); err == nil {
		t.Error("Delete hid an applier failure")
	}
	if err := s.Place("u1", hexID(9), []byte("z")); err == nil {
		t.Error("Place hid an applier failure")
	}
	ap.fail = false
	if err := s.Delete("u1", items[0].Name); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.List("u1"); len(left) != 0 {
		t.Errorf("%d items left after delete", len(left))
	}
}

// A key under the prefix that is not an item name (written by something else)
// is neither listed nor readable through the item API.
func TestScheduleInboxIgnoresForeignKeys(t *testing.T) {
	s, ap := newSchedInbox(t)
	if err := ap.ps.Set([]byte("/user/u1/schedinbox/odd"), []byte("not ours")); err != nil {
		t.Fatal(err)
	}
	if err := s.Place("u1", hexID(1), []byte("ours")); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List("u1")
	if len(items) != 1 || string(items[0].Data) != "ours" {
		t.Errorf("items = %+v, want only the placed one", items)
	}
	if _, err := s.Get("u1", "odd"); !errors.Is(err, ErrScheduleItemNotFound) {
		t.Errorf("Get(odd) = %v, want not found", err)
	}
}
