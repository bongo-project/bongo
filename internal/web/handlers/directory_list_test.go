package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type directoryResponse struct {
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated"`
	Items     []json.RawMessage `json:"items"`
}

func seedDirectory(t *testing.T) *DirectoryListHandler {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })

	prof, err := proto.Marshal(&bongov1.UserProfile{
		Email:        "amy@example.com",
		DisplayName:  "Amy",
		PasswordHash: []byte("SECRET-PASSWORD-HASH"),
		TotpSecret:   []byte("SECRET-TOTP-SEED"),
		RecoveryCodes: []string{
			"SECRET-RECOVERY-CODE",
		},
		TotpEnabled:       true,
		SessionGeneration: 987654321,
	})
	if err != nil {
		t.Fatal(err)
	}
	dkim, err := proto.Marshal(&bongov1.DomainRecord{DkimEnabled: true, DkimSelector: "mail", DkimPrivateKey: "SECRET-DKIM-PRIVATE-KEY"})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string][]byte{
		"/user/u1/profile":                    prof,
		"/system/domain/example.com":          []byte("enabled"),
		"/system/domain/example.com/dkim":     dkim,
		"/system/alias/sales@example.com":     []byte("amy@example.com"),
		"/system/domain_admin/example.com/u1": []byte("1"),
	} {
		if err := ps.Set([]byte(k), v); err != nil {
			t.Fatal(err)
		}
	}
	return NewDirectoryListHandler(ps, nil)
}

func getDirectory(t *testing.T, fn http.HandlerFunc, target string) (*httptest.ResponseRecorder, directoryResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	fn(rec, httptest.NewRequest(http.MethodGet, target, nil))
	var out directoryResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %s: %v", rec.Body.String(), err)
		}
	}
	return rec, out
}

func TestDirectoryListHandlerOutputs(t *testing.T) {
	h := seedDirectory(t)

	rec, users := getDirectory(t, h.Users, "/internal/users")
	if rec.Code != http.StatusOK || users.Count != 1 || len(users.Items) != 1 {
		t.Fatalf("users: %d %s", rec.Code, rec.Body.String())
	}
	var u store.UserSummary
	if err := json.Unmarshal(users.Items[0], &u); err != nil {
		t.Fatal(err)
	}
	if u.Email != "amy@example.com" || len(u.AdminDomains) != 1 || u.AdminDomains[0] != "example.com" {
		t.Errorf("user = %+v", u)
	}

	_, doms := getDirectory(t, h.Domains, "/internal/domains")
	if doms.Count != 1 || !strings.Contains(string(doms.Items[0]), `"dkim":true`) {
		t.Errorf("domains = %+v", doms)
	}
	_, als := getDirectory(t, h.AddressAliases, "/internal/aliases?domain=EXAMPLE.com")
	if als.Count != 1 {
		t.Errorf("aliases = %+v", als)
	}
	_, dotted := getDirectory(t, h.AddressAliases, "/internal/aliases?domain=example.com.")
	if dotted.Count != 1 {
		t.Errorf("trailing-dot filter listed %d aliases, want 1", dotted.Count)
	}
	_, none := getDirectory(t, h.AddressAliases, "/internal/aliases?domain=other.test")
	if none.Count != 0 || none.Items == nil || none.Truncated {
		t.Errorf("empty listing must be an empty array, got %+v", none)
	}
}

// TestDirectoryListHandlerLeaksNoSecrets marshals every response and fails if
// any seeded credential, TOTP, recovery, session or DKIM key value appears.
func TestDirectoryListHandlerLeaksNoSecrets(t *testing.T) {
	h := seedDirectory(t)
	forbidden := []string{
		"SECRET-PASSWORD-HASH", "SECRET-TOTP-SEED", "SECRET-RECOVERY-CODE", "SECRET-DKIM-PRIVATE-KEY",
		"987654321", "password_hash", "totp", "recovery", "session_generation", "private_key",
		// Base64 forms, which is how encoding/json renders a []byte field.
		"U0VDUkVULVBBU1NXT1JELUhBU0g", "U0VDUkVULVRPVFAtU0VFRA",
	}
	for name, fn := range map[string]http.HandlerFunc{"users": h.Users, "domains": h.Domains, "aliases": h.AddressAliases} {
		rec, _ := getDirectory(t, fn, "/internal/"+name)
		if rec.Code != http.StatusOK || rec.Body.Len() < 20 {
			t.Fatalf("%s: status %d body %q", name, rec.Code, rec.Body.String())
		}
		for _, bad := range forbidden {
			if strings.Contains(rec.Body.String(), bad) {
				t.Errorf("%s response contains %q: %s", name, bad, rec.Body.String())
			}
		}
	}
}

func TestDirectoryListHandlerRejectsBadDomain(t *testing.T) {
	h := seedDirectory(t)
	for _, q := range []string{"a%2Fb", "a%40b", "a%0Ab", strings.Repeat("a", 254), ".", "example.com.."} {
		for name, fn := range map[string]http.HandlerFunc{"users": h.Users, "aliases": h.AddressAliases} {
			if rec, _ := getDirectory(t, fn, "/internal/"+name+"?domain="+q); rec.Code != http.StatusBadRequest {
				t.Errorf("%s domain=%q: status %d, want 400", name, q, rec.Code)
			}
		}
	}
}

func TestDirectoryListHandlerFailsClosed(t *testing.T) {
	h := seedDirectory(t)
	if err := h.pebble.Set([]byte("/user/bad/profile"), []byte("\xff\xff\xff")); err != nil {
		t.Fatal(err)
	}
	if rec, _ := getDirectory(t, h.Users, "/internal/users"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}
