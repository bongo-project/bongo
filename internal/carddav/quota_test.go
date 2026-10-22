package carddav

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-webdav/carddav"
)

type fakeQuota struct {
	over bool
	err  error
}

func (f *fakeQuota) RecipientOverQuota(string) (bool, error) { return f.over, f.err }

func TestPutAddressObjectQuota(t *testing.T) {
	b, userID := newTestBackend(t)
	fq := &fakeQuota{over: true}
	b.SetQuotaChecker(fq)
	ctx := WithUserID(context.Background(), userID)
	path := "/carddav/" + userID + "/addressbooks/default/q1.vcf"

	_, err := b.PutAddressObject(ctx, path, testCard("Quota", "q@example.test"), nil)
	if err == nil {
		t.Fatal("PUT over quota: want an error, got nil")
	}
	if !errors.Is(err, ErrStorageQuotaExceeded) {
		t.Errorf("PUT over quota: want ErrStorageQuotaExceeded, got %v", err)
	}
	if _, gerr := b.contacts.Get(userID, "q1"); gerr == nil {
		t.Error("contact stored despite refused PUT")
	}

	fq.over = false
	if _, err := b.PutAddressObject(ctx, path, testCard("Quota", "q@example.test"), nil); err != nil {
		t.Fatalf("PUT under quota: %v", err)
	}
	if _, gerr := b.contacts.Get(userID, "q1"); gerr != nil {
		t.Errorf("contact not stored after accepted PUT: %v", gerr)
	}

	fq.over, fq.err = true, errors.New("pebble unavailable")
	if _, err := b.PutAddressObject(ctx, "/carddav/"+userID+"/addressbooks/default/q2.vcf", testCard("Two", "t@example.test"), nil); err != nil {
		t.Errorf("PUT with failing checker: want success (fail open), got %v", err)
	}

	fq.over, fq.err = true, nil
	if err := b.DeleteAddressObject(ctx, path); err != nil {
		t.Errorf("DELETE over quota: %v", err)
	}
}

func TestPutAddressObjectQuotaHTTP507(t *testing.T) {
	b, userID := newTestBackend(t)
	fq := &fakeQuota{over: true}
	b.SetQuotaChecker(fq)
	h := carddav.Handler{Backend: b, Prefix: "/carddav"}

	body := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Quota\r\nEMAIL:q@example.test\r\nEND:VCARD\r\n"
	put := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/carddav/"+userID+"/addressbooks/default/h1.vcf", strings.NewReader(body))
		req.Header.Set("Content-Type", "text/vcard")
		req = req.WithContext(WithUserID(req.Context(), userID))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := put(); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("PUT over quota: status %d, want 507 (body %q)", w.Code, w.Body.String())
	}
	if _, err := b.contacts.Get(userID, "h1"); err == nil {
		t.Error("contact stored despite 507")
	}
	fq.over = false
	if w := put(); w.Code != http.StatusCreated && w.Code != http.StatusNoContent {
		t.Fatalf("PUT under quota: status %d, want 201/204 (body %q)", w.Code, w.Body.String())
	}
}
