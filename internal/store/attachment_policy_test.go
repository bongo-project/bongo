package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newAttachmentPolicyEnv(t *testing.T) (*PebbleStore, *AttachmentPolicyStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps, NewAttachmentPolicyStore(ps, &directApplier{store: ps})
}

func TestAttachmentPolicyKey(t *testing.T) {
	if got := string(AttachmentPolicyKey("Example.COM")); got != "/system/domain/example.com/attachment_policy" {
		t.Fatalf("key = %q", got)
	}
}

func TestAttachmentPolicyStore_GetMissing(t *testing.T) {
	_, s := newAttachmentPolicyEnv(t)
	p, err := s.Get("example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p != nil {
		t.Fatalf("missing key must mean no policy, got %+v", p)
	}
}

func TestAttachmentPolicyStore_PutGet(t *testing.T) {
	ps, s := newAttachmentPolicyEnv(t)
	in := &bongov1.AttachmentPolicy{
		Enabled:           true,
		BlockedExtensions: []string{".EXE", "scr", "exe", " Js "},
		BlockedMimeTypes:  []string{"Application/X-Msdownload", "application/x-msdownload"},
		Action:            "strip",
		Language:          "de",
	}
	if err := s.Put("Example.com", in, "admin1"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := ps.Get(AttachmentPolicyKey("example.com")); err != nil {
		t.Fatalf("key not written: %v", err)
	}
	got, err := s.Get("example.com")
	if err != nil || got == nil {
		t.Fatalf("Get: %v %v", got, err)
	}
	if strings.Join(got.BlockedExtensions, ",") != "exe,js,scr" {
		t.Errorf("extensions = %v", got.BlockedExtensions)
	}
	if strings.Join(got.BlockedMimeTypes, ",") != "application/x-msdownload" {
		t.Errorf("mime = %v", got.BlockedMimeTypes)
	}
	if got.Action != "strip" || got.Language != "de" || !got.Enabled || got.UpdatedBy != "admin1" || got.UpdatedAt == 0 {
		t.Errorf("got %+v", got)
	}
}

func TestNormalizeAttachmentPolicy(t *testing.T) {
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("x%d", i)
	}
	tests := []struct {
		name  string
		p     *bongov1.AttachmentPolicy
		field string // "" means valid
	}{
		{"ok", &bongov1.AttachmentPolicy{Action: "quarantine", BlockedExtensions: []string{"exe"}}, ""},
		{"bad action", &bongov1.AttachmentPolicy{Action: "delete"}, "action"},
		{"empty action", &bongov1.AttachmentPolicy{}, "action"},
		{"ext with slash", &bongov1.AttachmentPolicy{Action: "strip", BlockedExtensions: []string{"a/b"}}, "extensions"},
		{"ext with space", &bongov1.AttachmentPolicy{Action: "strip", BlockedExtensions: []string{"a b"}}, "extensions"},
		{"ext too long", &bongov1.AttachmentPolicy{Action: "strip", BlockedExtensions: []string{strings.Repeat("a", 17)}}, "extensions"},
		{"ext empty", &bongov1.AttachmentPolicy{Action: "strip", BlockedExtensions: []string{"."}}, "extensions"},
		{"too many ext", &bongov1.AttachmentPolicy{Action: "strip", BlockedExtensions: many}, "extensions"},
		{"mime no slash", &bongov1.AttachmentPolicy{Action: "strip", BlockedMimeTypes: []string{"exe"}}, "mime_types"},
		{"mime params", &bongov1.AttachmentPolicy{Action: "strip", BlockedMimeTypes: []string{"a/b; c=d"}}, "mime_types"},
		{"mime long", &bongov1.AttachmentPolicy{Action: "strip", BlockedMimeTypes: []string{"a/" + strings.Repeat("b", 130)}}, "mime_types"},
		{"bad language", &bongov1.AttachmentPolicy{Action: "strip", Language: "xx"}, "language"},
		{"good language", &bongov1.AttachmentPolicy{Action: "strip", Language: "fr"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NormalizeAttachmentPolicy(tt.p)
			if (err != nil) != (tt.field != "") {
				t.Fatalf("err = %v, want field %q", err, tt.field)
			}
			if tt.field != "" {
				var fe *AttachmentPolicyFieldError
				if !errors.As(err, &fe) || fe.Field != tt.field {
					t.Fatalf("err = %v, want field %q", err, tt.field)
				}
			}
		})
	}
}

func TestAttachmentPolicyStore_PutRejectsInvalid(t *testing.T) {
	ps, s := newAttachmentPolicyEnv(t)
	if err := s.Put("example.com", &bongov1.AttachmentPolicy{Action: "nope"}, "a"); err == nil {
		t.Fatal("want error")
	}
	if _, err := ps.Get(AttachmentPolicyKey("example.com")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid policy must not be written: %v", err)
	}
}

func TestAttachmentPolicyStore_DomainPurgeRemovesKey(t *testing.T) {
	ps, s := newAttachmentPolicyEnv(t)
	app := &directApplier{store: ps}
	if err := s.Put("example.com", &bongov1.AttachmentPolicy{Enabled: true, Action: "strip"}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Get(AttachmentPolicyKey("example.com")); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := PurgeDomainConfig(ps, app, "example.com"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := ps.Get(AttachmentPolicyKey("example.com")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("policy key survived the purge: %v", err)
	}
}
