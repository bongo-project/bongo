package handlers

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// failAddressPutApplier fails PUTs under /system/address/ and passes the rest
// through, to exercise the alias-record write failure path.
type failAddressPutApplier struct{ *directApplierTags }

func (f *failAddressPutApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && strings.HasPrefix(string(cmd.Key), "/system/address/") {
		return errors.New("injected raft failure")
	}
	return f.directApplierTags.ApplyCommand(cmd, d)
}

func TestCreateAlias_RecordWriteFailureRollsBack(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	targetID, err := auth.CreateUser(applier, md, "boss@target.example", "Boss", "pass12345", true)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failAddressPutApplier{applier}
	h.applier = failing
	h.SetAddressStore(store.NewAddressStore(h.store, failing, md.System))

	w := createAliasRequest(t, h, "sales@alias.example", "boss@target.example", targetID)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if _, err := h.store.Get([]byte("/system/alias/sales@alias.example")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("alias key survived failed record write: err = %v", err)
	}
	if _, ok, _ := h.addresses.Get("sales@alias.example"); ok {
		t.Error("address record exists after failed write")
	}
	var n int
	if err := md.System.DB().QueryRow(`SELECT count(*) FROM aliases WHERE address = 'sales@alias.example'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("aliases rows = %d, err = %v", n, err)
	}
}

func TestDeleteAlias_KeepsNonAliasRecords(t *testing.T) {
	tests := []struct {
		name string
		rec  *bongov1.AddressRecord
	}{
		{"primary", &bongov1.AddressRecord{UserId: "u1", Domain: "example.com", Kind: "primary", Verified: true, CanSend: true}},
		{"verified identity", &bongov1.AddressRecord{UserId: "u1", Domain: "example.com", Kind: "identity", Verified: true}},
		{"unverified identity", &bongov1.AddressRecord{UserId: "u1", Domain: "example.com", Kind: "identity"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, _ := newAdminDeleteTestHandler(t)
			if err := h.addresses.Put("who@example.com", tt.rec); err != nil {
				t.Fatal(err)
			}
			// No /system/alias/ key, so this is not an alias: 404.
			if w := deleteAliasRequest(t, h, "who@example.com", "admin"); w.Code != http.StatusNotFound {
				t.Fatalf("DeleteAlias = %d, want 404: %s", w.Code, w.Body.String())
			}
			got, ok, err := h.addresses.Get("who@example.com")
			if err != nil || !ok || got.Kind != tt.rec.Kind {
				t.Errorf("record = %+v ok=%v err=%v, want it kept", got, ok, err)
			}
		})
	}
}

func TestAddressAllocationError_FailsClosedOnMissingStores(t *testing.T) {
	h, applier, md := newAdminDeleteTestHandler(t)
	tests := []struct {
		name string
		ps   *store.PebbleStore
		md   *store.MetadataManager
		ap   store.RaftApplier
	}{
		{"nil pebble", nil, md, applier},
		{"nil metadata", h.store, nil, applier},
		{"nil applier", h.store, md, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := addressAllocationError(tt.ps, tt.md, tt.ap, "free@example.com", "")
			if err == nil {
				t.Fatal("want error for missing stores, got nil")
			}
			if errors.Is(err, auth.ErrAccountExists) {
				t.Errorf("missing stores must be a read failure, not 'taken': %v", err)
			}
		})
	}
	if err := addressAllocationError(h.store, md, applier, "free@example.com", ""); err != nil {
		t.Errorf("free address: %v", err)
	}
}

func TestDomainHostedLocally(t *testing.T) {
	h, applier, _ := newAdminDeleteTestHandler(t)
	if err := applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/system/domain/hosted.example"), Value: []byte("x")}, time.Second); err != nil {
		t.Fatal(err)
	}
	if got, err := domainHostedLocally(h.store, "Hosted.Example"); err != nil || !got {
		t.Errorf("hosted = %v err %v, want true", got, err)
	}
	if got, err := domainHostedLocally(h.store, "other.example"); err != nil || got {
		t.Errorf("other = %v err %v, want false", got, err)
	}
	var nilStore *store.PebbleStore
	if _, err := domainHostedLocally(nilStore, "hosted.example"); err == nil {
		t.Error("nil store: want error")
	}

	// A failed read must be an error, not "not hosted".
	if got, err := domainHostedLocally(errGetter{}, "hosted.example"); err == nil || got {
		t.Errorf("failing store: got %v err %v, want false + error", got, err)
	}
}

type errGetter struct{}

func (errGetter) Get([]byte) ([]byte, error) { return nil, errors.New("disk read failure") }
