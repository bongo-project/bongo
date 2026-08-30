package store

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newCaseReportPebble(t *testing.T) *PebbleStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps
}

func TestReportMixedCaseAddresses(t *testing.T) {
	ps := newCaseReportPebble(t)
	applier := &directApplier{store: ps}

	put := func(key string, val []byte) {
		t.Helper()
		if err := applier.ApplyCommand(&bongov1.RaftCommand{
			Op: bongov1.Op_OP_PUT, Key: []byte(key), Value: val,
		}, time.Second); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	profile := func(email string) []byte {
		t.Helper()
		b, err := proto.Marshal(&bongov1.UserProfile{Email: email})
		if err != nil {
			t.Fatalf("marshal profile: %v", err)
		}
		return b
	}

	// Clean entries that must NOT be reported.
	put("/system/alias/fine@example.com", []byte("owner@example.com"))
	put("/user/u-clean/profile", profile("clean@example.com"))
	// Offenders.
	put("/system/alias/Mixed@Example.com", []byte("owner@example.com"))
	put("/user/u-dirty/profile", profile("Dirty@Example.com"))

	found, err := ReportMixedCaseAddresses(ps, nil)
	if err != nil {
		t.Fatalf("ReportMixedCaseAddresses: %v", err)
	}

	byStored := make(map[string]MixedCaseAddress, len(found))
	for _, f := range found {
		byStored[f.Stored] = f
	}

	if len(found) != 2 {
		t.Fatalf("reported %d addresses (%v), want exactly 2 - lower-case entries must not be reported", len(found), byStored)
	}
	alias, ok := byStored["Mixed@Example.com"]
	if !ok {
		t.Fatal("mixed-case alias not reported")
	}
	if alias.Kind != "alias" || alias.Normalised != "mixed@example.com" || alias.Target != "owner@example.com" {
		t.Errorf("alias entry = %+v, want kind=alias normalised=mixed@example.com target=owner@example.com", alias)
	}
	acct, ok := byStored["Dirty@Example.com"]
	if !ok {
		t.Fatal("mixed-case account not reported")
	}
	if acct.Kind != "account" || acct.Normalised != "dirty@example.com" {
		t.Errorf("account entry = %+v, want kind=account normalised=dirty@example.com", acct)
	}
}

// A store with nothing wrong must report nothing, so the boot log stays quiet
// on healthy deployments.
func TestReportMixedCaseAddressesCleanStore(t *testing.T) {
	ps := newCaseReportPebble(t)
	applier := &directApplier{store: ps}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/system/alias/ok@example.com"), Value: []byte("owner@example.com"),
	}, time.Second); err != nil {
		t.Fatalf("put: %v", err)
	}
	found, err := ReportMixedCaseAddresses(ps, nil)
	if err != nil {
		t.Fatalf("ReportMixedCaseAddresses: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("clean store reported %v, want none", found)
	}
}

func TestReportMixedCaseAddressesNilStore(t *testing.T) {
	found, err := ReportMixedCaseAddresses(nil, nil)
	if err != nil || found != nil {
		t.Errorf("nil store = (%v, %v), want (nil, nil)", found, err)
	}
}
