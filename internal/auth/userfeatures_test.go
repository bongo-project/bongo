package auth

import (
	"errors"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A protocol switched off refuses like a wrong password, with the same error
// and one bcrypt comparison; other protocols are unaffected.
func TestAuthenticateProtocolFeatureDisabled(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "pw")
	e.mutateProfile(t, "u1", func(p *bongov1.UserProfile) {
		p.DisabledFeatures = []string{store.FeatureIMAP, "from-the-future"}
	})

	tests := []struct {
		feature string
		ok      bool
	}{
		{store.FeatureIMAP, false},
		{store.FeaturePOP3, true},
		{store.FeatureSubmission, true},
		{store.FeatureDAV, true},
	}
	for _, tt := range tests {
		t.Run(tt.feature, func(t *testing.T) {
			e.compare.Store(0)
			id, _, _, err := e.la.AuthenticateProtocol(tt.feature, "alice@example.com", "pw")
			if n := e.compare.Load(); n != 1 {
				t.Errorf("bcrypt comparisons = %d, want 1", n)
			}
			if tt.ok != (err == nil && id == "u1") {
				t.Fatalf("id=%q err=%v, want ok=%v", id, err, tt.ok)
			}
			if !tt.ok && !errors.Is(err, ErrBadCredentials) {
				t.Fatalf("err = %v, want ErrBadCredentials", err)
			}
		})
	}

	_, _, _, refused := e.la.AuthenticateProtocol(store.FeatureIMAP, "alice@example.com", "pw")
	_, _, _, wrong := e.la.AuthenticateProtocol(store.FeatureIMAP, "alice@example.com", "nope")
	if refused != wrong {
		t.Errorf("refusal %v differs from wrong password %v", refused, wrong)
	}
}

// Disabling a feature after login makes AccountState not live for that
// protocol only, and the epoch does not move.
func TestAccountStateFeatureDisabled(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "pw")
	_, profile, _, err := e.la.AuthenticateProtocol(store.FeatureIMAP, "alice@example.com", "pw")
	if err != nil {
		t.Fatal(err)
	}
	epoch := ProtocolEpoch(profile)

	st, err := e.la.AccountState("u1", "", store.FeatureIMAP)
	if err != nil || !st.Live(epoch) {
		t.Fatalf("before: live=%v err=%v", st.Live(epoch), err)
	}

	e.mutateProfile(t, "u1", func(p *bongov1.UserProfile) {
		p.DisabledFeatures = []string{store.FeatureIMAP}
		p.SendLimits = &bongov1.SendLimits{MessagesPerMinute: 4}
	})
	st, err = e.la.AccountState("u1", "", store.FeatureIMAP)
	if err != nil {
		t.Fatal(err)
	}
	if st.Live(epoch) || !st.FeatureDisabled {
		t.Errorf("imap session still live after the switch: %+v", st)
	}
	if st.SendLimits.GetMessagesPerMinute() != 4 {
		t.Errorf("send limits not carried: %+v", st.SendLimits)
	}
	st, err = e.la.AccountState("u1", "", store.FeaturePOP3)
	if err != nil || !st.Live(epoch) {
		t.Errorf("pop3 session ended by an imap switch: live=%v err=%v", st.Live(epoch), err)
	}
	if !(AccountState{Epoch: "x"}).Live("x") {
		t.Error("zero value must stay live")
	}
}

// Web login and re-authentication refuse an account with web switched off.
func TestAuthenticateProfileWebDisabled(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "pw")
	if _, _, err := e.la.AuthenticateProfile("alice@example.com", "pw"); err != nil {
		t.Fatal(err)
	}
	e.mutateProfile(t, "u1", func(p *bongov1.UserProfile) { p.DisabledFeatures = []string{store.FeatureWeb} })
	if _, _, err := e.la.AuthenticateProfile("alice@example.com", "pw"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("web disabled: err = %v, want ErrBadCredentials", err)
	}
	// Protocols are still open.
	if _, _, _, err := e.la.AuthenticateProtocol(store.FeatureIMAP, "alice@example.com", "pw"); err != nil {
		t.Errorf("imap with only web disabled: %v", err)
	}
}
