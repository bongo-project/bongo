package agent

import (
	"net/http"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// With dav switched off CalDAV and CardDAV Basic answer 401 exactly as for a
// wrong password, and the failure counts toward the lockout. The next request
// of an already authenticated client is refused too, because DAV
// authenticates every request.
func TestDAVBasicAuthFeatureDisabled(t *testing.T) {
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	put := func(features ...string) davKeyGetter {
		data, err := proto.Marshal(&bongov1.UserProfile{Email: "alice@example.com", PasswordHash: hash, DisabledFeatures: features})
		if err != nil {
			t.Fatal(err)
		}
		return davKeyGetter{"/user/uid1/profile": data}
	}
	if _, err := mm.System.DB().Exec(
		"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES ('uid1', 'alice@example.com', 'A', 1, 0)"); err != nil {
		t.Fatal(err)
	}

	lim := authlimit.New(davMaxAuthFailures, time.Minute)
	a := auth.NewLocalAuth(put(), mm)
	if code := davAttempt(a, lim, "alice@example.com", "pw"); code != http.StatusOK {
		t.Fatalf("positive control: %d", code)
	}

	a = auth.NewLocalAuth(put(store.FeatureIMAP, store.FeaturePOP3), mm)
	if code := davAttempt(a, lim, "alice@example.com", "pw"); code != http.StatusOK {
		t.Fatalf("other protocols off: %d", code)
	}

	a = auth.NewLocalAuth(put(store.FeatureDAV), mm)
	wrong := davAttempt(a, lim, "alice@example.com", "nope")
	for range 2 {
		if got := davAttempt(a, lim, "alice@example.com", "pw"); got != wrong || got != http.StatusUnauthorized {
			t.Fatalf("dav disabled = %d, wrong password = %d, want both 401", got, wrong)
		}
	}
}
