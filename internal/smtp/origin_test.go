package smtp

import (
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// An authenticated submission to an external recipient is queued with a LOCAL
// origin so the queue worker DKIM-signs it. A dropped Origin would still sign
// (UNSPECIFIED is signed), so pin it here.
func TestSubmissionExternalRelayOriginLocal(t *testing.T) {
	b, _, relay := testBackend("alice@example.com")
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}

	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt("outsider@example.net", nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	if err := s.Data(strings.NewReader("From: alice@example.com\r\nSubject: hi\r\n\r\nbody")); err != nil {
		t.Fatalf("Data: %v", err)
	}
	if len(relay.enqueued) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(relay.enqueued))
	}
	if got := relay.enqueued[0].GetOrigin(); got != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL {
		t.Errorf("origin = %v, want LOCAL", got)
	}
}

// AuthUserID reaches the local-delivery metadata only for authenticated
// sessions; a trusted-network session carries none, so the antispam stage can
// tell the two apart.
func TestInboundMetaAuthUserID(t *testing.T) {
	b, _, _ := testBackend()
	tests := []struct {
		name   string
		authed bool
		want   string
	}{
		{"authenticated session carries user ID", true, "uid-alice"},
		{"unauthenticated session carries none", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{backend: b, logger: b.logger, authed: tt.authed, userID: "uid-alice"}
			if got := s.inboundMeta("203.0.113.5").AuthUserID; got != tt.want {
				t.Errorf("AuthUserID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnqueueLocalCopiesAuthUserID(t *testing.T) {
	dp, _, spool := setupTestPipeline(t)
	meta := &InboundMeta{Trusted: true, AuthUserID: "uid-alice"}
	if err := dp.EnqueueLocal("alice@example.com", []string{"bob@example.com"}, "hash", meta); err != nil {
		t.Fatalf("EnqueueLocal: %v", err)
	}
	envs, err := spool.List("incoming")
	if err != nil || len(envs) != 1 {
		t.Fatalf("spool List = (%d envelopes, %v), want 1", len(envs), err)
	}
	if envs[0].AuthUserId != "uid-alice" {
		t.Errorf("AuthUserId = %q, want uid-alice", envs[0].AuthUserId)
	}
}
