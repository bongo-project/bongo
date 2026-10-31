//go:build integration

package integration

import (
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

// TestSMTPSubmissionListeners runs the same submission checks against 587 and
// the implicit-TLS listener on 465 of a full dev agent.
func TestSMTPSubmissionListeners(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)
	cfg.SMTP.Enabled = true

	a, cancel, _ := sharedStartAgent(t, cfg)
	defer cancel()

	userID, err := auth.CreateUser(a, a.Metadata, "alice@localhost", "Alice", "alicepass", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	resolver := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got, rerr := resolver.ResolveRecipient("alice@localhost"); rerr == nil && got == userID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("alice@localhost did not resolve")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !a.SMTP.SubmissionsEnabled() {
		t.Fatal("implicit-TLS submission is not enabled in dev")
	}
	insecure := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- self-signed dev certificate

	listeners := []struct {
		name string
		dial func() (*gosmtp.Client, error)
	}{
		{"587", func() (*gosmtp.Client, error) { return gosmtp.Dial(a.SMTP.SubmissionAddr()) }},
		{"465", func() (*gosmtp.Client, error) { return gosmtp.DialTLS(a.SMTP.SubmissionsAddr(), insecure) }},
	}
	for _, l := range listeners {
		t.Run(l.name, func(t *testing.T) {
			dial := func(t *testing.T) *gosmtp.Client {
				c, err := l.dial()
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				t.Cleanup(func() { _ = c.Close() })
				if err := c.Hello("client.example"); err != nil {
					t.Fatalf("EHLO: %v", err)
				}
				return c
			}

			t.Run("AUTH offered, no STARTTLS on 465", func(t *testing.T) {
				c := dial(t)
				if ok, params := c.Extension("AUTH"); !ok || params == "" {
					t.Fatalf("AUTH not offered (%q)", params)
				}
				if st, _ := c.Extension("STARTTLS"); l.name == "465" && st {
					t.Error("STARTTLS offered on 465")
				}
			})
			t.Run("unauthenticated MAIL is 530", func(t *testing.T) {
				c := dial(t)
				var se *gosmtp.SMTPError
				if err := c.Mail("alice@localhost", nil); !errors.As(err, &se) || se.Code != 530 {
					t.Fatalf("MAIL = %v, want 530", err)
				}
			})
			t.Run("send-as is enforced after AUTH", func(t *testing.T) {
				c := dial(t)
				if err := c.Auth(sasl.NewPlainClient("", "alice@localhost", "alicepass")); err != nil {
					t.Fatalf("AUTH: %v", err)
				}
				if err := c.Mail("someone-else@example.net", nil); err == nil {
					t.Fatal("MAIL as a foreign address was accepted")
				}
				if err := c.Mail("alice@localhost", nil); err != nil {
					t.Fatalf("MAIL as own address: %v", err)
				}
			})
		})
	}
}
