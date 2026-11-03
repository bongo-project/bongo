package handlers

import (
	"net/http"
	"strings"
	"testing"
)

// The cap sits between 49 and 51 grants: the 50th grantee is accepted and the
// 51st refused. Changing the comparison either way turns one assertion red.
func TestShareCalendarCapBoundary(t *testing.T) {
	e := newShareEnv(t, true)
	if err := e.acl.RevokeCalendarGrant("alice", e.fullID, "filler-aa"); err != nil {
		t.Fatal(err)
	}
	if got := len(e.shared(t, e.fullID)); got != maxCalendarShares-1 {
		t.Fatalf("fixture has %d grants, want %d", got, maxCalendarShares-1)
	}
	if rr := e.share("alice", e.fullID, "bob@d.test"); rr.Code != http.StatusOK {
		t.Fatalf("50th grantee: status = %d %q, want 200", rr.Code, rr.Body)
	}
	if got := len(e.shared(t, e.fullID)); got != maxCalendarShares {
		t.Fatalf("grants after the 50th = %d, want %d", got, maxCalendarShares)
	}
	if rr := e.share("alice", e.fullID, "dave@ca.test"); rr.Code != http.StatusBadRequest {
		t.Fatalf("51st grantee: status = %d %q, want 400", rr.Code, rr.Body)
	}
	if got := len(e.shared(t, e.fullID)); got != maxCalendarShares {
		t.Errorf("grants after the refused 51st = %d, want %d", got, maxCalendarShares)
	}
}

// The card shows the time in the user's zone, not UTC. 09:00 UTC on a winter
// date is 10:00 in Berlin; a UTC assertion would pass whether or not the zone
// were applied.
func TestInvitationCardShowsUserZone(t *testing.T) {
	e := newImipEnv(t)
	e.cal.SetZoneSource(fixedZone{mustZone(t, "Europe/Berlin")})
	uid := e.invite(t, icsOpts{start: "20261118T090000Z", end: "20261118T100000Z"})
	body := e.view(t, uid)
	if !strings.Contains(body, "2026-11-18 10:00 - 11:00 CET") {
		t.Error("card lacks the Berlin time 2026-11-18 10:00 - 11:00 CET")
	}
	if strings.Contains(body, "09:00") {
		t.Error("card shows the UTC time")
	}
}

// A COUNTER or REFRESH is not an invitation the user can answer: no card, no
// answer form. Each has a DTSTART so Parse accepts it and the method filter,
// not a malformed-part refusal, is what keeps the card away. The REQUEST
// control shows the same fixture does render a card.
func TestCounterAndRefreshShowNoInvitationCard(t *testing.T) {
	control := newImipEnv(t)
	body := control.view(t, control.invite(t, icsOpts{}))
	if !strings.Contains(body, "invite-card") || !strings.Contains(body, "/invitation") {
		t.Fatal("control REQUEST renders no card")
	}
	for _, method := range []string{"COUNTER", "REFRESH"} {
		t.Run(method, func(t *testing.T) {
			e := newImipEnv(t)
			ics := icsOpts{}.build(method)
			uid := e.deliver(t, quotaUser, inviteMail(method, ics, "Authentication-Results: mx.test; spf=pass; dmarc=pass\r\n"))
			body := e.view(t, uid)
			for _, banned := range []string{"invite-card", "/invitation"} {
				if strings.Contains(body, banned) {
					t.Errorf("%s message renders %q", method, banned)
				}
			}
			if w := e.answer(t, uid, "ACCEPTED"); w.Code != http.StatusUnprocessableEntity {
				t.Errorf("answer status %d, want 422", w.Code)
			}
			e.noEvent(t)
			if len(e.fs.messages()) != 0 {
				t.Error("an answer to a COUNTER or REFRESH sent mail")
			}
		})
	}
}
