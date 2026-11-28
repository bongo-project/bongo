package deliver

import (
	"errors"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeFeatures is a store.FeatureLookup over "user|feature" pairs. A user in
// errUsers gets a read error and, like the real lookup, the fail-closed
// answer.
type fakeFeatures struct {
	off      map[string]bool
	errUsers map[string]bool
}

func (f fakeFeatures) FeatureDisabled(userID, feature string) (bool, error) {
	if f.errUsers[userID] {
		return true, errors.New("pebble unavailable")
	}
	return f.off[userID+"|"+feature], nil
}

var _ store.FeatureLookup = fakeFeatures{}

func TestForwardingSwitchedOff(t *testing.T) {
	aliceOK := &fakeTargets{allowed: map[string]bool{"user-alice|" + extTarget: true}}
	on := fakeFeatures{}
	off := fakeFeatures{off: map[string]bool{"user-alice|" + store.FeatureForwarding: true}}
	otherOff := fakeFeatures{off: map[string]bool{"user-alice|" + store.FeatureRules: true}}
	broken := fakeFeatures{errUsers: map[string]bool{"user-alice": true}}

	tests := []struct {
		name          string
		features      store.FeatureLookup
		prefs         map[string]*bongov1.UserPreferences
		requests      []*bongov1.ForwardRequest
		wantForwarded int
		wantLocal     int
	}{
		{"control: forward with keep copy", on, accountFwd(true), nil, 1, 1},
		{"control: no lookup wired", nil, accountFwd(true), nil, 1, 1},
		{"account forward dropped, copy kept", off, accountFwd(true), nil, 0, 1},
		{"account forward without copy still delivers locally", off, accountFwd(false), nil, 0, 1},
		{"control: forward without copy skips local", on, accountFwd(false), nil, 1, 0},
		{"rule forward dropped", off, nil, ruleFwd("user-alice", extTarget), 0, 1},
		{"another switch does not stop forwarding", otherOff, accountFwd(true), nil, 1, 1},
		{"lookup error fails closed, copy still delivered", broken, accountFwd(false), nil, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFwdFixture(t, aliceOK, tt.prefs)
			if tt.features != nil {
				f.proc.SetFeatures(tt.features)
			}
			f.deliver(t, &bongov1.QueueEnvelope{
				Id: "env-feat", Recipients: []string{"alice@example.com"}, ForwardRequests: tt.requests,
			}, rawFor("alice@example.com"))
			if got := len(f.queuer.messages); got != tt.wantForwarded {
				t.Errorf("forwarded = %d, want %d", got, tt.wantForwarded)
			}
			if got := len(f.applier.commands); got != tt.wantLocal {
				t.Errorf("local delivery commands = %d, want %d", got, tt.wantLocal)
			}
		})
	}
}

func TestVacationSwitchedOff(t *testing.T) {
	tests := []struct {
		name     string
		features store.FeatureLookup
		want     int
	}{
		{"control: reply sent", fakeFeatures{}, 1},
		{"vacation off: no reply", fakeFeatures{off: map[string]bool{"user-alice|" + store.FeatureVacation: true}}, 0},
		{"other switch off: reply sent", fakeFeatures{off: map[string]bool{"user-alice|" + store.FeatureForwarding: true}}, 1},
		{"lookup error fails closed", fakeFeatures{errUsers: map[string]bool{"user-alice": true}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newVacHarness(t)
			h.proc.SetFeatures(tt.features)
			env, raw := extMsg("bob@example.net")
			if err := h.send(env, raw); err != nil {
				t.Fatal(err)
			}
			if got := len(h.autoReplies()); got != tt.want {
				t.Fatalf("auto replies = %d, want %d", got, tt.want)
			}
			// The message itself is always delivered.
			if n := len(decodeDeliveries(t, &mockApplier{commands: h.applier.commands})); n != 1 {
				t.Errorf("deliveries = %d, want 1", n)
			}
		})
	}
}
