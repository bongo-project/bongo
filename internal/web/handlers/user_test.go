package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// nopApplier records nothing and always succeeds.
type nopApplier struct{}

func (nopApplier) ApplyCommand(_ *bongov1.RaftCommand, _ time.Duration) error { return nil }

// recordingEnsurer records the user ID it was asked to provision.
type recordingEnsurer struct {
	called   bool
	userID   string
	returned error
}

func (r *recordingEnsurer) EnsureDefaultMailboxes(userID string) error {
	r.called = true
	r.userID = userID
	return r.returned
}

func TestUserCreateProvisionsDefaultMailboxes(t *testing.T) {
	ens := &recordingEnsurer{}
	h := NewUserHandler(nopApplier{}, nil)
	h.SetMailboxes(ens)

	body, _ := json.Marshal(map[string]any{"email": "new@example.com", "password": "secret123"})
	req := httptest.NewRequest(http.MethodPost, "/internal/user/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var resp createUserResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !ens.called {
		t.Fatal("EnsureDefaultMailboxes was not called for the new user")
	}
	if ens.userID != resp.UserID {
		t.Errorf("provisioned userID = %q, want %q (the created user)", ens.userID, resp.UserID)
	}
}

func TestUserCreateFailsWhenMailboxProvisioningFails(t *testing.T) {
	ens := &recordingEnsurer{returned: errProvision}
	h := NewUserHandler(nopApplier{}, nil)
	h.SetMailboxes(ens)

	body, _ := json.Marshal(map[string]any{"email": "new@example.com", "password": "secret123"})
	req := httptest.NewRequest(http.MethodPost, "/internal/user/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// errProvision is a sentinel error for the failing-provisioning test.
var errProvision = &provisionError{}

type provisionError struct{}

func (*provisionError) Error() string { return "provisioning failed" }
