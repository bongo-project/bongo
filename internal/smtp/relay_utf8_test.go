package smtp

import (
	"context"
	"net"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"
)

func TestNeedsSMTPUTF8(t *testing.T) {
	tests := []struct {
		name string
		from string
		rcpt []string
		raw  string
		want bool
	}{
		{"ascii only", "a@example.com", []string{"b@remote.test"}, "Subject: t\r\n\r\nhello\r\n", false},
		{"utf8 sender local part", "jörg@example.com", []string{"b@remote.test"}, "Subject: t\r\n\r\nhi\r\n", true},
		{"utf8 in one of several recipients", "a@example.com", []string{"b@remote.test", "c@remote.test", "zoë@remote.test"}, "Subject: t\r\n\r\nhi\r\n", true},
		{"utf8 subject header", "a@example.com", []string{"b@remote.test"}, "Subject: caf\xc3\xa9\r\n\r\nhi\r\n", true},
		{"utf8 body only", "a@example.com", []string{"b@remote.test"}, "Subject: t\r\n\r\ncaf\xc3\xa9\r\n", false},
		{"lf only, utf8 header", "a@example.com", []string{"b@remote.test"}, "Subject: caf\xc3\xa9\n\nhi\n", true},
		{"lf only, utf8 body", "a@example.com", []string{"b@remote.test"}, "Subject: t\n\ncaf\xc3\xa9\n", false},
		{"no blank line, utf8", "a@example.com", []string{"b@remote.test"}, "Subject: caf\xc3\xa9\r\n", true},
		{"no blank line, ascii", "a@example.com", []string{"b@remote.test"}, "Subject: t\r\n", false},
		{"empty message", "a@example.com", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsSMTPUTF8(tt.from, tt.rcpt, []byte(tt.raw)); got != tt.want {
				t.Errorf("needsSMTPUTF8 = %v, want %v", got, tt.want)
			}
		})
	}
}

// startUTF8Server starts a go-smtp server that advertises SMTPUTF8 only when
// asked, recording the MAIL options it receives.
func startUTF8Server(t *testing.T, b *dsnBackend, advertise bool) net.Listener {
	t.Helper()
	srv := gosmtp.NewServer(b)
	srv.Domain = "localhost"
	srv.EnableSMTPUTF8 = advertise
	srv.MaxMessageBytes = 1 << 20
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l
}

func TestSendToHostSMTPUTF8(t *testing.T) {
	asciiHdr := "From: a@example.com\r\nSubject: t\r\n\r\nhello\r\n"
	tests := []struct {
		name      string
		advertise bool
		from      string
		rcpts     []string
		raw       string
		wantUTF8  bool
	}{
		{"advertised, utf8 recipient", true, "a@example.com", []string{"zoë@remote.test"}, asciiHdr, true},
		{"advertised, utf8 sender", true, "jörg@example.com", []string{"b@remote.test"}, asciiHdr, true},
		{"advertised, utf8 header only", true, "a@example.com", []string{"b@remote.test"}, "Subject: caf\xc3\xa9\r\n\r\nhello\r\n", true},
		{"advertised, utf8 body only", true, "a@example.com", []string{"b@remote.test"}, "Subject: t\r\n\r\ncaf\xc3\xa9\r\n", false},
		{"advertised, all ascii", true, "a@example.com", []string{"b@remote.test"}, asciiHdr, false},
		// Not advertised: send without SMTPUTF8 and let the remote decide,
		// as net/smtp did. Setting it would make go-smtp fail client-side
		// with a non-permanent error and the queue would retry for ever.
		{"not advertised, utf8 recipient", false, "a@example.com", []string{"zoë@remote.test"}, asciiHdr, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newDSNBackend()
			l := startUTF8Server(t, b, tt.advertise)
			r := smartRelay(l.Addr().String())

			perm, _, err := r.sendToHostOpts(context.Background(), l.Addr().String(), tt.from, tt.rcpts, []byte(tt.raw), deliveryTLSPolicy{mxAuthorized: true}, nil)
			if err != nil || len(perm) != 0 {
				t.Fatalf("sendToHostOpts: perm=%v err=%v", perm, err)
			}
			select {
			case <-b.data:
			case <-time.After(5 * time.Second):
				t.Fatal("DATA never arrived")
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.mailOpts == nil {
				t.Fatal("no MAIL recorded")
			}
			if b.mailOpts.UTF8 != tt.wantUTF8 {
				t.Errorf("MAIL UTF8 = %v, want %v", b.mailOpts.UTF8, tt.wantUTF8)
			}
		})
	}
}
