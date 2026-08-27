package carddav

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-vcard"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// directApplier applies commands straight to Pebble (no Raft), mirroring the
// fixture used by the other store-backed packages.
type directApplier struct{ store *store.PebbleStore }

func (a *directApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

func newTestBackend(t *testing.T) (*Backend, string) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	contacts := store.NewContactStore(ps, &directApplier{store: ps})
	return NewBackend(contacts, nil, slog.Default()), "test-user-1"
}

func testCard(fn, email string) vcard.Card {
	card := vcard.Card{}
	card.SetValue(vcard.FieldFormattedName, fn)
	card.SetValue(vcard.FieldEmail, email)
	vcard.ToV4(card)
	return card
}

// assertPreconditionFailed checks the error is the 412 this backend returns.
// go-webdav's HTTPError is in its internal package so the status code is not
// inspectable here; ErrPreconditionFailed is wrapped into every one of them
// precisely so this assertion does not depend on message text.
func assertPreconditionFailed(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a 412, got nil", what)
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Errorf("%s: error = %v, want a precondition failure", what, err)
	}
}

// TestCardDAVETagChangesWithContent is the foundation the conditional requests
// rest on. The ETag was the record's UpdatedAt in whole seconds, so two edits
// in the same second were indistinguishable and an If-Match built on it could
// be satisfied by a version the client never saw.
func TestCardDAVETagChangesWithContent(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := objectPath(userID, "c1")

	first, err := backend.PutAddressObject(ctx, path, testCard("Ada Lovelace", "ada@example.com"), nil)
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	second, err := backend.PutAddressObject(ctx, path, testCard("Ada L", "ada@example.com"), nil)
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first.ETag == "" || second.ETag == "" {
		t.Fatal("ETag is empty")
	}
	if first.ETag == second.ETag {
		t.Errorf("ETag did not change after an edit in the same second (%q): "+
			"a second-resolution ETag cannot support If-Match", first.ETag)
	}
}

// TestCardDAVIfMatchRejectsStaleWrite is the lost-update case: two clients hold
// the same contact, both edit, and the second write must be refused rather than
// silently overwriting the first.
func TestCardDAVIfMatchRejectsStaleWrite(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := objectPath(userID, "c1")

	original, err := backend.PutAddressObject(ctx, path, testCard("Original", "a@example.com"), nil)
	if err != nil {
		t.Fatalf("seed put: %v", err)
	}
	staleETag := original.ETag

	if _, err := backend.PutAddressObject(ctx, path, testCard("From A", "a@example.com"), nil); err != nil {
		t.Fatalf("client A put: %v", err)
	}

	_, err = backend.PutAddressObject(ctx, path, testCard("From B", "a@example.com"),
		&carddav.PutAddressObjectOptions{IfMatch: webdav.ConditionalMatch(staleETag)})
	assertPreconditionFailed(t, err, "stale If-Match")

	// Client A's version must survive.
	got, err := backend.contacts.Get(userID, "c1")
	if err != nil {
		t.Fatalf("get after refused write: %v", err)
	}
	if got.DisplayName != "From A" {
		t.Errorf("stored name = %q, want \"From A\": the refused write was applied anyway", got.DisplayName)
	}
}

// TestCardDAVIfMatchAcceptsCurrentETag is the control: refusing every
// conditional write would satisfy the test above while breaking every
// well-behaved client.
func TestCardDAVIfMatchAcceptsCurrentETag(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := objectPath(userID, "c1")

	original, err := backend.PutAddressObject(ctx, path, testCard("Original", "a@example.com"), nil)
	if err != nil {
		t.Fatalf("seed put: %v", err)
	}
	if _, err := backend.PutAddressObject(ctx, path, testCard("Updated", "a@example.com"),
		&carddav.PutAddressObjectOptions{IfMatch: webdav.ConditionalMatch(original.ETag)}); err != nil {
		t.Fatalf("a current If-Match was refused: %v", err)
	}
}

// TestCardDAVIfNoneMatchWildcard covers create-only semantics.
func TestCardDAVIfNoneMatchWildcard(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := objectPath(userID, "c1")

	if _, err := backend.PutAddressObject(ctx, path, testCard("New", "a@example.com"),
		&carddav.PutAddressObjectOptions{IfNoneMatch: webdav.ConditionalMatch("*")}); err != nil {
		t.Fatalf("If-None-Match: * on a new object was refused: %v", err)
	}

	_, err := backend.PutAddressObject(ctx, path, testCard("Clobber", "a@example.com"),
		&carddav.PutAddressObjectOptions{IfNoneMatch: webdav.ConditionalMatch("*")})
	assertPreconditionFailed(t, err, "If-None-Match: * on an existing object")
}

// TestCardDAVIfMatchOnMissingObject: conditionally updating something already
// deleted must fail rather than quietly recreating it.
func TestCardDAVIfMatchOnMissingObject(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)

	_, err := backend.PutAddressObject(ctx, objectPath(userID, "gone"),
		testCard("Ghost", "g@example.com"),
		&carddav.PutAddressObjectOptions{IfMatch: webdav.ConditionalMatch(`"deadbeef"`)})
	assertPreconditionFailed(t, err, "If-Match on a nonexistent object")
}

// --- Cross-account scoping -------------------------------------------------
//
// The CardDAV URL contains a {userID} segment which the backend deliberately
// ignores: the owner comes from the Basic-Auth principal in the request
// context. go-webdav hands REPORT multiget hrefs straight to
// GetAddressObject with no path validation, so that convention is the entire
// cross-account boundary for contact data. It had no test.

func twoUserBackend(t *testing.T) (*Backend, string, string, string) {
	t.Helper()
	backend, attacker := newTestBackend(t)
	victim := "victim-user"

	victimCtx := WithUserID(context.Background(), victim)
	if _, err := backend.PutAddressObject(victimCtx, objectPath(victim, "secret-1"),
		testCard("Victim Private Contact", "private@victim.test"), nil); err != nil {
		t.Fatalf("seeding victim contact: %v", err)
	}
	return backend, attacker, victim, "secret-1"
}

func TestCardDAVReadIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, guid := twoUserBackend(t)

	// Control first: the victim can read their own contact.
	if _, err := backend.GetAddressObject(WithUserID(context.Background(), victim),
		objectPath(victim, guid), nil); err != nil {
		t.Fatalf("victim cannot read their own contact: %v", err)
	}

	obj, err := backend.GetAddressObject(WithUserID(context.Background(), attacker),
		objectPath(victim, guid), nil)
	if err == nil {
		t.Fatalf("attacker read the victim's contact by requesting its URL path: %+v", obj)
	}
}

func TestCardDAVWriteIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, guid := twoUserBackend(t)

	_, err := backend.PutAddressObject(WithUserID(context.Background(), attacker),
		objectPath(victim, guid), testCard("Overwritten", "attacker@evil.test"), nil)

	got, gerr := backend.contacts.Get(victim, guid)
	if gerr != nil {
		t.Fatalf("victim's contact disappeared entirely (put err was %v): %v", err, gerr)
	}
	if got.DisplayName != "Victim Private Contact" {
		t.Errorf("attacker overwrote the victim's contact via its URL path: name is now %q",
			got.DisplayName)
	}
}

func TestCardDAVDeleteIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, guid := twoUserBackend(t)

	_ = backend.DeleteAddressObject(WithUserID(context.Background(), attacker),
		objectPath(victim, guid))

	if _, err := backend.contacts.Get(victim, guid); err != nil {
		t.Errorf("attacker deleted the victim's contact by requesting its URL path: %v", err)
	}
}

func TestCardDAVListingIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, _ := twoUserBackend(t)

	objs, err := backend.ListAddressObjects(WithUserID(context.Background(), attacker),
		"/carddav/"+victim+"/addressbooks/default/", nil)
	if err != nil {
		return // refusing outright is correctly scoped
	}
	for _, o := range objs {
		if strings.Contains(o.Path, victim) {
			t.Errorf("attacker listed a contact from the victim's address book: %s", o.Path)
		}
	}
}
