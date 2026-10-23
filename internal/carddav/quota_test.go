package carddav

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-webdav/carddav"
	"google.golang.org/protobuf/proto"
)

// fakeQuota models a domain with a fixed amount of headroom. over forces a
// refusal regardless of size; err forces a checker failure.
type fakeQuota struct {
	over     bool
	err      error
	headroom int64
	useRoom  bool
	lastAdd  int64
}

func (f *fakeQuota) RecipientOverQuota(string) (bool, error) {
	if f.useRoom {
		return f.headroom <= 0, f.err
	}
	return f.over, f.err
}

func (f *fakeQuota) RecipientWouldExceedQuota(_ string, add int64) (bool, error) {
	f.lastAdd = add
	if f.err != nil {
		return false, f.err
	}
	if f.useRoom {
		return f.headroom <= 0 || add > f.headroom, nil
	}
	return f.over, nil
}

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

func quotaVCFBody(fn string) string {
	return "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:" + fn + "\r\nEMAIL:q@example.test\r\nEND:VCARD\r\n"
}

func putVCF(h http.Handler, userID, guid, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/carddav/"+userID+"/addressbooks/default/"+guid+".vcf", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/vcard")
	req = req.WithContext(WithUserID(req.Context(), userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func okPut(w *httptest.ResponseRecorder) bool {
	return w.Code == http.StatusCreated || w.Code == http.StatusNoContent
}

// storedSize is the byte length of the record in Pebble, the quantity the
// domain usage recompute counts.
func storedSize(t *testing.T, b *Backend, userID, guid string) int64 {
	t.Helper()
	c, err := b.contacts.Get(userID, guid)
	if err != nil {
		t.Fatalf("get %s: %v", guid, err)
	}
	return int64(proto.Size(c))
}

// A create must fit by the size of the stored record, not merely start under
// quota. The headroom is derived from a real stored record, so it is not a
// round number.
func TestPutAddressObjectQuotaBySize(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &carddav.Handler{Backend: b, Prefix: "/carddav"}
	body := quotaVCFBody("Sized Boundary Contact")

	if w := putVCF(h, userID, "s1", body); !okPut(w) {
		t.Fatalf("measure PUT: %d %q", w.Code, w.Body.String())
	}
	size := storedSize(t, b, userID, "s1")
	if err := b.DeleteAddressObject(WithUserID(context.Background(), userID), "/carddav/"+userID+"/addressbooks/default/s1.vcf"); err != nil {
		t.Fatal(err)
	}

	fq := &fakeQuota{useRoom: true, headroom: size - 1}
	b.SetQuotaChecker(fq)
	if w := putVCF(h, userID, "s1", body); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("one byte over headroom: status %d, want 507", w.Code)
	}
	if _, err := b.contacts.Get(userID, "s1"); err == nil {
		t.Error("contact stored despite 507")
	}

	fq.headroom = size
	if w := putVCF(h, userID, "s1", body); !okPut(w) {
		t.Fatalf("exact fit: status %d, want 201 (body %q)", w.Code, w.Body.String())
	}
	if got := storedSize(t, b, userID, "s1"); got != fq.headroom {
		t.Errorf("stored size %d, headroom %d: estimate and stored record disagree", got, fq.headroom)
	}
	if fq.lastAdd != size {
		t.Errorf("checker saw %d bytes, want %d", fq.lastAdd, size)
	}
}

// Updates are judged by growth. A non-growing edit is allowed at quota (the web
// rule); growth must fit the headroom exactly.
func TestPutAddressObjectQuotaUpdateGrowth(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &carddav.Handler{Backend: b, Prefix: "/carddav"}
	base0 := quotaVCFBody("Medium Name")
	if w := putVCF(h, userID, "u1", base0); !okPut(w) {
		t.Fatalf("seed PUT: %d", w.Code)
	}
	base := storedSize(t, b, userID, "u1")

	fq := &fakeQuota{useRoom: true, headroom: 0}
	b.SetQuotaChecker(fq)

	grown := quotaVCFBody("Medium Name With Thirteen More")
	if w := putVCF(h, userID, "u1", grown); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("growth at quota: status %d, want 507", w.Code)
	}
	if got := storedSize(t, b, userID, "u1"); got != base {
		t.Errorf("record changed by refused update: %d -> %d", base, got)
	}

	if w := putVCF(h, userID, "u1", base0); !okPut(w) {
		t.Errorf("identical update at quota: status %d, want accepted", w.Code)
	}
	if w := putVCF(h, userID, "u1", quotaVCFBody("Mid")); !okPut(w) {
		t.Errorf("shrinking update at quota: status %d, want accepted", w.Code)
	}
	fq.headroom = 1 << 20
	if w := putVCF(h, userID, "u1", base0); !okPut(w) {
		t.Fatalf("restore: %d", w.Code)
	}

	fq.headroom = 1 << 20
	if w := putVCF(h, userID, "u1", grown); !okPut(w) {
		t.Fatalf("grow with room: %d", w.Code)
	}
	growth := storedSize(t, b, userID, "u1") - base
	if growth <= 0 {
		t.Fatalf("growth %d, want positive", growth)
	}
	if w := putVCF(h, userID, "u1", base0); !okPut(w) {
		t.Fatalf("reset: %d", w.Code)
	}

	fq.headroom = growth - 1
	if w := putVCF(h, userID, "u1", grown); w.Code != http.StatusInsufficientStorage {
		t.Errorf("growth+1 over headroom: status %d, want 507", w.Code)
	}
	fq.headroom = growth
	if w := putVCF(h, userID, "u1", grown); !okPut(w) {
		t.Errorf("growth equal to headroom: status %d, want accepted", w.Code)
	}
}

// A stale If-Match is reported as 412 even when the user is over quota.
func TestPutAddressObjectPreconditionBeatsQuota(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &carddav.Handler{Backend: b, Prefix: "/carddav"}
	if w := putVCF(h, userID, "p1", quotaVCFBody("x")); !okPut(w) {
		t.Fatalf("seed: %d", w.Code)
	}
	b.SetQuotaChecker(&fakeQuota{useRoom: true, headroom: 0})
	req := httptest.NewRequest(http.MethodPut, "/carddav/"+userID+"/addressbooks/default/p1.vcf", strings.NewReader(quotaVCFBody("a much longer name than before")))
	req.Header.Set("Content-Type", "text/vcard")
	req.Header.Set("If-Match", `"stale"`)
	req = req.WithContext(WithUserID(req.Context(), userID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPreconditionFailed {
		t.Errorf("stale If-Match over quota: status %d, want 412", w.Code)
	}
}

// A one-byte growth at quota is refused. Guards the "addBytes <= 0" allow in
// checkQuota against being widened to a small tolerance, which would let a
// client grow a record without limit in small steps. X-NOTE is not mapped to a
// Contact field, so only the raw vCard grows.
func TestPutAddressObjectQuotaOneByteGrowth(t *testing.T) {
	b, userID := newTestBackend(t)
	h := &carddav.Handler{Backend: b, Prefix: "/carddav"}
	withNote := func(note string) string {
		return strings.Replace(quotaVCFBody("Note"), "END:VCARD", "X-NOTE:"+note+"\r\nEND:VCARD", 1)
	}
	if w := putVCF(h, userID, "n1", withNote("abc")); !okPut(w) {
		t.Fatalf("seed PUT: %d", w.Code)
	}
	base := storedSize(t, b, userID, "n1")

	fq := &fakeQuota{useRoom: true, headroom: 0}
	b.SetQuotaChecker(fq)
	if w := putVCF(h, userID, "n1", withNote("abcd")); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("one-byte growth at quota: status %d, want 507", w.Code)
	}
	if fq.lastAdd != 1 {
		t.Fatalf("measured growth %d, want exactly 1", fq.lastAdd)
	}
	if got := storedSize(t, b, userID, "n1"); got != base {
		t.Errorf("record changed by refused update: %d -> %d", base, got)
	}
}
