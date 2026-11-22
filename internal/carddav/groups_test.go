package carddav

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	groupUUID  = "A1B2C3D4-E5F6-4A5B-8C7D-0123456789AB"
	groupRef   = "a1b2c3d4e5f64a5b8c7d0123456789ab"
	groupGUID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	defaultSeg = "default"
)

func (e *davEnv) putCard(user, guid, body string) *httptest.ResponseRecorder {
	return e.do(user, "PUT", "/carddav/"+user+"/addressbooks/"+defaultSeg+"/"+guid+".vcf", body, "Content-Type", "text/vcard")
}

func (e *davEnv) getCard(user, guid string) *httptest.ResponseRecorder {
	return e.do(user, "GET", "/carddav/"+user+"/addressbooks/"+defaultSeg+"/"+guid+".vcf", "")
}

func TestCardDAVGroupRoundTripVCard4(t *testing.T) {
	e := newDAVEnv(t)
	body := "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:group\r\nFN:Team\r\nMEMBER:urn:uuid:" + groupUUID + "\r\nMEMBER:mailto:bob@example.com\r\nEND:VCARD\r\n"
	wantStatus(t, e.putCard("alice", groupGUID, body), http.StatusCreated, "put")

	got := e.getCard("alice", groupGUID).Body.String()
	for _, want := range []string{"KIND:group", "MEMBER:urn:uuid:" + groupUUID, "MEMBER:mailto:bob@example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("GET lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "X-ADDRESSBOOKSERVER") {
		t.Errorf("a vCard 4 group came back with Apple properties:\n%s", got)
	}

	c, err := e.contacts.Get("alice", groupGUID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "group" || len(c.Members) != 2 || c.Members[0] != groupRef || c.Members[1] != "mailto:bob@example.com" {
		t.Errorf("stored Kind=%q Members=%v", c.Kind, c.Members)
	}
}

func TestCardDAVGroupRoundTripApple(t *testing.T) {
	e := newDAVEnv(t)
	body := "BEGIN:VCARD\r\nVERSION:3.0\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nN:Team;;;;\r\nFN:Team\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:" + groupUUID + "\r\nEND:VCARD\r\n"
	wantStatus(t, e.putCard("alice", groupGUID, body), http.StatusCreated, "put")

	got := e.getCard("alice", groupGUID).Body.String()
	for _, want := range []string{"X-ADDRESSBOOKSERVER-KIND:group", "X-ADDRESSBOOKSERVER-MEMBER:urn:uuid:" + groupUUID} {
		if !strings.Contains(got, want) {
			t.Errorf("GET lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\nKIND:") || strings.Contains(got, "\nMEMBER:") {
		t.Errorf("an Apple group came back in vCard 4 form:\n%s", got)
	}
	c, _ := e.contacts.Get("alice", groupGUID)
	if c.Kind != "group" || len(c.Members) != 1 || c.Members[0] != groupRef {
		t.Errorf("stored Kind=%q Members=%v", c.Kind, c.Members)
	}
}

func TestCardDAVGroupOverCapForbidden(t *testing.T) {
	e := newDAVEnv(t)
	member := func(n int) string { return fmt.Sprintf("MEMBER:urn:uuid:%032x\r\n", n) }
	build := func(n int) string {
		var b strings.Builder
		b.WriteString("BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:group\r\nFN:Big\r\n")
		for i := 1; i <= n; i++ {
			b.WriteString(member(i))
		}
		b.WriteString("END:VCARD\r\n")
		return b.String()
	}

	wantStatus(t, e.putCard("alice", groupGUID, build(500)), http.StatusCreated, "500 members")

	over := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	wantStatus(t, e.putCard("alice", over, build(501)), http.StatusForbidden, "501 members, create")
	if _, err := e.contacts.Get("alice", over); err == nil {
		t.Error("a refused PUT stored the group")
	}
	// An update past the cap is refused too, and the stored group is unchanged.
	wantStatus(t, e.putCard("alice", groupGUID, build(501)), http.StatusForbidden, "501 members, update")
	c, _ := e.contacts.Get("alice", groupGUID)
	if len(c.Members) != 500 {
		t.Errorf("stored members = %d after a refused update", len(c.Members))
	}
}
