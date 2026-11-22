package carddav

import (
	"net/http"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCardDAVDeleteClearsSelfContact(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.putCard("alice", "me1", vcardBody), http.StatusCreated, "put")
	if err := e.contacts.SetSelfContact("alice", "me1"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.do("alice", "DELETE", "/carddav/alice/addressbooks/default/me1.vcf", ""), http.StatusNoContent, "delete")
	if e.contacts.IsSelfContact("alice", "me1") {
		t.Fatal("the preference survives a CardDAV delete")
	}
	// A new card PUT at the same href must not inherit the mark.
	wantStatus(t, e.putCard("alice", "me1", vcardBody), http.StatusCreated, "put again")
	if _, ok := e.contacts.SelfContact("alice"); ok {
		t.Error("a new card at the same href became This is me")
	}
}

func TestCardDAVGranteeDeleteClearsOwnersSelfContact(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	if err := e.contacts.SetSelfContact(shOwner, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.contacts.Create(shWriter, &bongov1.Contact{Guid: "w1", DisplayName: "Writer"}); err != nil {
		t.Fatal(err)
	}
	if err := e.contacts.SetSelfContact(shWriter, "w1"); err != nil {
		t.Fatal(err)
	}
	w := e.do(shWriter, "DELETE", "/carddav/"+shWriter+"/addressbooks/"+sharedSeg(shOwner, shBook)+"/c1.vcf", "")
	wantStatus(t, w, http.StatusNoContent, "grantee delete")
	if _, err := e.contacts.Get(shOwner, "c1"); err == nil {
		t.Fatal("the contact was not deleted")
	}
	if e.contacts.IsSelfContact(shOwner, "c1") {
		t.Error("the owner's preference survives")
	}
	if !e.contacts.IsSelfContact(shWriter, "w1") {
		t.Error("the grantee's own preference changed")
	}
}
