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

// Previously asserted that any non-local envelope sender was accepted (the
// spoofing hole). Inverted deliberately: an address the user has no verified
// send-as identity for is now refused at MAIL FROM.
func TestSubmissionRejectsUnownedNonLocalMailFromUnit(t *testing.T) {
	b, _, _ := testBackend("alice@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if code := smtpCode(s.Mail("newsletter@example.com", nil)); code != 553 {
		t.Fatalf("unowned sender should be 553, got %d", code)
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
