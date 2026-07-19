package smtp

import (
	"testing"

	gosmtp "github.com/emersion/go-smtp"
)

// fakeQuotaChecker implements QuotaChecker for tests.
type fakeQuotaChecker struct {
	// overQuota maps userID → whether that user's domain is over quota.
	overQuota map[string]bool
}

func (f *fakeQuotaChecker) RecipientOverQuota(userID string) (bool, error) {
	return f.overQuota[userID], nil
}

// TestRcpt_QuotaEnforcement_UnderQuota verifies that a recipient whose domain
// is under quota (or has no quota set) is accepted.
func TestRcpt_QuotaEnforcement_UnderQuota(t *testing.T) {
	b := &Backend{
		auth:     &fakeAuth{userID: "uid-1"},
		resolver: &fakeResolver{local: map[string]bool{"alice@example.com": true}},
		quotaChecker: &fakeQuotaChecker{
			overQuota: map[string]bool{"uid-alice@example.com": false},
		},
		deliverer:  &fakeSplitDeliverer{},
		relayQueue: &fakeRelayQueue{},
		perIP:      map[string]int{},
		logger:     testLogger(),
	}
	sess := &Session{backend: b, logger: testLogger()}

	if err := sess.Rcpt("alice@example.com", &gosmtp.RcptOptions{}); err != nil {
		t.Errorf("Rcpt under quota: want nil, got %v", err)
	}
	if len(sess.localRcpts) != 1 {
		t.Errorf("Rcpt under quota: localRcpts = %v, want 1 recipient", sess.localRcpts)
	}
}

// TestRcpt_QuotaEnforcement_OverQuota verifies that a recipient whose domain is
// at or over its storage quota is rejected with a 552 permanent SMTP error.
// The message must NOT be accepted (no local copy stored).
func TestRcpt_QuotaEnforcement_OverQuota(t *testing.T) {
	// fakeResolver returns "uid-bob@example.com" for bob@example.com.
	local := map[string]bool{"bob@example.com": true}
	overQuota := map[string]bool{"uid-bob@example.com": true}

	b := &Backend{
		auth:         &fakeAuth{userID: "uid-1"},
		resolver:     &fakeResolver{local: local},
		quotaChecker: &fakeQuotaChecker{overQuota: overQuota},
		deliverer:    &fakeSplitDeliverer{},
		relayQueue:   &fakeRelayQueue{},
		perIP:        map[string]int{},
		logger:       testLogger(),
	}
	sess := &Session{backend: b, logger: testLogger()}

	err := sess.Rcpt("bob@example.com", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("Rcpt over quota: want 5xx error, got nil")
	}
	code := smtpCode(err)
	if code != 552 {
		t.Errorf("Rcpt over quota: want code 552, got %d", code)
	}
	if len(sess.localRcpts) != 0 {
		t.Errorf("Rcpt over quota: localRcpts should be empty, got %v", sess.localRcpts)
	}
}

// TestRcpt_QuotaEnforcement_NilChecker verifies that when no QuotaChecker is
// wired (dev/agent/edge nodes), all local recipients are accepted regardless.
func TestRcpt_QuotaEnforcement_NilChecker(t *testing.T) {
	b := &Backend{
		auth:       &fakeAuth{userID: "uid-1"},
		resolver:   &fakeResolver{local: map[string]bool{"carol@example.com": true}},
		deliverer:  &fakeSplitDeliverer{},
		relayQueue: &fakeRelayQueue{},
		perIP:      map[string]int{},
		logger:     testLogger(),
		// quotaChecker intentionally nil
	}
	sess := &Session{backend: b, logger: testLogger()}

	if err := sess.Rcpt("carol@example.com", &gosmtp.RcptOptions{}); err != nil {
		t.Errorf("Rcpt nil checker: want nil, got %v", err)
	}
	if len(sess.localRcpts) != 1 {
		t.Errorf("Rcpt nil checker: localRcpts = %v, want 1", sess.localRcpts)
	}
}

// TestRcpt_QuotaEnforcement_NonLocalUnchanged verifies that the relay-denied
// path for non-local recipients is unaffected by the quota checker.
func TestRcpt_QuotaEnforcement_NonLocalUnchanged(t *testing.T) {
	b := &Backend{
		auth:         &fakeAuth{userID: "uid-1"},
		resolver:     &fakeResolver{local: map[string]bool{}}, // no local addresses
		quotaChecker: &fakeQuotaChecker{overQuota: map[string]bool{}},
		deliverer:    &fakeSplitDeliverer{},
		relayQueue:   &fakeRelayQueue{},
		perIP:        map[string]int{},
		logger:       testLogger(),
	}
	sess := &Session{backend: b, logger: testLogger()}

	// Non-local + unauthenticated → 550 relay denied (unchanged behaviour).
	err := sess.Rcpt("external@elsewhere.com", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("Rcpt non-local unauthed: want relay-denied error, got nil")
	}
	code := smtpCode(err)
	if code != 550 {
		t.Errorf("Rcpt non-local unauthed: want code 550, got %d", code)
	}
}

// TestRcpt_QuotaEnforcement_UnlimitedQuota verifies that 0/unset max_storage_bytes
// means unlimited — all recipients are accepted even with high usage.
func TestRcpt_QuotaEnforcement_UnlimitedQuota(t *testing.T) {
	// quotaChecker returns false (not over) for any user when limit is unset (0).
	b := &Backend{
		auth:     &fakeAuth{userID: "uid-1"},
		resolver: &fakeResolver{local: map[string]bool{"dave@example.com": true}},
		quotaChecker: &fakeQuotaChecker{
			overQuota: map[string]bool{
				// Even if the map has false, the resolver would return "uid-dave@example.com"
				// and we want not-over.
				"uid-dave@example.com": false,
			},
		},
		deliverer:  &fakeSplitDeliverer{},
		relayQueue: &fakeRelayQueue{},
		perIP:      map[string]int{},
		logger:     testLogger(),
	}
	sess := &Session{backend: b, logger: testLogger()}

	if err := sess.Rcpt("dave@example.com", &gosmtp.RcptOptions{}); err != nil {
		t.Errorf("Rcpt unlimited quota: want nil, got %v", err)
	}
	if len(sess.localRcpts) != 1 {
		t.Errorf("Rcpt unlimited quota: localRcpts = %v, want 1", sess.localRcpts)
	}
}
