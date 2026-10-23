package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// lockedBuffer is a bytes.Buffer safe for concurrent slog writes and reads.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockedBuffer) count(sub string) int {
	return strings.Count(l.String(), sub)
}

const (
	revokedLogMsg = "startup: revoked identity address record"
	colliding     = "b@shadow.example"

	nonConflictExt   = "ext@external.example"
	nonConflictCatch = "any@catch.example"
)

// seedShadowedCollision writes live account B (profile plus users row) and a
// verified, send-capable identity owned by A at B's own address. Both share
// /system/address/{addr}, so the identity overwrites what B's primary would be.
func seedShadowedCollision(t *testing.T, ps *store.PebbleStore, sys *store.SystemIndex) {
	t.Helper()
	prof, err := proto.Marshal(&bongov1.UserProfile{Email: colliding})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/userB/profile"), prof); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.DB().Exec(
		`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
		 VALUES ('userB', ?, 'B', 1, unixepoch(), unixepoch())`, colliding); err != nil {
		t.Fatal(err)
	}
	rec, err := proto.Marshal(&bongov1.AddressRecord{
		UserId: "userA", Domain: "shadow.example", Kind: "identity", Verified: true, CanSend: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/system/address/"+colliding), rec); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileShadowedIdentities_LeaderRevokesOnRun(t *testing.T) {
	buf := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := blobGCLeaderTestAgentSeeded(t, logger, func(a *Agent) {
		seedShadowedCollision(t, a.Store, a.Metadata.System)
		// Reported but not revocable: no conflict, and a catch-all owner that
		// loses to a verified identity in resolver order.
		for addr, rec := range map[string]*bongov1.AddressRecord{
			nonConflictExt:   {UserId: "userA", Kind: "identity"},
			nonConflictCatch: {UserId: "userA", Kind: "identity", Verified: true},
		} {
			b, err := proto.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Set([]byte("/system/address/"+addr), b); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Store.Set(store.CatchAllKey("catch.example"), []byte("catcher")); err != nil {
			t.Fatal(err)
		}
	})

	// The migration runs after the pass in the same boot, so B's primary
	// record is rebuilt where the identity used to be.
	rec, ok, err := a.AddressStore.Get(colliding)
	if err != nil || !ok {
		t.Fatalf("address record = %+v ok=%v err=%v", rec, ok, err)
	}
	if rec.Kind != "primary" || rec.UserId != "userB" {
		t.Errorf("record = kind %q user %q, want B's primary", rec.Kind, rec.UserId)
	}
	if can, err := store.CanSendAs(a.AddressStore, "userA", "", colliding); err != nil || can {
		t.Errorf("CanSendAs(A) = %v, %v, want false", can, err)
	}
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)
	if got, err := resolver.ResolveRecipient(colliding); err != nil || got != "userB" {
		t.Errorf("ResolveRecipient = %q, %v, want userB", got, err)
	}

	logs := buf.String()
	var line string
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, revokedLogMsg) {
			line = l
		}
	}
	for _, want := range []string{"address=" + colliding, "user_id=userA", "conflict=account", "conflict_owner=userB"} {
		if !strings.Contains(line, want) {
			t.Errorf("revocation log line %q lacks %s", line, want)
		}
	}
	if n := buf.count(revokedLogMsg); n != 1 {
		t.Errorf("revocation lines = %d, want 1", n)
	}

	// Records without a revocable conflict survive on the leader and are
	// still reported.
	for _, addr := range []string{nonConflictExt, nonConflictCatch} {
		if r, ok, err := a.AddressStore.Get(addr); err != nil || !ok || r.Kind != "identity" {
			t.Errorf("%s = %+v ok=%v err=%v, want the identity kept", addr, r, ok, err)
		}
		if !strings.Contains(logs, "does not route mail") || !strings.Contains(logs, "address="+addr) {
			t.Errorf("%s not reported on the leader", addr)
		}
	}

	// Second run on the same agent: nothing left to revoke.
	a.reconcileShadowedIdentities()
	if n := buf.count(revokedLogMsg); n != 1 {
		t.Errorf("revocation lines after second run = %d, want 1", n)
	}
	again, ok, err := a.AddressStore.Get(colliding)
	if err != nil || !ok || again.Kind != "primary" || again.UserId != "userB" {
		t.Errorf("record after second run = %+v ok=%v err=%v", again, ok, err)
	}
}

// countingApplier counts and applies nothing.
type countingApplier struct{ n int }

func (c *countingApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	c.n++
	return nil
}

func newShadowedFollower(t *testing.T) (*Agent, *countingApplier, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	md, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { md.Close() })
	seedShadowedCollision(t, ps, md.System)

	applier := &countingApplier{}
	var buf bytes.Buffer
	a := &Agent{
		Store:        ps,
		Metadata:     md,
		AddressStore: store.NewAddressStore(ps, applier, md.System),
		logger:       slog.New(slog.NewTextHandler(&buf, nil)),
	}
	return a, applier, &buf
}

func TestReconcileShadowedIdentities_FollowerDoesNotRevoke(t *testing.T) {
	a, applier, buf := newShadowedFollower(t)
	a.reconcileShadowedIdentities()

	if applier.n != 0 {
		t.Errorf("follower applied %d commands, want 0", applier.n)
	}
	rec, ok, err := a.AddressStore.Get(colliding)
	if err != nil || !ok || rec.Kind != "identity" || rec.UserId != "userA" {
		t.Errorf("record = %+v ok=%v err=%v, want the identity kept", rec, ok, err)
	}
	if strings.Contains(buf.String(), revokedLogMsg) {
		t.Errorf("follower logged a revocation: %s", buf.String())
	}
	// The report still runs on every node.
	if !strings.Contains(buf.String(), "does not route mail") {
		t.Errorf("follower did not report the record: %s", buf.String())
	}
}

func TestReconcileShadowedIdentities_NonConflictsSurviveAndAreReported(t *testing.T) {
	a, applier, buf := newShadowedFollower(t)
	// Direct Pebble writes: the counting applier applies nothing.
	raw := func(addr string, rec *bongov1.AddressRecord) {
		t.Helper()
		b, _ := proto.Marshal(rec)
		if err := a.Store.Set([]byte("/system/address/"+addr), b); err != nil {
			t.Fatal(err)
		}
	}
	raw("ext@external.example", &bongov1.AddressRecord{UserId: "userA", Kind: "identity"})
	raw("any@catch.example", &bongov1.AddressRecord{UserId: "userA", Kind: "identity", Verified: true})
	if err := a.Store.Set(store.CatchAllKey("catch.example"), []byte("catcher")); err != nil {
		t.Fatal(err)
	}
	before := applier.n
	a.reconcileShadowedIdentities()
	if applier.n != before {
		t.Errorf("applied %d commands", applier.n-before)
	}
	for _, addr := range []string{"ext@external.example", "any@catch.example"} {
		if _, ok, _ := a.AddressStore.Get(addr); !ok {
			t.Errorf("%s was removed", addr)
		}
		if !strings.Contains(buf.String(), "address="+addr) {
			t.Errorf("%s not reported", addr)
		}
	}
}

func TestRevokeIfUnchanged(t *testing.T) {
	scanned := store.ShadowedIdentity{Address: colliding, UserID: "userA"}
	tests := []struct {
		name    string
		rec     *bongov1.AddressRecord
		deleted bool
	}{
		{"unchanged identity", &bongov1.AddressRecord{UserId: "userA", Kind: "identity", Verified: true}, true},
		{"rewritten as primary", &bongov1.AddressRecord{UserId: "userA", Kind: "primary", Verified: true}, false},
		{"re-owned identity", &bongov1.AddressRecord{UserId: "userC", Kind: "identity", Verified: true}, false},
		{"already gone", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, applier, _ := newShadowedFollower(t)
			if tt.rec == nil {
				if err := a.Store.Delete([]byte("/system/address/" + colliding)); err != nil {
					t.Fatal(err)
				}
			} else {
				b, _ := proto.Marshal(tt.rec)
				if err := a.Store.Set([]byte("/system/address/"+colliding), b); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.revokeIfUnchanged(scanned)
			if err != nil || got != tt.deleted {
				t.Fatalf("revokeIfUnchanged = %v, %v, want %v", got, err, tt.deleted)
			}
			if wantApplies := map[bool]int{true: 1, false: 0}[tt.deleted]; applier.n != wantApplies {
				t.Errorf("applied %d commands, want %d", applier.n, wantApplies)
			}
		})
	}
}
