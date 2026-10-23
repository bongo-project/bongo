package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type repairApplier struct{ ps *store.PebbleStore }

func (a *repairApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.ps.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.ps.Delete(cmd.Key)
	}
	return nil
}

func newRepairHandler(t *testing.T) (*AddressRepairHandler, *store.PebbleStore) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	as := store.NewAddressStore(ps, &repairApplier{ps: ps}, nil)

	rec, _ := proto.Marshal(&bongov1.AddressRecord{UserId: "ghost", Domain: "example.com", Kind: "primary"})
	for k, v := range map[string][]byte{
		"/system/address/ghost@example.com": rec,
		"/system/alias/dead@example.com":    []byte("gone@example.com"),
		"/system/catchall/dead.example":     []byte("ghost-cc"),
	} {
		if err := ps.Set([]byte(k), v); err != nil {
			t.Fatal(err)
		}
	}
	return NewAddressRepairHandler(ps, as, nil), ps
}

func TestAddressRepairHandler_ListIncludesReportOnlyKinds(t *testing.T) {
	h, _ := newRepairHandler(t)
	rr := httptest.NewRecorder()
	h.List(rr, httptest.NewRequest(http.MethodGet, "/internal/addresses/orphaned", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out struct {
		Count   int
		Orphans []struct{ Address, Kind string }
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, o := range out.Orphans {
		kinds[o.Kind] = o.Address
	}
	if out.Count != 3 || kinds["legacy-alias"] != "dead@example.com" || kinds["catchall"] != "dead.example" || kinds["primary"] != "ghost@example.com" {
		t.Fatalf("list = %s", rr.Body.String())
	}
}

func TestAddressRepairHandler_RepairLeavesReportOnlyKeys(t *testing.T) {
	h, ps := newRepairHandler(t)
	rr := httptest.NewRecorder()
	h.Repair(rr, httptest.NewRequest(http.MethodPost, "/internal/addresses/repair", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var out struct {
		Deleted []string
		Count   int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 1 || len(out.Deleted) != 1 || out.Deleted[0] != "ghost@example.com" {
		t.Fatalf("repair = %s", rr.Body.String())
	}
	if _, err := ps.Get([]byte("/system/address/ghost@example.com")); err == nil {
		t.Error("address-index orphan not removed")
	}
	for _, k := range []string{"/system/alias/dead@example.com", "/system/catchall/dead.example"} {
		if _, err := ps.Get([]byte(k)); err != nil {
			t.Errorf("%s removed by repair: %v", k, err)
		}
	}
}
