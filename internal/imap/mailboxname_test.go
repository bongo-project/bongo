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

	const (
		invalid  = "NO [CANNOT] Mailbox name is not valid"
		reserved = "NO [CANNOT] Mailbox name is reserved"
	)
	refused := []struct{ name, arg, want string }{
		{"empty", `""`, invalid},
		{"virtual view", `"starred"`, reserved},
		{"over-long", `"` + strings.Repeat("a", 256) + `"`, invalid},
		{"double delimiter", `"a//b"`, invalid},
		{"leading delimiter", `"/a"`, invalid},
		{"other-users prefix", `"Other Users"`, reserved},
		{"below other-users prefix", `"Other Users/x"`, reserved},
		{"shared prefix", `"Shared/x"`, reserved},
	}
	for _, tc := range refused {
		if got := raw.execStatus("CREATE %s", tc.arg); !strings.HasPrefix(got, tc.want) {
			t.Errorf("CREATE (%s) = %q, want %q", tc.name, got, tc.want)
		}
		if got := raw.execStatus("RENAME Work %s", tc.arg); !strings.HasPrefix(got, tc.want) {
			t.Errorf("RENAME Work (%s) = %q, want %q", tc.name, got, tc.want)
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

// A control character cannot be typed into a quoted string, so the session is
// called directly. Personal and shared folders refuse it with the same code.
func TestMailboxNameRefusesControlCharacters(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswipkxtea")
	e.grant("Work/Sub", "lrswipkxtea")
	if err := e.ms.CreateMailbox(pollTestUser, "Mine"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a\x01b", "a\tb", "a\x7fb"} {
		wantErr(t, e.session().Create(name, nil), errInvalidMailboxName)
		wantErr(t, e.session().Rename("Mine", name, nil), errInvalidMailboxName)
		// Beneath a shared folder only the owner-side check can refuse it.
		wantErr(t, e.session().Create(shWork+"/"+name, nil), errInvalidMailboxName)
		wantErr(t, e.session().Rename(shSub, shWork+"/"+name, nil), errInvalidMailboxName)
	}
}

// createShared and renameShared validate the name in the owner's keyspace:
// the personal validator never sees a name under the other-users prefix.
func TestSharedCreateAndRenameApplyOwnerNameRules(t *testing.T) {
	long := strings.Repeat("a", 256)
	tests := []struct{ name, leaf string }{
		{"over-long", long},
		{"control character", "a\x01b"},
		{"invalid UTF-8", "a\xffb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", "lrswipkxtea")
			e.grant("Work/Sub", "lrswipkxtea")
			wantErr(t, e.session().Create(shWork+"/"+tt.leaf, nil), errInvalidMailboxName)
			wantErr(t, e.session().Rename(shSub, shWork+"/"+tt.leaf, nil), errInvalidMailboxName)
			if _, err := e.ms.GetMailbox(shOwner, "Work/"+tt.leaf); err == nil {
				t.Fatal("a refused name exists in the owner's keyspace")
			}
			if _, err := e.ms.GetMailbox(shOwner, "Work/Sub"); err != nil {
				t.Fatalf("source folder gone after a refused rename: %v", err)
			}
		})
	}
}

// A trailing delimiter only announces inferiors (RFC 3501 6.3.3), for a folder
// under the other-users prefix as for a personal one.
func TestSharedCreateTrimsTrailingDelimiter(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lk")
	wantErr(t, e.session().Create(shWork+"/New/", nil), nil)
	if _, err := e.ms.GetMailbox(shOwner, "Work/New"); err != nil {
		t.Fatalf("Work/New not created: %v", err)
	}
	if _, err := e.ms.GetMailbox(shOwner, "Work/New/"); err == nil {
		t.Fatal("a folder with a trailing delimiter was created")
	}
}
