package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
	"google.golang.org/protobuf/proto"
)

func TestProfileFeaturesLookup(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	data, err := proto.Marshal(&bongov1.UserProfile{
		Email:            "a@x.test",
		DisabledFeatures: []string{FeatureRules},
		SendLimits:       &bongov1.SendLimits{MessagesPerMinute: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/u1/profile"), data); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/bad/profile"), []byte{0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}

	lookup := NewProfileFeatures(ps.Get)
	tests := []struct {
		name    string
		user    string
		feature string
		off     bool
		wantErr bool
	}{
		{"switched off", "u1", FeatureRules, true, false},
		{"not switched off", "u1", FeatureVacation, false, false},
		{"missing profile fails closed without an error", "gone", FeatureRules, true, false},
		{"unreadable profile fails closed with an error", "bad", FeatureRules, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off, err := lookup.FeatureDisabled(tt.user, tt.feature)
			if off != tt.off || (err != nil) != tt.wantErr {
				t.Fatalf("off=%v err=%v, want off=%v err=%v", off, err, tt.off, tt.wantErr)
			}
		})
	}

	got, err := ReadSendLimits(ps.Get, "u1")
	if err != nil || got.MessagesPerMinute != 3 {
		t.Fatalf("ReadSendLimits = %v, %v", got, err)
	}
	if _, err := ReadSendLimits(ps.Get, "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile: %v, want ErrNotFound", err)
	}
	plain, _ := proto.Marshal(&bongov1.UserProfile{Email: "b@x.test"})
	if err := ps.Set([]byte("/user/u2/profile"), plain); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadSendLimits(ps.Get, "u2"); err != nil || got == nil || got.MessagesPerMinute != 0 {
		t.Fatalf("no limits stored: %v, %v", got, err)
	}
}
