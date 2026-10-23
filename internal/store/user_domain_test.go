package store

import "testing"

func TestUserDomains(t *testing.T) {
	ps, _, _, _ := newResolverTestEnv(t)
	putUserProfile(t, ps, "u1", "Alice@Hosted.Example", false)
	putUserProfile(t, ps, "u2", "nodomain", false)
	putUserProfile(t, ps, "u3", "trailing@", false)
	ud := &UserDomains{Pebble: ps}

	tests := []struct {
		user, want string
		wantErr    bool
	}{
		{"u1", "hosted.example", false},
		{"u2", "", true},
		{"u3", "", true},
		{"missing", "", true},
	}
	for _, tt := range tests {
		got, err := ud.UserDomain(tt.user)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("UserDomain(%q) = %q, %v; want %q, err=%v", tt.user, got, err, tt.want, tt.wantErr)
		}
	}
	if _, err := (&UserDomains{}).UserDomain("u1"); err == nil {
		t.Error("nil store must error")
	}
}
