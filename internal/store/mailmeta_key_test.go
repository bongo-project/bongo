package store

import "testing"

// TestParseMailMetaKey pins the structural parse in isolation, independent of
// Pebble, so every edge case is cheap to enumerate.
func TestParseMailMetaKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantUserID string
		wantGUID   string
		wantOK     bool
	}{
		{"plain mail key", "/user/u1/mail/g1", "u1", "g1", true},
		{"mailbox literally named mail", "/user/u1/mailbox/mail/msg/0000000001", "", "", false},
		{"mailbox named mail, meta key", "/user/u1/mailbox/mail/meta", "", "", false},
		{"non-mail user key", "/user/u1/config/theme", "", "", false},
		{"empty guid", "/user/u1/mail/", "", "", false},
		{"exactly /user/", "/user/", "", "", false},
		{"empty user segment", "/user//mail/g1", "", "", false},
		{"guid containing a slash", "/user/u1/mail/g1/extra", "", "", false},
		{"user id containing mail", "/user/mail/mail/g1", "mail", "g1", true},
		{"segment prefix collision (mailer, not mail)", "/user/u1/mailer/g1", "", "", false},
		{"no leading /user/ prefix", "/other/u1/mail/g1", "", "", false},
		{"root only", "/", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUserID, gotGUID, gotOK := ParseMailMetaKey([]byte(tt.key))
			if gotOK != tt.wantOK || gotUserID != tt.wantUserID || gotGUID != tt.wantGUID {
				t.Errorf("ParseMailMetaKey(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.key, gotUserID, gotGUID, gotOK, tt.wantUserID, tt.wantGUID, tt.wantOK)
			}
		})
	}
}
