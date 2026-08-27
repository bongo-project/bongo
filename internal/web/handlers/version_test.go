package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-project/bongo/internal/version"
)

func TestVersion(t *testing.T) {
	oldVersion, oldCommit, oldDate := version.Version, version.Commit, version.Date
	t.Cleanup(func() {
		version.Version = oldVersion
		version.Commit = oldCommit
		version.Date = oldDate
	})

	version.Version = "v1.2.3"
	version.Commit = "abcdef123456"
	version.Date = "2026-07-08T19:30:00Z"

	req := httptest.NewRequest(http.MethodGet, "/admin/version", nil)
	rec := httptest.NewRecorder()

	Version(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body version.BuildInfo
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "bongo-go" || body.Version != "v1.2.3" || body.Commit != "abcdef123456" || body.Date != "2026-07-08T19:30:00Z" {
		t.Fatalf("body = %#v", body)
	}
}
