package attachpolicy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A store failure while deciding whose policy applies must fail the stage, so
// the envelope is held in error/ instead of being delivered unchecked. Each
// recipient's literal domain has no policy, so falling back to it would let
// the exe through.
func TestDomainLookupStoreErrorsFailTheStage(t *testing.T) {
	storeDown := fmt.Errorf("pebble get: %w", errors.New("disk on fire"))
	tests := []struct {
		name  string
		rcpt  string
		setup func(d *Deps)
	}{
		{"resolve", "bob@other.test", func(d *Deps) {
			d.Resolve = func(string) (string, error) {
				return "", fmt.Errorf("resolve recipient: alias target lookup: %w", storeDown)
			}
		}},
		{"user domain", "bob@other.test", func(d *Deps) {
			d.UserDomain = func(string) (string, error) { return "", fmt.Errorf("user domain: load profile: %w", storeDown) }
		}},
		{"policy domain", "nobody@alias.test", func(d *Deps) {
			d.Resolve = func(email string) (string, error) {
				return "", fmt.Errorf("resolve recipient: %s: not a local address", email)
			}
			d.PolicyDomain = func(domain string) (string, bool, error) { return domain, true, storeDown }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, policy("strip"))
			e.proc.d.Resolve = func(string) (string, error) { return "u1", nil }
			tt.setup(&e.proc.d)
			en := &bongov1.QueueEnvelope{Id: "m", Recipients: []string{tt.rcpt}, BlobHash: e.put(t, withExe)}
			orig := en.BlobHash
			res, err := e.proc.Process(context.Background(), en, strings.NewReader(withExe))
			if err == nil {
				t.Fatalf("store error passed the stage: %+v", res)
			}
			if !errors.Is(err, storeDown) {
				t.Errorf("error does not wrap the cause: %v", err)
			}
			if en.BlobHash != orig {
				t.Error("envelope changed on a failed stage")
			}
		})
	}
}

// An address that is simply not a local user, and a profile that has gone,
// are not store failures: the recipient's own domain decides.
func TestDomainLookupMissesFallBack(t *testing.T) {
	tests := []struct {
		name  string
		rcpt  string
		setup func(d *Deps)
	}{
		{"not local", "nobody@corp.test", func(d *Deps) {
			d.Resolve = func(email string) (string, error) {
				return "", fmt.Errorf("resolve recipient: %s: not a local address", email)
			}
		}},
		{"unavailable", "bob@corp.test", func(d *Deps) {
			d.Resolve = func(email string) (string, error) {
				return "", fmt.Errorf("resolve recipient: %s: %w", email, store.ErrRecipientUnavailable)
			}
		}},
		{"group", "team@corp.test", func(d *Deps) {
			d.Resolve = func(email string) (string, error) { return "", &store.GroupRecipientError{Email: email, GroupID: "g"} }
		}},
		{"profile gone", "bob@corp.test", func(d *Deps) {
			d.UserDomain = func(string) (string, error) {
				return "", fmt.Errorf("user domain: load profile: %w", store.ErrNotFound)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, policy("strip"))
			tt.setup(&e.proc.d)
			if _, res := e.run(t, tt.rcpt, withExe); res.Action != "strip" {
				t.Fatalf("action = %q", res.Action)
			}
		})
	}
}

// The real resolver's refusal of an unknown address must read as a miss, not
// as a store failure, or mail to unknown hosted addresses would never move.
func TestRealResolverMissIsNotAStoreError(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	r := store.NewRecipientResolver(nil, ps, nil)
	e := newEnv(t, policy("strip"))
	e.proc.d.Resolve = r.ResolveRecipient
	for _, rcpt := range []string{"nobody@corp.test", "x@elsewhere.test", "postmaster"} {
		en := &bongov1.QueueEnvelope{Id: "m", Recipients: []string{rcpt}, BlobHash: e.put(t, withExe)}
		if _, err := e.proc.Process(context.Background(), en, strings.NewReader(withExe)); err != nil {
			t.Fatalf("%s: %v", rcpt, err)
		}
	}
}

// Stripping changes the body, so DKIM is judged on the message as it arrived
// and the verdict travels in the stage result.
func TestStripRecordsDKIMOfOriginal(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var signed strings.Builder
	if err := dkim.Sign(&signed, strings.NewReader(withExe), &dkim.SignOptions{
		Domain: "example.org", Selector: "s1", Signer: priv,
		HeaderKeys: []string{"From", "To", "Subject", "MIME-Version", "Content-Type"},
	}); err != nil {
		t.Fatal(err)
	}
	prev := antispam.LookupTXT
	antispam.LookupTXT = func(name string) ([]string, error) {
		if name == "s1._domainkey.example.org" {
			return []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)}, nil
		}
		return nil, errors.New("no such record")
	}
	t.Cleanup(func() { antispam.LookupTXT = prev })

	if r, d, _ := antispam.VerifyDKIM([]byte(signed.String())); r != "pass" || d != "example.org" {
		t.Fatalf("fixture does not verify: %s %s", r, d)
	}
	e := newEnv(t, policy("strip"))
	en, res := e.run(t, "bob@corp.test", signed.String())
	if res.Action != "strip" {
		t.Fatalf("res = %+v", res)
	}
	if res.DkimResult != "pass" || res.DkimDomain != "example.org" {
		t.Fatalf("dkim_result=%q dkim_domain=%q", res.DkimResult, res.DkimDomain)
	}
	stored := e.read(t, en.BlobHash)
	if !strings.Contains(stored, "DKIM-Signature:") {
		t.Fatal("signature header not kept")
	}
	if r, _, _ := antispam.VerifyDKIM([]byte(stored)); r == "pass" {
		t.Fatal("the stripped copy still verifies; the docs say it does not")
	}
}
