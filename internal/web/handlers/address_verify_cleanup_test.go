package handlers

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// barrierApplier makes the Put of one key wait for a second writer, so two
// SubmitCode calls that both passed the availability check meet at the write.
// With the per-address lock the second caller is held back, so the first gives
// up after the timeout and proceeds alone.
type barrierApplier struct {
	*directApplierTags
	key     string
	timeout time.Duration

	mu      sync.Mutex
	arrived int
	both    chan struct{}
}

func (a *barrierApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && string(cmd.Key) == a.key {
		a.mu.Lock()
		a.arrived++
		if a.arrived == 2 {
			close(a.both)
		}
		a.mu.Unlock()
		select {
		case <-a.both:
		case <-time.After(a.timeout):
		}
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

func TestSubmitCode_ConcurrentSameAddressOneOwner(t *testing.T) {
	const addr = "ext@example.net"
	env := newVerifyTestEnvFull(t)
	env.seedUser(t, "userA", "a@example.com")
	env.seedUser(t, "userB", "b@example.com")

	gate := &barrierApplier{
		directApplierTags: env.applier,
		key:               "/system/address/" + addr,
		timeout:           300 * time.Millisecond,
		both:              make(chan struct{}),
	}
	env.handler.SetAddressStore(store.NewAddressStore(env.ps, gate, env.meta.System))
	env.addresses = store.NewAddressStore(env.ps, env.applier, env.meta.System)

	codes := map[string]string{"userA": "111111", "userB": "222222"}
	for uid, code := range codes {
		if err := env.verifyStore.Put(uid, addr, &bongov1.AddressVerification{
			CodeHash:  hashVerificationCode([]byte(code)),
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	locs := map[string]string{}
	var mu sync.Mutex
	for uid, code := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := env.submitCodeHTTP(t, uid, uid+"@example.com", addr, code)
			if w.Code != http.StatusSeeOther {
				t.Errorf("%s: status = %d", uid, w.Code)
			}
			mu.Lock()
			locs[uid] = w.Header().Get("Location")
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	var winner, loser string
	for uid, loc := range locs {
		switch {
		case strings.Contains(loc, "flash=Address+verified"):
			if winner != "" {
				t.Fatalf("both users verified %s: %v", addr, locs)
			}
			winner = uid
		case strings.Contains(loc, "error="):
			loser = uid
		}
	}
	if winner == "" || loser == "" {
		t.Fatalf("want exactly one winner and one refusal, got %v", locs)
	}
	rec, ok, err := env.addresses.Get(addr)
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}
	if rec.UserId != winner {
		t.Errorf("owner = %s, want winner %s", rec.UserId, winner)
	}
	env.assertNoClaim(t, loser, addr)
}
