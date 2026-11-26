package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The unsubscribe panel fills the host (or list name) into these strings with
// Sprintf, so a translation must keep exactly one %s, and the fixed texts must
// keep none.
func TestUnsubscribeKeysKeepPlaceholders(t *testing.T) {
	withHost := []string{"mail.unsubscribeOneClick", "mail.unsubscribeLinkConfirm", "mail.unsubscribeListTitle"}
	fixed := []string{
		"mail.unsubscribe", "mail.unsubscribeContinue", "mail.unsubscribeMailto", "mail.unsubscribeSuccess",
		"mail.unsubscribeFailure", "mail.unsubscribeRateLimited", "mail.unsubscribeSpamHint",
		"mail.unsubscribeListsAction", "mail.unsubscribeNotAvailable", "mail.unsubscribeNoProof", "mail.unsubscribeListNote",
	}
	for _, locale := range []string{"en", "de", "es", "fr"} {
		raw, err := os.ReadFile(filepath.Join("messages", locale+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(raw, &msgs); err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		for _, key := range withHost {
			if got := strings.Count(msgs[key], "%s"); got != 1 {
				t.Errorf("%s %s = %q: want exactly one %%s, found %d", locale, key, msgs[key], got)
			}
		}
		for _, key := range fixed {
			if msgs[key] == "" || strings.Contains(msgs[key], "%") {
				t.Errorf("%s %s = %q: want text without a placeholder", locale, key, msgs[key])
			}
		}
	}
}
