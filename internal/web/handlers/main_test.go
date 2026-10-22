package handlers

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/i18n"
)

// TestMain initializes the i18n catalogue so that templates rendered in tests
// produce real translated text (English) rather than raw message IDs. Without
// this, t(ctx, "...") returns the message ID and assertions on user-facing
// strings would fail. It also drops the bcrypt cost: the package seeds hundreds
// of accounts, and at the default cost it overruns the 10m test timeout on CI.
func TestMain(m *testing.M) {
	i18n.Init("en")
	auth.PasswordHashCost = bcrypt.MinCost
	os.Exit(m.Run())
}
