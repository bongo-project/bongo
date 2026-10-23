package agent

import (
	"testing"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

// TestBuildPipelineStages_SenderAuthorizerWiring proves the deliver stage gets
// the send-as policy when the stores exist, and none (forwards stay unsigned)
// when they do not.
func TestBuildPipelineStages_SenderAuthorizerWiring(t *testing.T) {
	t.Run("stores present", func(t *testing.T) {
		ps, err := store.NewPebbleStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ps.Close() })
		a := &Agent{Config: config.Defaults(), logger: testLogger(), Store: ps, AddressStore: store.NewAddressStore(ps, nil, nil)}
		_, deliver := a.buildPipelineStages(nil)
		if _, ok := deliver.SenderAuthorizer().(*store.SendAsPolicy); !ok {
			t.Errorf("sender authorizer = %T, want *store.SendAsPolicy", deliver.SenderAuthorizer())
		}
	})
	t.Run("no stores", func(t *testing.T) {
		a := &Agent{Config: config.Defaults(), logger: testLogger()}
		_, deliver := a.buildPipelineStages(nil)
		if deliver.SenderAuthorizer() != nil {
			t.Error("sender authorizer set without stores")
		}
	})
}
