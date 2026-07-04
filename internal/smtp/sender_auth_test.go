package smtp

import "testing"

// An authenticated submission user may use their own local address as the
// envelope sender (the resolved owner matches the session user).
func TestSubmissionAllowsOwnMailFrom(t *testing.T) {
	b, _, _ := testBackend("alice@example.com", "ceo@example.com")
	// fakeResolver maps a local address to "uid-"+address, so alice's session
	// userID must match that to represent "the authenticated user is alice".
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("user should be allowed to send as their own address, got %v", err)
	}
}

// An authenticated submission user may use an envelope sender that is not a
// provisioned local account (e.g. a role or external address); those are
// policed by the relay/SPF layer, not the sender-ownership check.
func TestSubmissionAllowsNonLocalMailFrom(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if err := s.Mail("newsletter@example.com", nil); err != nil {
		t.Fatalf("non-provisioned sender should not be rejected by the ownership check, got %v", err)
	}
}

// A different local user's address is rejected as spoofing (mirrors the
// exposing test but asserts the specific 553 code).
func TestSubmissionRejectsOtherLocalUserWith553(t *testing.T) {
	b, _, _ := testBackend("alice@example.com", "ceo@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if code := smtpCode(s.Mail("ceo@example.com", nil)); code != 553 {
		t.Fatalf("spoofing another local user should return 553, got %d", code)
	}
}
