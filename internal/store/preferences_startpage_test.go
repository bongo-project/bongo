package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestStartPath(t *testing.T) {
	tests := []struct {
		name  string
		prefs *bongov1.UserPreferences
		want  string
	}{
		{"nil", nil, "/summary"},
		{"empty", &bongov1.UserPreferences{}, "/summary"},
		{"summary", &bongov1.UserPreferences{StartPage: "summary"}, "/summary"},
		{"inbox", &bongov1.UserPreferences{StartPage: "inbox"}, "/mail/INBOX"},
		{"garbage", &bongov1.UserPreferences{StartPage: "//evil.example"}, "/summary"},
		{"wrong case", &bongov1.UserPreferences{StartPage: "INBOX"}, "/summary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StartPath(tt.prefs); got != tt.want {
				t.Errorf("StartPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartPageRoundTripsAndDefaultsToSummary(t *testing.T) {
	ps := newPrefTestEnv(t)
	if got := StartPath(ps.Get("u1")); got != "/summary" {
		t.Fatalf("default start path = %q, want /summary", got)
	}
	p := ps.Get("u1")
	p.StartPage = StartPageInbox
	if err := ps.Set("u1", p); err != nil {
		t.Fatal(err)
	}
	if got := StartPath(ps.Get("u1")); got != "/mail/INBOX" {
		t.Errorf("start path after saving inbox = %q", got)
	}
	if got := StartPath(ps.Get("u2")); got != "/summary" {
		t.Errorf("another user's start path = %q, want /summary", got)
	}
}
