package handlers

import (
	"os"
	"testing"

	"github.com/bongo-go/bongo-go/internal/i18n"
)

// TestMain initializes the i18n catalogue so that templates rendered in tests
// produce real translated text (English) rather than raw message IDs. Without
// this, t(ctx, "...") returns the message ID and assertions on user-facing
// strings would fail.
func TestMain(m *testing.M) {
	i18n.Init("en")
	os.Exit(m.Run())
}
