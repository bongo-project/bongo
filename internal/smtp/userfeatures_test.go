package smtp

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func setSMTPProfileFeatures(t *testing.T, ps *store.PebbleStore, features []string, limits *bongov1.SendLimits) {
	t.Helper()
	data, err := ps.Get([]byte("/user/uid1/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.DisabledFeatures = features
	p.SendLimits = limits
	if data, err = proto.Marshal(&p); err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/uid1/profile"), data); err != nil {
		t.Fatal(err)
	}
}

// AUTH with submission switched off returns exactly the error a wrong
// password returns; a different switch does not matter.
func TestSMTPAuthFeatureDisabledMatchesWrongPassword(t *testing.T) {
	b, ps := revokeBackend(t)
	putSMTPProfile(t, ps, "pw", false)
	setSMTPProfileFeatures(t, ps, []string{store.FeatureSubmission}, nil)

	wrong := newSessionForIP(b, "203.0.113.9").AuthPlain("u@example.com", "nope")
	disabled := newSessionForIP(b, "203.0.113.10").AuthPlain("u@example.com", "pw")
	if wrong == nil || disabled == nil {
		t.Fatalf("expected both refused: wrong=%v disabled=%v", wrong, disabled)
	}
	if wrong.Error() != disabled.Error() || smtpCode(wrong) != smtpCode(disabled) {
		t.Fatalf("disabled reply %v differs from wrong-password reply %v", disabled, wrong)
	}

	setSMTPProfileFeatures(t, ps, []string{store.FeatureIMAP, store.FeaturePOP3}, nil)
	if err := newSessionForIP(b, "203.0.113.11").AuthPlain("u@example.com", "pw"); err != nil {
		t.Fatalf("AUTH with only other protocols disabled: %v", err)
	}
}

// An authenticated submission session ends on the next command once
// submission is switched off.
func TestSMTPSessionEndsWhenFeatureDisabled(t *testing.T) {
	b, ps := revokeBackend(t)
	putSMTPProfile(t, ps, "pw", false)
	s := newSessionForIP(b, "203.0.113.9")
	if err := s.AuthPlain("u@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkAccountLive("MAIL"); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	setSMTPProfileFeatures(t, ps, []string{store.FeatureSubmission}, nil)
	if code := smtpCode(s.Mail("u@example.com", nil)); code != 535 {
		t.Fatalf("MAIL after switch = code %d, want 535", code)
	}
}
