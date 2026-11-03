package handlers

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const replyRID2 = "20301104T090000Z"

func bobAnswer(ps string) []string { return []string{replyBob + "=" + ps} }

// One REPLY with a master and an override answers both in one store write.
func TestApplyReplyMasterAndOverrides(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(fmt.Sprintf("override stored=%v", stored), func(t *testing.T) {
			e := newReplyEnv(t)
			sd := seed{rrule: "FREQ=WEEKLY", seq: 1}
			if stored {
				sd.override = replyRID2
			}
			e.seed(t, sd)
			e.apply(replyBob, replyOpts{seq: 1, attendees: bobAnswer("ACCEPTED"),
				overrides: []replyOverride{{rid: replyRID2, seq: 1, attendees: bobAnswer("DECLINED")}}})
			got := e.get(t, quotaUser)
			if rp := rawPartstatAt(t, got.IcalRaw, "", replyBob); rp != "ACCEPTED" {
				t.Errorf("master bob = %q", rp)
			}
			if structuredPartstat(got, replyBob) != "ACCEPTED" {
				t.Errorf("structured bob = %q", structuredPartstat(got, replyBob))
			}
			if rp := rawPartstatAt(t, got.IcalRaw, replyRID2, replyBob); rp != "DECLINED" {
				t.Errorf("override bob = %q", rp)
			}
			if n := e.writes.n.Load(); n != 1 {
				t.Errorf("writes = %d, want 1", n)
			}
		})
	}
}

func TestApplyReplyMultiVEvent(t *testing.T) {
	rid := func(week int) string {
		return time.Date(2030, 5, 6+7*week, 9, 0, 0, 0, time.UTC).Format("20060102T150405Z")
	}
	t.Run("cap is counted across the VEVENTs of one message", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1})
		for d := 1; d < maxReplyOverrides; d++ {
			e.apply(replyBob, replyOpts{rid: rid(d), seq: 1, attendees: bobAnswer("ACCEPTED")})
		}
		e.writes.n.Store(0)
		e.apply(replyBob, replyOpts{seq: 1, attendees: bobAnswer("TENTATIVE"), overrides: []replyOverride{
			{rid: rid(200), seq: 1, attendees: bobAnswer("DECLINED")},
			{rid: rid(201), seq: 1, attendees: bobAnswer("DECLINED")},
		}})
		got := e.get(t, quotaUser)
		if n := strings.Count(string(got.IcalRaw), "RECURRENCE-ID"); n != maxReplyOverrides {
			t.Errorf("overrides = %d, want %d", n, maxReplyOverrides)
		}
		if rp := rawPartstatAt(t, got.IcalRaw, "", replyBob); rp != "TENTATIVE" {
			t.Errorf("master bob = %q, want applied", rp)
		}
		if rawPartstatAt(t, got.IcalRaw, rid(200), replyBob) != "DECLINED" || rawPartstatAt(t, got.IcalRaw, rid(201), replyBob) != "-" {
			t.Error("exactly the first new override should exist")
		}
		if n := e.writes.n.Load(); n != 1 {
			t.Errorf("writes = %d, want 1", n)
		}
		e.apply(replyBob, replyOpts{noMaster: true, overrides: []replyOverride{{rid: rid(1), seq: 1, attendees: bobAnswer("TENTATIVE")}}})
		if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, rid(1), replyBob); rp != "TENTATIVE" {
			t.Errorf("existing override at cap = %q", rp)
		}
	})
	t.Run("sender not on the series: nothing written", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1, noBob: true})
		e.apply(replyBob, replyOpts{seq: 1, attendees: bobAnswer("ACCEPTED"),
			overrides: []replyOverride{{rid: rid(1), seq: 1, attendees: bobAnswer("DECLINED")}}})
		if e.writes.n.Load() != 0 {
			t.Error("a party crasher changed the event")
		}
	})
	t.Run("stored override without the sender is untouched", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1, override: replyRID})
		ev := e.get(t, quotaUser)
		ev.IcalRaw = []byte(strings.ReplaceAll(string(ev.IcalRaw),
			"ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:"+replyBob+"\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:"+replyCarol+"\r\nEND:VEVENT\r\nEND:VCALENDAR",
			"ATTENDEE;PARTSTAT=ACCEPTED:mailto:"+replyCarol+"\r\nEND:VEVENT\r\nEND:VCALENDAR"))
		if err := e.events.Update(quotaUser, ev.Guid, ev); err != nil {
			t.Fatal(err)
		}
		if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, replyRID, replyBob); rp != "" {
			t.Fatalf("setup: override still lists bob: %q", rp)
		}
		e.writes.n.Store(0)
		// The override's own SEQUENCE is 2, so only the missing sender refuses
		// this VEVENT, and with no master in the reply nothing may be written.
		e.apply(replyBob, replyOpts{noMaster: true,
			overrides: []replyOverride{{rid: replyRID, seq: 2, attendees: bobAnswer("DECLINED")}}})
		if n := e.writes.n.Load(); n != 0 {
			t.Errorf("writes = %d, want 0", n)
		}
		if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, replyRID, replyBob); rp != "" {
			t.Errorf("override gained bob: %q", rp)
		}
	})
	t.Run("a newer override sequence refuses only that VEVENT", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1})
		e.apply(replyBob, replyOpts{seq: 1, attendees: bobAnswer("ACCEPTED"), overrides: []replyOverride{
			{rid: rid(1), seq: 5, attendees: bobAnswer("DECLINED")},
			{rid: rid(2), seq: 1, attendees: bobAnswer("DECLINED")},
		}})
		got := e.get(t, quotaUser)
		if rawPartstatAt(t, got.IcalRaw, rid(1), replyBob) != "-" {
			t.Error("the newer override was applied")
		}
		if rawPartstatAt(t, got.IcalRaw, rid(2), replyBob) != "DECLINED" || rawPartstatAt(t, got.IcalRaw, "", replyBob) != "ACCEPTED" {
			t.Error("the other VEVENTs were not applied")
		}
	})
	t.Run("overrides without a master", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1})
		e.apply(replyBob, replyOpts{noMaster: true, overrides: []replyOverride{
			{rid: rid(1), seq: 1, attendees: bobAnswer("DECLINED")},
			{rid: rid(2), seq: 1, attendees: bobAnswer("TENTATIVE")},
		}})
		got := e.get(t, quotaUser)
		if rawPartstatAt(t, got.IcalRaw, rid(1), replyBob) != "DECLINED" || rawPartstatAt(t, got.IcalRaw, rid(2), replyBob) != "TENTATIVE" {
			t.Error("both overrides should apply")
		}
		if rawPartstatAt(t, got.IcalRaw, "", replyBob) != "NEEDS-ACTION" || structuredPartstat(got, replyBob) != "NEEDS-ACTION" {
			t.Error("master changed by an override-only reply")
		}
		if n := e.writes.n.Load(); n != 1 {
			t.Errorf("writes = %d", n)
		}
	})
	t.Run("another attendee's line in an override is never read", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1})
		e.apply(replyBob, replyOpts{noMaster: true, overrides: []replyOverride{
			{rid: rid(1), seq: 1, attendees: []string{replyCarol + "=DECLINED", replyBob + "=ACCEPTED"}},
		}})
		got := e.get(t, quotaUser)
		if rawPartstatAt(t, got.IcalRaw, rid(1), replyBob) != "ACCEPTED" || rawPartstatAt(t, got.IcalRaw, rid(1), replyCarol) != "ACCEPTED" {
			t.Error("carol's line in the reply was read")
		}
	})
	t.Run("duplicate keys: first wins", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1})
		e.apply(replyBob, replyOpts{noMaster: true, overrides: []replyOverride{
			{rid: rid(1), seq: 1, attendees: bobAnswer("DECLINED")},
			{rid: rid(1), seq: 1, attendees: bobAnswer("ACCEPTED")},
		}})
		if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, rid(1), replyBob); rp != "DECLINED" {
			t.Errorf("got %q, want the first", rp)
		}
	})
	t.Run("duplicate keys: an unusable first VEVENT does not hide the answer", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1})
		e.apply(replyBob, replyOpts{noMaster: true, overrides: []replyOverride{
			{rid: rid(1), seq: 1, attendees: []string{}},
			{rid: rid(1), seq: 1, attendees: bobAnswer("DECLINED")},
		}})
		if rp := rawPartstatAt(t, e.get(t, quotaUser).IcalRaw, rid(1), replyBob); rp != "DECLINED" {
			t.Errorf("override bob = %q, want DECLINED", rp)
		}
	})
	t.Run("master-only reply on a series without raw stays without raw", func(t *testing.T) {
		e := newReplyEnv(t)
		e.seed(t, seed{rrule: "FREQ=WEEKLY", seq: 1, noRaw: true})
		e.apply(replyBob, replyOpts{seq: 1, attendees: bobAnswer("ACCEPTED")})
		got := e.get(t, quotaUser)
		if len(got.IcalRaw) != 0 || structuredPartstat(got, replyBob) != "ACCEPTED" {
			t.Errorf("raw = %d bytes, partstat = %q", len(got.IcalRaw), structuredPartstat(got, replyBob))
		}
	})
}
