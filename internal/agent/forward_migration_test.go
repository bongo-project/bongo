package agent

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// putForwardPref stores a user's account-level forward address.
func putForwardPref(t *testing.T, a *Agent, userID, target string) {
	t.Helper()
	b, err := proto.Marshal(&bongov1.UserPreferences{ForwardAddress: target})
	if err != nil {
		t.Fatalf("marshal prefs: %v", err)
	}
	if err := a.Store.Set([]byte("/user/"+userID+"/preferences"), b); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
}

// The grandfathering migration runs once. A forward saved after it has run
// (by an old-version node, restored from a backup) must go through
// verification, not be grandfathered by the next boot.
func TestMigrateForwardTargetsRunsOnce(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	ft := a.forwardTargetStore()

	done, err := ft.MigrationDone()
	if err != nil || !done {
		t.Fatalf("migration marker after first boot = %v, %v; want done", done, err)
	}

	const target = "late@elsewhere.example"

	putForwardPref(t, a, "user-late", target)
	a.migrateForwardTargets()
	if _, exists, err := ft.Get("user-late", target); err != nil || exists {
		t.Fatalf("forward added after the first run was grandfathered by the second (exists=%v, err=%v)", exists, err)
	}

	// Control: with the marker removed the same forward is grandfathered, so
	// the assertion above is about the marker and not about the data.
	if err := a.Store.Delete([]byte("/system/migration/fwdtargets")); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	a.migrateForwardTargets()
	rec, exists, err := ft.Get("user-late", target)
	if err != nil || !exists || rec.State != store.ForwardStateGrandfathered {
		t.Fatalf("control: record = %v exists=%v err=%v, want grandfathered", rec, exists, err)
	}
}

// A verified send-as identity on an external domain resolves to its owner but
// is not a hosted address: forwarding to it needs the forwarder's own
// verification, and the migration must not exempt it either.
func TestHostedForwardTargetIgnoresExternalIdentities(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)

	if err := a.AddressStore.Put("alice@gmail.example", &bongov1.AddressRecord{
		UserId: "user-alice", Domain: "gmail.example", Kind: "identity", Verified: true,
	}); err != nil {
		t.Fatalf("put identity: %v", err)
	}
	if _, err := resolver.ResolveRecipient("alice@gmail.example"); err != nil {
		t.Fatalf("precondition: identity must resolve: %v", err)
	}
	if err := a.Store.Set([]byte("/system/domain/hosted.example"), []byte("x")); err != nil {
		t.Fatalf("put domain: %v", err)
	}
	if err := a.AddressStore.Put("carol@hosted.example", &bongov1.AddressRecord{
		UserId: "user-carol", Domain: "hosted.example", Kind: "alias",
	}); err != nil {
		t.Fatalf("put alias: %v", err)
	}

	hosted := a.hostedForwardTarget(resolver)
	if hosted("alice@gmail.example") {
		t.Error("external verified identity treated as hosted")
	}
	if !hosted("carol@hosted.example") {
		t.Error("resolvable address on a hosted domain not treated as hosted")
	}
	if hosted("nobody@hosted.example") {
		t.Error("unresolvable address on a hosted domain treated as hosted")
	}
}
