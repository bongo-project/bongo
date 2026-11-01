package agent

import (
	"io"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func bounceCopyTestStore(t *testing.T) *store.PebbleStore {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	return ps
}

func seedBounceCopy(t *testing.T, ps *store.PebbleStore, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := ps.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
}

// putBounceCopyOwner writes a live owner profile and the domain owner key.
func putBounceCopyOwner(t *testing.T, ps *store.PebbleStore, domain, id string) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.UserProfile{Email: id + "@" + domain})
	if err != nil {
		t.Fatal(err)
	}
	seedBounceCopy(t, ps, map[string]string{
		"/user/" + id + "/profile":            string(data),
		"/system/domain/" + domain + "/owner": id,
	})
}

// The bounce copy goes to postmaster@<domain> only for a hosted, enabled
// domain. A disabled or unknown domain falls back to smtp.postmaster_account.
// An alias domain follows the resolver: with a disabled alias_of target it
// counts only while postmaster@ the alias still has a live target.
func TestBounceCopyHostedDomain(t *testing.T) {
	alias := func(aliasState, targetState string) map[string]string {
		m := map[string]string{
			"/system/domain/a.test":          aliasState,
			"/system/domain/a.test/alias_of": "t.test",
		}
		if targetState != "" {
			m["/system/domain/t.test"] = targetState
		}
		return m
	}
	tests := []struct {
		name   string
		seed   map[string]string
		owner  string // live owner id for the domain, "" for none
		domain string
		want   bool
	}{
		{"plain enabled", map[string]string{"/system/domain/on.test": "enabled"}, "", "on.test", true},
		{"plain disabled", map[string]string{"/system/domain/off.test": "disabled"}, "", "off.test", false},
		{"unknown", nil, "", "unknown.test", false},
		{"alias, target enabled", alias("enabled", "enabled"), "", "a.test", true},
		{"alias, target disabled, no owner", alias("enabled", "disabled"), "", "a.test", false},
		{"alias, target disabled, live alias owner", alias("enabled", "disabled"), "aowner", "a.test", true},
		{"alias disabled", alias("disabled", "enabled"), "", "a.test", false},
		{"alias_of target not hosted", alias("enabled", ""), "", "a.test", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps := bounceCopyTestStore(t)
			seedBounceCopy(t, ps, tc.seed)
			if tc.owner != "" {
				putBounceCopyOwner(t, ps, tc.domain, tc.owner)
			}
			a := &Agent{Config: config.Defaults(), Store: ps, logger: testLogger()}
			if got := a.bounceCopyHostedDomain(tc.domain); got != tc.want {
				t.Errorf("bounceCopyHostedDomain(%q) = %v, want %v", tc.domain, got, tc.want)
			}
		})
	}

	var nilStore Agent
	if nilStore.bounceCopyHostedDomain("on.test") {
		t.Error("an agent without a store reports a hosted domain")
	}
}

// bounceRecorder records the recipients of every Deliver call.
type bounceRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *bounceRecorder) Deliver(_ string, recipients []string, rd io.Reader) error {
	if _, err := io.Copy(io.Discard, rd); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), recipients...))
	return nil
}

// newBounceGenerator is the only constructor of the generator Run hands to the
// queue worker, so these cases cover what Run wires.
func TestNewBounceGenerator_PostmasterCopyWiring(t *testing.T) {
	seed := map[string]string{
		"/system/domain/hosted.test":     "enabled",
		"/system/domain/a.test":          "enabled",
		"/system/domain/a.test/alias_of": "t.test",
		"/system/domain/t.test":          "disabled",
	}
	tests := []struct {
		name    string
		flag    bool
		account string
		sender  string
		want    []string // recipient of each delivery, in order
	}{
		{"flag off", false, "Ops@Ops.test", "alice@hosted.test", []string{"alice@hosted.test"}},
		{"flag on, hosted sender", true, "", "alice@hosted.test", []string{"alice@hosted.test", "postmaster@hosted.test"}},
		{"flag on, unhosted sender uses trimmed lowercase account", true, "  Ops@Ops.test ", "alice@elsewhere.test",
			[]string{"alice@elsewhere.test", "ops@ops.test"}},
		{"flag on, alias of disabled target uses account", true, "ops@ops.test", "alice@a.test",
			[]string{"alice@a.test", "ops@ops.test"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ps := bounceCopyTestStore(t)
			seedBounceCopy(t, ps, seed)
			cfg := config.Defaults()
			cfg.Queue.BounceCopyPostmaster = tc.flag
			cfg.SMTP.PostmasterAccount = tc.account
			a := &Agent{Config: cfg, Store: ps, logger: testLogger()}
			rec := &bounceRecorder{}
			g := a.newBounceGenerator(rec)

			orig := &bongov1.OutboundMessage{SenderEmail: tc.sender}
			if err := g.GenerateAndDeliver(orig, []string{"bob@external.test"}, "550 5.1.1 user unknown"); err != nil {
				t.Fatalf("GenerateAndDeliver: %v", err)
			}
			if len(rec.calls) != len(tc.want) {
				t.Fatalf("deliveries = %v, want %d", rec.calls, len(tc.want))
			}
			for i, w := range tc.want {
				if len(rec.calls[i]) != 1 || rec.calls[i][0] != w {
					t.Errorf("delivery %d recipients = %v, want [%s]", i, rec.calls[i], w)
				}
			}
		})
	}
}
