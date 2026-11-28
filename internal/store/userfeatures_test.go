package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
	"google.golang.org/protobuf/proto"
)

func TestFeatureDisabled(t *testing.T) {
	p := &bongov1.UserProfile{DisabledFeatures: []string{"imap", "future-thing"}}
	tests := []struct {
		name string
		p    *bongov1.UserProfile
		f    string
		want bool
	}{
		{"listed", p, FeatureIMAP, true},
		{"not listed", p, FeaturePOP3, false},
		{"unknown stored name", p, "future-thing", true},
		{"nil profile", nil, FeatureIMAP, false},
		{"empty profile", &bongov1.UserProfile{}, FeatureWeb, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FeatureDisabled(tt.p, tt.f); got != tt.want {
				t.Fatalf("FeatureDisabled = %v, want %v", got, tt.want)
			}
		})
	}
	if KnownFeature("future-thing") || !KnownFeature(FeatureVacation) {
		t.Fatal("KnownFeature wrong")
	}
	if len(KnownFeatures()) != 8 {
		t.Fatalf("want 8 features, got %d", len(KnownFeatures()))
	}
}

func TestEffectiveLimit(t *testing.T) {
	tests := []struct {
		per    uint32
		global int
		want   int
	}{
		{0, 30, 30}, {0, 0, 0}, {5, 30, 5}, {60, 30, 60}, {5, 0, 5},
	}
	for _, tt := range tests {
		if got := EffectiveLimit(tt.per, tt.global); got != tt.want {
			t.Errorf("EffectiveLimit(%d,%d) = %d, want %d", tt.per, tt.global, got, tt.want)
		}
	}
}

func TestValidateSendLimits(t *testing.T) {
	tests := []struct {
		name string
		l    *bongov1.SendLimits
		ok   bool
	}{
		{"nil", nil, true},
		{"zero", &bongov1.SendLimits{}, true},
		{"max bounds", &bongov1.SendLimits{MessagesPerMinute: 10000, RecipientsPerHour: 100000, MaxRecipients: 1000}, true},
		{"mpm over", &bongov1.SendLimits{MessagesPerMinute: 10001}, false},
		{"rph over", &bongov1.SendLimits{RecipientsPerHour: 100001}, false},
		{"max rcpts over", &bongov1.SendLimits{MaxRecipients: 1001}, false},
		{"rph below max", &bongov1.SendLimits{RecipientsPerHour: 50, MaxRecipients: 100}, false},
		{"rph equal max", &bongov1.SendLimits{RecipientsPerHour: 100, MaxRecipients: 100}, true},
		{"rph set alone small", &bongov1.SendLimits{RecipientsPerHour: 1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSendLimits(tt.l)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidSendLimits) {
				t.Fatalf("error does not wrap ErrInvalidSendLimits: %v", err)
			}
		})
	}
}

// A resource profile keeps its capacity and resource type when the switches
// and limits are set and the profile makes a wire round trip.
func TestResourceProfileRoundTripWithFeatures(t *testing.T) {
	p := &bongov1.UserProfile{Email: "room@x.test", Kind: "resource", Capacity: 12, ResourceType: "room"}
	p.DisabledFeatures = []string{"imap", "web"}
	p.SendLimits = &bongov1.SendLimits{MessagesPerMinute: 3, RecipientsPerHour: 40, MaxRecipients: 10}
	b, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got bongov1.UserProfile
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Capacity != 12 || got.ResourceType != "room" || got.Kind != "resource" {
		t.Fatalf("resource fields changed: %+v", &got)
	}
	if !proto.Equal(p, &got) {
		t.Fatal("round trip changed the profile")
	}
}
