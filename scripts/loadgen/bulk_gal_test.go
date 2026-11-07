package main

import (
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// TestBulkLoadFillsGAL checks that every loaded user reaches the global
// address list. The batched search path passes profile writes straight
// through; without this test a loader that dropped them would still pass the
// mail-index comparisons.
func TestBulkLoadFillsGAL(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	dir, _ := loadBulk(t, spec)

	sm, err := store.NewSearchManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sm.Close() }()

	caller := spec.PlanUser(0).Email
	for i := range spec.Users {
		email := spec.PlanUser(i).Email
		// The GAL is searched by prefix over analysed tokens, as compose
		// autocomplete types it, so query the local part, not the address.
		local, _, _ := strings.Cut(email, "@")
		hits, err := sm.SearchGAL(local, 5, caller)
		if err != nil {
			t.Fatalf("SearchGAL(%q): %v", email, err)
		}
		found := false
		for _, h := range hits {
			if h.Email == email {
				found = true
			}
		}
		if !found {
			t.Errorf("user %d is not in the global address list after a bulk load", i)
		}
	}
}
