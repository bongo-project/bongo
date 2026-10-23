package smtp

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/srs"
)

type countingResolver struct {
	catchAllStub
	calls []string
}

func (c *countingResolver) ResolveRecipient(email string) (string, error) {
	c.calls = append(c.calls, email)
	return c.catchAllStub.ResolveRecipient(email)
}

// TestRcptNoRelay: the reserved no-relay sender accepts bounces (null MAIL
// FROM) on a hosted domain, refuses everything else, and is never resolved, so
// a catch-all owner cannot receive it. It works without an SRS codec.
func TestRcptNoRelay(t *testing.T) {
	hostedDomains := func(d string) bool { return d == "hosted.example" }
	addr := srs.NoRelayLocal + "@hosted.example"
	tests := []struct {
		name   string
		from   string
		to     string
		authed bool
		want   int // 0 = accepted
	}{
		{"null sender accepted", "", addr, false, 0},
		{"mixed case accepted", "", strings.ToUpper(srs.NoRelayLocal) + "@Hosted.Example", false, 0},
		{"non-null MAIL FROM refused", "mallory@evil.example", addr, false, 550},
		{"authenticated non-null refused", "bob@hosted.example", addr, true, 550},
		{"unhosted domain takes the ordinary path", "", srs.NoRelayLocal + "@elsewhere.example", false, 550},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &countingResolver{}
			b := &Backend{resolver: res, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			b.SetHostedDomain(hostedDomains)
			s := &Session{backend: b, logger: b.logger, authed: tt.authed, from: tt.from}
			err := s.Rcpt(tt.to, nil)
			if tt.want == 0 {
				if err != nil || len(s.localRcpts) != 1 {
					t.Fatalf("Rcpt = %v, local=%v; want accepted as local", err, s.localRcpts)
				}
			} else if code := smtpCode(err); code != tt.want {
				t.Fatalf("Rcpt code = %d (%v), want %d", code, err, tt.want)
			}
			if strings.HasSuffix(strings.ToLower(tt.to), "@hosted.example") && len(res.calls) != 0 {
				t.Errorf("no-relay address was resolved: %v", res.calls)
			}
			if tt.want != 0 && (len(s.localRcpts) != 0 || len(s.externalRcpts) != 0) {
				t.Fatalf("refused recipient was queued: local=%v ext=%v", s.localRcpts, s.externalRcpts)
			}
		})
	}
}
