package smtp

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

func enforcingRelay(t *testing.T) *Relay {
	t.Helper()
	r := NewRelay(&config.SMTPRelay{MTASTSEnabled: true}, "mail.example.test", slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	r.mtasts.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	r.mtasts.lookupTXT = func(context.Context, string) ([]string, error) {
		return []string{"v=STSv1; id=abc;"}, nil
	}
	r.mtasts.fetch = func(context.Context, string) (string, error) {
		return "version: STSv1\nmode: enforce\nmx: mx.example.com\n", nil
	}
	return r
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestMTAStsPolicyForMX_Enforce(t *testing.T) {
	r := enforcingRelay(t)

	// Policy-listed MX -> require authenticated TLS, authorized.
	pol := r.mtaStsPolicyForMX(context.Background(), "example.com", "mx.example.com")
	if !pol.requireTLS || !pol.mxAuthorized {
		t.Errorf("listed MX: got %+v, want requireTLS+authorized", pol)
	}

	// MX not in policy -> not authorized (must be skipped).
	pol = r.mtaStsPolicyForMX(context.Background(), "example.com", "evil.attacker.net")
	if pol.mxAuthorized {
		t.Errorf("unlisted MX should not be authorized under an enforcing policy")
	}
}

func TestMTAStsPolicyForMX_DisabledIsOpportunistic(t *testing.T) {
	r := NewRelay(&config.SMTPRelay{MTASTSEnabled: false}, "mail.example.test", slog.New(slog.NewTextHandler(discardWriter{}, nil)))
	pol := r.mtaStsPolicyForMX(context.Background(), "example.com", "mx.example.com")
	if pol.requireTLS || !pol.mxAuthorized {
		t.Errorf("MTA-STS disabled should be opportunistic + authorized, got %+v", pol)
	}
}
