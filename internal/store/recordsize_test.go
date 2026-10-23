package store

import (
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestEventRecordSizeMatchesStoredBytes(t *testing.T) {
	es := newEventTestEnv(t)
	ev := &bongov1.Event{Title: "Standup", Description: strings.Repeat("d", 1337), Location: "Room 7", StartTime: 1_700_000_000}
	est := EventRecordSize(ev)
	guid, err := es.Create("u1", ev)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := es.pebble.Get([]byte("/user/u1/event/" + guid))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != est {
		t.Fatalf("stored %d bytes, estimate %d", len(raw), est)
	}
}

func TestContactRecordSizeMatchesStoredBytes(t *testing.T) {
	cs := newContactTestEnv(t)
	c := &bongov1.Contact{DisplayName: "Ada", Email: "ada@x.example", Notes: strings.Repeat("n", 613)}
	est := ContactRecordSize(c)
	guid, err := cs.Create("u1", c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cs.pebble.Get([]byte("/user/u1/contact/" + guid))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != est {
		t.Fatalf("stored %d bytes, estimate %d", len(raw), est)
	}
}
