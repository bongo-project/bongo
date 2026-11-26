package agent

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

func TestCleanupSharedSubPass(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	subKeys := []string{"/user/u1/sharedsub/o/" + id, "/user/u2/sharedsub/o/" + id}
	keepKeys := []string{"/user/u1/sharedunsub/o/" + id, "/user/u1/profile", "/user/sharedsub/profile/x"}

	newEnv := func(t *testing.T) (*Agent, *store.PebbleStore, *recordingApplier) {
		ps, err := store.NewPebbleStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ps.Close() })
		app := &recordingApplier{ps: ps}
		a := &Agent{Store: ps, migrationApplier: app, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		for _, k := range append(append([]string{}, subKeys...), keepKeys...) {
			if err := ps.Set([]byte(k), []byte{1}); err != nil {
				t.Fatal(err)
			}
		}
		return a, ps, app
	}
	has := func(ps *store.PebbleStore, k string) bool {
		_, err := ps.Get([]byte(k))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}

	t.Run("follower does nothing", func(t *testing.T) {
		a, ps, app := newEnv(t)
		if a.cleanupSharedSubPass(func() bool { return false }) {
			t.Fatal("follower reported done")
		}
		if app.calls.Load() != 0 || !has(ps, subKeys[0]) || has(ps, string(sharedSubCleanupMarkerKey)) {
			t.Fatal("follower changed state")
		}
	})
	t.Run("leader deletes legacy keys only", func(t *testing.T) {
		a, ps, app := newEnv(t)
		if !a.cleanupSharedSubPass(func() bool { return true }) {
			t.Fatal("not done")
		}
		for _, k := range subKeys {
			if has(ps, k) {
				t.Fatalf("%s survived", k)
			}
		}
		for _, k := range keepKeys {
			if !has(ps, k) {
				t.Fatalf("%s was deleted", k)
			}
		}
		if !has(ps, string(sharedSubCleanupMarkerKey)) {
			t.Fatal("marker not set")
		}
		before := app.calls.Load()
		if !a.cleanupSharedSubPass(func() bool { return true }) || app.calls.Load() != before {
			t.Fatal("second run was not a no-op")
		}
	})
	t.Run("failure leaves marker unset and retries", func(t *testing.T) {
		a, ps, app := newEnv(t)
		app.failOn = 1
		if a.cleanupSharedSubPass(func() bool { return true }) {
			t.Fatal("reported done after a failure")
		}
		if has(ps, string(sharedSubCleanupMarkerKey)) {
			t.Fatal("marker written after a failure")
		}
		app.failOn = 0
		if !a.cleanupSharedSubPass(func() bool { return true }) {
			t.Fatal("retry did not finish")
		}
		for _, k := range subKeys {
			if has(ps, k) {
				t.Fatalf("%s survived the retry", k)
			}
		}
	})
}
