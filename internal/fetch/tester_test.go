package fetch

import (
	"errors"
	"testing"

	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
)

func TestTesterIMAP(t *testing.T) {
	tests := []struct {
		name     string
		security string
		folder   string
		pass     string
		wantCode string
	}{
		{"ok implicit TLS", SecurityTLS, "", testPass, ""},
		{"ok named folder", SecurityTLS, "INBOX", testPass, ""},
		{"ok starttls", SecurityStartTLS, "", testPass, ""},
		{"wrong password", SecurityTLS, "", "nope", CodeAuthFailed},
		{"missing folder", SecurityTLS, "Nowhere", testPass, CodeFolderMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: tt.security == SecurityTLS}, 2)
			p := e.params(tt.security)
			p.Password = tt.pass
			err := e.c.Test(ctx(t), ProtocolIMAP, tt.folder, p)
			if got := Code(err); got != tt.wantCode {
				t.Fatalf("Code = %q (%v), want %q", got, err, tt.wantCode)
			}
		})
	}
}

// The test button must never change or even select the remote folder
// read-write: EXAMINE only, and no message is fetched.
func TestTesterIMAPOnlyExamines(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 2)
	if err := e.c.Test(ctx(t), ProtocolIMAP, "", e.params(SecurityTLS)); err != nil {
		t.Fatal(err)
	}
	if n := e.srv.Count("EXAMINE"); n != 1 {
		t.Fatalf("EXAMINE count = %d, want 1 (commands %v)", n, e.srv.Commands())
	}
	for _, verb := range []string{"SELECT", "FETCH", "UID FETCH", "STORE", "UID STORE", "EXPUNGE", "UID EXPUNGE"} {
		if n := e.srv.Count(verb); n != 0 {
			t.Fatalf("%s sent %d times: %v", verb, n, e.srv.Commands())
		}
	}
}

func TestTesterPOP3(t *testing.T) {
	tests := []struct {
		name     string
		noUIDL   bool
		pass     string
		wantCode string
	}{
		{"ok", false, testPass, ""},
		{"wrong password", false, "nope", CodeAuthFailed},
		{"no UIDL", true, testPass, CodeNoUIDL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, NoUIDL: tt.noUIDL, Msgs: oneMsg()})
			p := e.params(SecurityTLS)
			p.Password = tt.pass
			err := e.c.Test(ctx(t), ProtocolPOP3, "", p)
			if got := Code(err); got != tt.wantCode {
				t.Fatalf("Code = %q (%v), want %q", got, err, tt.wantCode)
			}
		})
	}
}

func TestTesterPOP3NeverRetrievesOrDeletes(t *testing.T) {
	e := newPOP3Env(t, &fetchtest.POP3Server{Implicit: true, Msgs: oneMsg()})
	if err := e.c.Test(ctx(t), ProtocolPOP3, "", e.params(SecurityTLS)); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"RETR", "DELE", "TOP"} {
		if n := e.srv.Count(verb); n != 0 {
			t.Fatalf("%s sent %d times: %v", verb, n, e.srv.Commands())
		}
	}
	if e.srv.Marked(1) || e.srv.Committed(1) {
		t.Fatal("message 1 was marked or deleted")
	}
}

func TestTesterUnknownProtocol(t *testing.T) {
	e := newIMAPEnv(t, fetchtest.IMAPOptions{Implicit: true}, 0)
	err := e.c.Test(ctx(t), "smtp", "", e.params(SecurityTLS))
	if err == nil {
		t.Fatal("want an error")
	}
	if got := Code(err); got != CodeInternal {
		t.Fatalf("Code = %q, want internal", got)
	}
	if errors.Is(err, ErrAuth) {
		t.Fatal("unexpected auth error")
	}
}
