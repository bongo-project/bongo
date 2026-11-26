package imap

import (
	"strings"
	"testing"
)

// TestCreateAndRenameApplyTheSharedNameRules pins every class of refusal the
// shared validator adds, for CREATE and RENAME, and that a folder which breaks
// the rules can still be renamed away or deleted.
func TestCreateAndRenameApplyTheSharedNameRules(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX", "Work", "unread")
	raw := dialRawIMAP(t, startNamespaceTestServer(t, ms, ps, false, "", ""))

	refused := []struct{ name, arg string }{
		{"virtual view", `"starred"`},
		{"over-long", `"` + strings.Repeat("a", 256) + `"`},
		{"double delimiter", `"a//b"`},
		{"leading delimiter", `"/a"`},
	}
	for _, tc := range refused {
		if got := raw.execStatus("CREATE %s", tc.arg); !strings.HasPrefix(got, "NO") {
			t.Errorf("CREATE (%s) = %q, want NO", tc.name, got)
		}
		if got := raw.execStatus("RENAME Work %s", tc.arg); !strings.HasPrefix(got, "NO") {
			t.Errorf("RENAME Work (%s) = %q, want NO", tc.name, got)
		}
	}
	if _, err := ms.GetMailbox(pollTestUser, "Work"); err != nil {
		t.Errorf("Work gone after refused renames: %v", err)
	}
	// A case difference is not the virtual name.
	if got := raw.execStatus(`CREATE "Starred"`); !strings.HasPrefix(got, "OK") {
		t.Errorf("CREATE Starred = %q, want OK", got)
	}
	// A trailing delimiter announces inferiors: the folder is "Docs".
	if got := raw.execStatus(`CREATE "Docs/"`); !strings.HasPrefix(got, "OK") {
		t.Errorf("CREATE Docs/ = %q, want OK", got)
	}
	if _, err := ms.GetMailbox(pollTestUser, "Docs"); err != nil {
		t.Errorf("CREATE Docs/ did not create Docs: %v", err)
	}
	// Only the new name is checked: a legacy virtual-named folder is renamed
	// away and deleted.
	if got := raw.execStatus(`RENAME unread Reading`); !strings.HasPrefix(got, "OK") {
		t.Errorf("RENAME legacy unread = %q, want OK", got)
	}
	if got := raw.execStatus(`DELETE Reading`); !strings.HasPrefix(got, "OK") {
		t.Errorf("DELETE Reading = %q, want OK", got)
	}
}
