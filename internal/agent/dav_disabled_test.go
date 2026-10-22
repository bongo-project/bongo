package agent

import (
	"net/http"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
)

// DAV authenticates every request with Basic auth, so refusing the login of an
// account whose Pebble profile is disabled (or missing) ends its DAV access
// even when the derived SQLite users.enabled flag still says 1.
func TestDAVBasicAuthHonoursProfileDisabled(t *testing.T) {
	tests := []struct {
		name     string
		disabled bool
		missing  bool
		want     int
	}{
		{"enabled profile (control)", false, false, http.StatusOK},
		{"disabled profile, sqlite enabled=1", true, false, http.StatusUnauthorized},
		{"missing profile, sqlite enabled=1", false, true, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm, err := store.NewMetadataManager(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mm.Close() })
			if _, err := mm.System.DB().Exec(
				"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('uid1', 'u@example.com', 'U', 1, 0)"); err != nil {
				t.Fatal(err)
			}
			kv := davKeyGetter{}
			if !tt.missing {
				hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
				data, err := proto.Marshal(&bongov1.UserProfile{Email: "u@example.com", PasswordHash: hash, Disabled: tt.disabled})
				if err != nil {
					t.Fatal(err)
				}
				kv["/user/uid1/profile"] = data
			}
			a := auth.NewLocalAuth(kv, mm)
			lim := authlimit.New(davMaxAuthFailures, time.Minute)
			if code := davAttempt(a, lim, "u@example.com", "pw"); code != tt.want {
				t.Fatalf("status = %d, want %d", code, tt.want)
			}
		})
	}
}
