package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCalendarStore_Mutate(t *testing.T) {
	cs := newCalendarTestEnv(t)
	guid, err := cs.Create("u1", &bongov1.Calendar{Name: "A", PublishToken: []byte("t")})
	if err != nil {
		t.Fatal(err)
	}

	if err := cs.Mutate("u1", guid, func(c *bongov1.Calendar) error {
		c.Name = "B"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := cs.Get("u1", guid)
	if got.Name != "B" || string(got.PublishToken) != "t" {
		t.Errorf("after mutate: %+v", got)
	}

	// An error from fn writes nothing.
	if err := cs.Mutate("u1", guid, func(c *bongov1.Calendar) error {
		c.Name = "C"
		return errors.New("refused")
	}); err == nil {
		t.Fatal("fn error swallowed")
	}
	if got, _ := cs.Get("u1", guid); got.Name != "B" {
		t.Errorf("a failed mutate wrote: %q", got.Name)
	}

	// A missing calendar is an error, not a created record.
	if err := cs.Mutate("u1", "nope", func(*bongov1.Calendar) error { return nil }); err == nil {
		t.Error("mutating a missing calendar succeeded")
	}
	if _, err := cs.Get("u1", "nope"); err == nil {
		t.Error("mutate created a calendar")
	}
}
