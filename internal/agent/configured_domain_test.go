package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// storeApplier applies commands straight to Pebble, failing deletes whose key
// starts with failDelete.
type storeApplier struct {
	ps         *store.PebbleStore
	failDelete string
}

func (a *storeApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.ps.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		if a.failDelete != "" && len(cmd.Key) >= len(a.failDelete) && string(cmd.Key[:len(a.failDelete)]) == a.failDelete {
			return errors.New("injected delete failure")
		}
		return a.ps.Delete(cmd.Key)
	}
	return nil
}

func TestSeedConfiguredDomain_PurgesStaleConfigFirst(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	const d = "example.test"
	stale := []string{
		"/system/domain_admin/" + d + "/old-admin",
		"/system/domain/" + d + "/owner",
		"/system/domain/" + d + "/dkim",
		"/system/catchall/" + d,
	}
	for _, k := range stale {
		if err := ps.Set([]byte(k), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{Store: ps, logger: testLogger()}

	a.seedConfiguredDomain(d, &storeApplier{ps: ps})

	for _, k := range stale {
		if _, err := ps.Get([]byte(k)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("stale key %s survived seeding (err=%v)", k, err)
		}
	}
	if v, err := ps.Get([]byte("/system/domain/" + d)); err != nil || string(v) != "enabled" {
		t.Errorf("domain key = %q, %v; want enabled", v, err)
	}
}

func TestSeedConfiguredDomain_PurgeFailureDoesNotCreate(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	const d = "example.test"
	if err := ps.Set([]byte("/system/catchall/"+d), []byte("x")); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Store: ps, logger: testLogger()}

	a.seedConfiguredDomain(d, &storeApplier{ps: ps, failDelete: "/system/catchall/"})

	if _, err := ps.Get([]byte("/system/domain/" + d)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("domain key created despite failed purge (err=%v)", err)
	}
}
