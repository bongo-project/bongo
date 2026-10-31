package carddav

import (
	"context"
	"net/http"
	"testing"

	"github.com/emersion/go-webdav/carddav"
)

type fakeStorageGate struct{ low bool }

func (g *fakeStorageGate) Low() bool { return g.low }

func TestPutAddressObjectStorageLowHTTP507(t *testing.T) {
	b, userID := newTestBackend(t)
	gate := &fakeStorageGate{low: true}
	b.SetStorageGate(gate)
	h := &carddav.Handler{Backend: b, Prefix: "/carddav"}

	if w := putVCF(h, userID, "s1", quotaVCFBody("Low")); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("PUT with storage low: status %d, want 507 (body %q)", w.Code, w.Body.String())
	}
	if _, err := b.contacts.Get(userID, "s1"); err == nil {
		t.Error("contact stored despite 507")
	}

	gate.low = false
	if w := putVCF(h, userID, "s1", quotaVCFBody("Low")); !okPut(w) {
		t.Fatalf("PUT with storage ok: status %d (body %q)", w.Code, w.Body.String())
	}

	// DELETE is never gated: it frees space.
	gate.low = true
	if err := b.DeleteAddressObject(WithUserID(context.Background(), userID), "/carddav/"+userID+"/addressbooks/default/s1.vcf"); err != nil {
		t.Errorf("DELETE with storage low: %v", err)
	}
}
