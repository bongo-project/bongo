package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

func TestImportMboxRefusesReservedFolderNames(t *testing.T) {
	const user, email = "u-reserved", "reserved@example.test"
	const mbox = "From a@example.test Mon Jan  1 00:00:00 2024\nSubject: hi\n\nbody\n"

	tests := []struct {
		name      string
		setter    bool
		fields    map[string]string
		locale    string
		wantCode  int
		wantNew   string // mailbox that must exist afterwards, "" for none
		wantAbsnt string // mailbox that must not exist afterwards
	}{
		{"new_mailbox Shared/x en", false, map[string]string{"new_mailbox": "Shared/x"}, "en", http.StatusBadRequest, "", "Shared/x"},
		{"new_mailbox other users de", false, map[string]string{"new_mailbox": "other users"}, "de", http.StatusBadRequest, "", "other users"},
		{"posted missing mailbox fr", false, map[string]string{"mailbox": "SHARED"}, "fr", http.StatusBadRequest, "", "SHARED"},
		{"custom prefix es", true, map[string]string{"new_mailbox": "Team/a"}, "es", http.StatusBadRequest, "", "Team/a"},
		{"ordinary new folder", false, map[string]string{"new_mailbox": "Archive2"}, "en", http.StatusOK, "Archive2", ""},
		{"lookalike", false, map[string]string{"new_mailbox": "SharedFolder"}, "en", http.StatusOK, "SharedFolder", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ps, err := store.NewPebbleStore(dir + "/pebble")
			if err != nil {
				t.Fatalf("NewPebbleStore: %v", err)
			}
			t.Cleanup(func() { ps.Close() })
			bs, err := store.NewBlobStore(dir + "/blobs")
			if err != nil {
				t.Fatalf("NewBlobStore: %v", err)
			}
			mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
			if err := mailboxes.CreateMailbox(user, "INBOX"); err != nil {
				t.Fatalf("CreateMailbox: %v", err)
			}
			h := NewMailHandler(ps, bs, mailboxes, discardLogger())
			if tt.setter {
				h.SetNamespacePrefixes("People", "Team")
			}

			req := uploadReq(t, "/import", "mbox", []byte(mbox), tt.fields)
			req = withUserContext(req, user, email)
			req = req.WithContext(middleware.WithLocale(req.Context(), tt.locale))
			w := httptest.NewRecorder()
			h.ImportMbox(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode == http.StatusBadRequest {
				want := i18n.T(tt.locale, "import.errReservedMailbox")
				if want == "import.errReservedMailbox" || want == "" {
					t.Fatalf("no %s translation for %q", "import.errReservedMailbox", tt.locale)
				}
				if got := strings.TrimSpace(w.Body.String()); got != want {
					t.Errorf("body = %q, want %q", got, want)
				}
			}
			if tt.wantNew != "" {
				if _, err := mailboxes.GetMailbox(user, tt.wantNew); err != nil {
					t.Errorf("mailbox %q not created: %v", tt.wantNew, err)
				}
			}
			if tt.wantAbsnt != "" {
				if _, err := mailboxes.GetMailbox(user, tt.wantAbsnt); err == nil {
					t.Errorf("mailbox %q was created despite the refusal", tt.wantAbsnt)
				}
			}
		})
	}
}

// A legacy folder that already carries a reserved name is still a valid import
// target: nothing is created, so nothing is refused.
func TestImportMboxExistingLegacyReservedFolderStillWorks(t *testing.T) {
	const user, email = "u-legacy", "legacy@example.test"
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
	for _, name := range []string{"INBOX", "Shared"} {
		if err := mailboxes.CreateMailbox(user, name); err != nil {
			t.Fatalf("CreateMailbox(%q): %v", name, err)
		}
	}
	h := NewMailHandler(ps, bs, mailboxes, discardLogger())

	mbox := "From a@example.test Mon Jan  1 00:00:00 2024\nSubject: hi\n\nbody\n"
	req := withUserContext(uploadReq(t, "/import", "mbox", []byte(mbox), map[string]string{"mailbox": "Shared"}), user, email)
	w := httptest.NewRecorder()
	h.ImportMbox(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
}

// A target name that is malformed (not merely reserved) is refused with the
// invalid-name text, before the quota check and any write: no folder, no
// message.
func TestImportMboxRefusesInvalidFolderNames(t *testing.T) {
	const user, email = "u-invalid", "invalid@example.test"
	const mbox = "From a@example.test Mon Jan  1 00:00:00 2024\nSubject: hi\n\nbody\n"

	tests := []struct {
		name    string
		field   string
		value   string
		locale  string
		wantMsg string
	}{
		{"double delimiter", "new_mailbox", "a//b", "en", "import.errInvalidMailbox"},
		{"leading delimiter", "new_mailbox", "/a", "de", "import.errInvalidMailbox"},
		{"over-long", "new_mailbox", strings.Repeat("a", 256), "es", "import.errInvalidMailbox"},
		{"control character", "mailbox", "a\x01b", "fr", "import.errInvalidMailbox"},
		{"virtual view", "new_mailbox", "starred", "en", "import.errReservedMailbox"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ps, err := store.NewPebbleStore(dir + "/pebble")
			if err != nil {
				t.Fatalf("NewPebbleStore: %v", err)
			}
			t.Cleanup(func() { ps.Close() })
			bs, err := store.NewBlobStore(dir + "/blobs")
			if err != nil {
				t.Fatalf("NewBlobStore: %v", err)
			}
			mailboxes := store.NewMailboxStore(ps, &directApplierTags{ps: ps})
			if err := mailboxes.CreateMailbox(user, "INBOX"); err != nil {
				t.Fatalf("CreateMailbox: %v", err)
			}
			h := NewMailHandler(ps, bs, mailboxes, discardLogger())

			req := uploadReq(t, "/import", "mbox", []byte(mbox), map[string]string{tt.field: tt.value})
			req = withUserContext(req, user, email)
			req = req.WithContext(middleware.WithLocale(req.Context(), tt.locale))
			w := httptest.NewRecorder()
			h.ImportMbox(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %q)", w.Code, w.Body.String())
			}
			want := i18n.T(tt.locale, tt.wantMsg)
			if want == tt.wantMsg || want == "" {
				t.Fatalf("no %s translation for %q", tt.wantMsg, tt.locale)
			}
			if got := strings.TrimSpace(w.Body.String()); got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			if strings.Contains(w.Body.String(), tt.value) {
				t.Error("the posted name is echoed")
			}
			boxes, err := mailboxes.ListMailboxes(user)
			if err != nil {
				t.Fatal(err)
			}
			if len(boxes) != 1 || boxes[0].Name != "INBOX" {
				t.Errorf("mailboxes after refusal = %v, want only INBOX", boxes)
			}
			msgs, err := mailboxes.ListMessages(user, "INBOX")
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 0 {
				t.Errorf("INBOX holds %d messages after a refused import", len(msgs))
			}
		})
	}
}
