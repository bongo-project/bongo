package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newAppPasswordEnv(t *testing.T) (*AppPasswordStore, *PebbleStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewAppPasswordStore(ps, &directApplier{store: ps}), ps
}

func TestValidAppPasswordID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"abcdef", true},
		{"zyxwvu", true},
		{"abcde", false},
		{"abcdefg", false},
		{"", false},
		{"abcdei", false}, // i is not in the alphabet
		{"abcdel", false},
		{"abcdeo", false},
		{"ABCDEF", false},
		{"abcde/", false},
		{"abc/ef", false},
		{"..abcd", false},
		{"abcde\x00", false},
	}
	for _, tt := range tests {
		if got := ValidAppPasswordID(tt.id); got != tt.want {
			t.Errorf("ValidAppPasswordID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestAppPasswordStoreCRUD(t *testing.T) {
	s, ps := newAppPasswordEnv(t)

	for _, id := range []string{"bbbbbb", "aaaaaa"} {
		if err := s.Put("u1", &bongov1.AppPassword{Id: id, Label: "phone", PasswordHash: []byte("h")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put("u2", &bongov1.AppPassword{Id: "cccccc", Label: "other"}); err != nil {
		t.Fatal(err)
	}

	rec, ok, err := s.Get("u1", "aaaaaa")
	if err != nil || !ok || rec.Label != "phone" {
		t.Fatalf("Get = %v %v %v", rec, ok, err)
	}
	if _, ok, _ := s.Get("u2", "aaaaaa"); ok {
		t.Error("record of u1 visible under u2")
	}

	list, err := s.List("u1")
	if err != nil || len(list) != 2 || list[0].Id != "aaaaaa" || list[1].Id != "bbbbbb" {
		t.Fatalf("List = %v %v", list, err)
	}

	if err := s.Delete("u1", "aaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("u1", "aaaaaa"); err != nil {
		t.Fatalf("second delete not idempotent: %v", err)
	}
	if _, ok, _ := s.Get("u1", "aaaaaa"); ok {
		t.Error("record survived delete")
	}
	if _, err := ps.Get(AppPasswordKey("u1", "aaaaaa")); !errors.Is(err, ErrNotFound) {
		t.Errorf("key still in pebble: %v", err)
	}
}

func TestAppPasswordStoreRejectsInvalidID(t *testing.T) {
	s, _ := newAppPasswordEnv(t)
	for _, id := range []string{"", "short", "../../x", "abcdef/gh", "ABCDEF"} {
		if err := s.Put("u1", &bongov1.AppPassword{Id: id}); !errors.Is(err, ErrInvalidAppPasswordID) {
			t.Errorf("Put(%q) = %v", id, err)
		}
		if _, _, err := s.Get("u1", id); !errors.Is(err, ErrInvalidAppPasswordID) {
			t.Errorf("Get(%q) = %v", id, err)
		}
		if err := s.Delete("u1", id); !errors.Is(err, ErrInvalidAppPasswordID) {
			t.Errorf("Delete(%q) = %v", id, err)
		}
		if err := s.Touch("u1", id, 1_700_000_000); !errors.Is(err, ErrInvalidAppPasswordID) {
			t.Errorf("Touch(%q) = %v", id, err)
		}
	}
}

func TestDayStart(t *testing.T) {
	const day = int64(1_700_006_400) // 2023-11-15 00:00:00 UTC
	for _, tt := range []struct{ in, want int64 }{
		{day, day},
		{day + 1, day},
		{day + 86399, day},
		{day + 86400, day + 86400},
	} {
		if got := DayStart(tt.in); got != tt.want {
			t.Errorf("DayStart(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestAppPasswordTouch(t *testing.T) {
	const day = int64(1_700_006_400)
	s, _ := newAppPasswordEnv(t)
	if err := s.Put("u1", &bongov1.AppPassword{Id: "aaaaaa", Label: "x", PasswordHash: []byte("h")}); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		name string
		at   int64
		want int64
	}{
		{"first touch truncates to the day", day + 3600, day},
		{"same day again is a no-op", day + 7200, day},
		{"an earlier day never moves it back", day - 86400, day},
		{"a later day advances", day + 86400 + 5, day + 86400},
	}
	for _, st := range steps {
		if err := s.Touch("u1", "aaaaaa", st.at); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		rec, _, _ := s.Get("u1", "aaaaaa")
		if rec.LastUsedAt != st.want {
			t.Errorf("%s: last_used_at = %d, want %d", st.name, rec.LastUsedAt, st.want)
		}
		if string(rec.PasswordHash) != "h" || rec.Label != "x" {
			t.Errorf("%s: touch altered other fields: %v", st.name, rec)
		}
	}
}

// A touch that lands after a revoke must not bring the record back. The test
// applier dispatches the command; without that the touch would be stored as a
// literal key and this negative test would pass vacuously.
func TestAppPasswordTouchAfterRevokeLeavesNothing(t *testing.T) {
	s, ps := newAppPasswordEnv(t)
	if err := s.Put("u1", &bongov1.AppPassword{Id: "aaaaaa", Label: "x", PasswordHash: []byte("h")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("u1", "aaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch("u1", "aaaaaa", 1_700_006_400); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Get(AppPasswordKey("u1", "aaaaaa")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch resurrected a revoked record: %v", err)
	}
	// And nothing stored literally under the synthetic prefix.
	it, err := ps.PrefixScan([]byte("/__apppassword_touch__/"))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	if it.Next() {
		t.Errorf("literal synthetic key stored: %s", it.Key())
	}
}
