package handlers

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestGALMarksResources(t *testing.T) {
	e := newResourceEnv(t)
	sm, err := store.NewSearchManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sm.Close() })

	data, err := proto.Marshal(&bongov1.UserProfile{Email: "projector@example.org", DisplayName: "Projector", Kind: "resource", ResourceType: "equipment"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&directApplierTags{ps: e.q.ps}).ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/equip-1/profile"), Value: data,
	}, time.Second); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ id, email, name string }{
		{resID, resMail, "Room One"}, {"equip-1", "projector@example.org", "Projector"}, {aliceID, aliceAdr, "Room Alice"},
	} {
		if err := sm.IndexProfile(p.id, p.email, p.name); err != nil {
			t.Fatal(err)
		}
	}
	ch := NewComposeHandler(e.q.ps, nil, nil, sm, nil, discardLogger())

	search := func(q string) []map[string]string {
		req := httptest.NewRequest("GET", "/api/gal?q="+q, nil)
		req = req.WithContext(middleware.WithUserContext(req.Context(), aliceID, aliceAdr))
		rr := httptest.NewRecorder()
		ch.GALSearch(rr, req)
		var got []map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("%v: %s", err, rr.Body.String())
		}
		return got
	}

	got := search("pro")
	if len(got) != 1 || got[0]["kind"] != "resource" || got[0]["resource_type"] != "equipment" || got[0]["label"] == "" {
		t.Errorf("projector = %v", got)
	}
	kinds := map[string]string{}
	for _, g := range search("room") {
		kinds[g["email"]] = g["kind"] + "/" + g["resource_type"]
	}
	if kinds[resMail] != "resource/room" || kinds[aliceAdr] != "/" {
		t.Errorf("kinds = %v, want the room marked and the person not", kinds)
	}
}

// failDeletes refuses every delete.
type failDeletes struct{ inner store.RaftApplier }

func (f failDeletes) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_DELETE {
		return fmt.Errorf("raft unavailable")
	}
	return f.inner.ApplyCommand(cmd, d)
}

func TestCancelBooking(t *testing.T) {
	e := newResourceEnv(t)
	e.book(resID, aliceAdr, bookOpts{})
	guid := uidGuid("meet-1@example.org")

	t.Run("a failed delete fails and tells nobody", func(t *testing.T) {
		h := NewCalendarHandler(store.NewEventStore(e.q.ps, failDeletes{&directApplierTags{ps: e.q.ps}}), e.q.calendars, nil)
		h.SetProfiles(e.q.ps)
		h.SetInviteSender(e.fs)
		before := len(e.fs.sent)
		if err := h.CancelBooking(resID, guid); err == nil {
			t.Fatal("CancelBooking succeeded although the delete failed")
		}
		if e.count(t) != 1 || len(e.fs.sent) != before {
			t.Errorf("bookings %d, messages %d -> %d", e.count(t), before, len(e.fs.sent))
		}
	})
	t.Run("a person is not cancelled through it", func(t *testing.T) {
		if err := e.h.CancelBooking(aliceID, guid); err == nil {
			t.Error("CancelBooking worked on a person")
		}
	})
	t.Run("cancel deletes and the organiser is told", func(t *testing.T) {
		if err := e.h.CancelBooking(resID, guid); err != nil {
			t.Fatal(err)
		}
		if e.count(t) != 0 {
			t.Error("booking still there")
		}
		if got := e.answers(t); fmt.Sprint(got) != "[ACCEPTED DECLINED]" {
			t.Errorf("answers = %v, want the accept then the decline", got)
		}
	})
	t.Run("the list shows what is booked", func(t *testing.T) {
		e.book(resID, aliceAdr, bookOpts{uid: "again@example.org"})
		evs, err := e.h.Bookings(resID)
		if err != nil || len(evs) != 1 {
			t.Errorf("Bookings = %v, %v", evs, err)
		}
	})
}
