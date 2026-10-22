package smtp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	gosmtp "github.com/emersion/go-smtp"
)

type dataLogRecorder struct {
	mu   *sync.Mutex
	recs *[]string
}

func (h *dataLogRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *dataLogRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *dataLogRecorder) WithGroup(string) slog.Handler            { return h }
func (h *dataLogRecorder) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + fmt.Sprint(a.Value.Resolve().Any()))
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, b.String())
	h.mu.Unlock()
	return nil
}

func newDataSession(t *testing.T, rcpt string) (*Session, *fakeSplitDeliverer, *fakeRelayQueue, func() []string) {
	t.Helper()
	b, d, q := testBackend("staff@example.com", "alice@example.com")
	var recs []string
	mu := &sync.Mutex{}
	b.logger = slog.New(&dataLogRecorder{mu: mu, recs: &recs})
	s := &Session{backend: b, logger: b.logger, submission: true, authed: true, userID: "uid-alice@example.com"}
	if err := s.Mail("alice@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := s.Rcpt(rcpt, nil); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	return s, d, q, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), recs...) }
}

func TestDataInternalErrorIsGeneric451(t *testing.T) {
	secret := errors.New("pebble: secret/path/detail")
	tests := []struct {
		name  string
		rcpt  string
		setup func(*Backend, *fakeSplitDeliverer, *fakeRelayQueue)
	}{
		{"store blob", "staff@example.com", func(_ *Backend, d *fakeSplitDeliverer, _ *fakeRelayQueue) { d.storeErr = secret }},
		{"enqueue local", "staff@example.com", func(_ *Backend, d *fakeSplitDeliverer, _ *fakeRelayQueue) { d.enqueueErr = secret }},
		{"enqueue relay", "outsider@example.net", func(_ *Backend, _ *fakeSplitDeliverer, q *fakeRelayQueue) { q.enqueueErr = secret }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, d, q, logs := newDataSession(t, tt.rcpt)
			tt.setup(s.backend, d, q)

			err := s.Data(strings.NewReader(authedMsg))
			var se *gosmtp.SMTPError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want *SMTPError", err)
			}
			if se.Code != 451 || se.EnhancedCode != (gosmtp.EnhancedCode{4, 3, 0}) {
				t.Errorf("status = %d %v, want 451 4.3.0", se.Code, se.EnhancedCode)
			}
			if strings.Contains(se.Message, "pebble") || strings.Contains(se.Message, "secret") {
				t.Errorf("message %q leaks internal detail", se.Message)
			}
			logged := false
			for _, l := range logs() {
				if strings.HasPrefix(l, "ERROR") && strings.Contains(l, "secret/path/detail") {
					logged = true
				}
				if strings.Contains(l, "Subject") || strings.Contains(l, "hi\r\n") {
					t.Errorf("log carries message content: %s", l)
				}
			}
			if !logged {
				t.Errorf("detail not logged at ERROR; logs: %v", logs())
			}
		})
	}
}

func TestDataPermanentSMTPErrorsStayPermanent(t *testing.T) {
	s, d, _, _ := newDataSession(t, "staff@example.com")
	d.storeErr = fmt.Errorf("reading: %w", gosmtp.ErrDataTooLarge)
	err := s.Data(strings.NewReader(authedMsg))
	if smtpCode(err) != 552 {
		t.Errorf("wrapped ErrDataTooLarge -> %v, want 552", err)
	}

	// Policy rejection from the submission header check stays 550.
	s2, _, _, _ := newDataSession(t, "staff@example.com")
	err = s2.Data(strings.NewReader("From: mallory@evil.example\r\n\r\nbody"))
	if smtpCode(err) != 550 {
		t.Errorf("foreign From -> %v, want 550", err)
	}
}

func TestDataNoRecipientsIs554(t *testing.T) {
	b, _, _ := testBackend()
	s := &Session{backend: b, logger: b.logger}
	err := s.Data(io.NopCloser(strings.NewReader(authedMsg)))
	var se *gosmtp.SMTPError
	if !errors.As(err, &se) || se.Code != 554 || se.EnhancedCode != (gosmtp.EnhancedCode{5, 5, 1}) {
		t.Errorf("err = %v, want 554 5.5.1", err)
	}
}

func TestDataDoesNotLogSubjectAtInfo(t *testing.T) {
	const subject = "S-7f3a-secret"
	for _, rcpt := range []string{"staff@example.com", "outsider@example.net"} {
		s, _, _, logs := newDataSession(t, rcpt)
		msg := "From: alice@example.com\r\nSubject: " + subject + "\r\n\r\nbody"
		if err := s.Data(strings.NewReader(msg)); err != nil {
			t.Fatalf("Data(%s): %v", rcpt, err)
		}
		recs := logs()
		accepted := false
		for _, l := range recs {
			if strings.Contains(l, "message accepted") {
				accepted = true
			}
			if !strings.HasPrefix(l, "DEBUG") && strings.Contains(l, subject) {
				t.Errorf("log contains the subject: %s", l)
			}
		}
		if !accepted {
			t.Errorf("no acceptance record for %s; test would pass vacuously: %v", rcpt, recs)
		}
	}
}

type plainFailDeliverer struct{ err error }

func (d plainFailDeliverer) Deliver(string, []string, io.Reader) error { return d.err }

type spfFailDeliverer struct{ err error }

func (d spfFailDeliverer) Deliver(string, []string, io.Reader) error { return d.err }
func (d spfFailDeliverer) DeliverWithSPF(string, []string, io.Reader, *InboundMeta) error {
	return d.err
}

// The non-split path (agent and edge roles) must not echo store detail either.
func TestDataFallbackDeliveryErrorIsGeneric451(t *testing.T) {
	secret := errors.New("pebble: secret/path/detail")
	tests := []struct {
		name string
		d    Deliverer
	}{
		{"plain deliverer", plainFailDeliverer{secret}},
		{"spf deliverer", spfFailDeliverer{secret}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _, logs := newDataSession(t, "staff@example.com")
			s.backend.deliverer = tt.d

			err := s.Data(strings.NewReader(authedMsg))
			assertGeneric451(t, err, logs)
		})
	}
}

type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, errors.New("pebble: secret/path/detail")
	}
	r.n--
	return copy(p, "X-Partial: 1\r\n"), nil
}

func TestDataSubmissionHeaderReadErrorIsGeneric451(t *testing.T) {
	s, _, _, logs := newDataSession(t, "staff@example.com")
	err := s.Data(&failingReader{n: 1})
	assertGeneric451(t, err, logs)
}

func assertGeneric451(t *testing.T, err error, logs func() []string) {
	t.Helper()
	var se *gosmtp.SMTPError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *SMTPError", err)
	}
	if se.Code != 451 || se.EnhancedCode != (gosmtp.EnhancedCode{4, 3, 0}) {
		t.Errorf("status = %d %v, want 451 4.3.0", se.Code, se.EnhancedCode)
	}
	if strings.Contains(se.Message, "pebble") || strings.Contains(se.Message, "secret") {
		t.Errorf("message %q leaks internal detail", se.Message)
	}
	for _, l := range logs() {
		if strings.HasPrefix(l, "ERROR") && strings.Contains(l, "secret/path/detail") {
			return
		}
	}
	t.Errorf("detail not logged at ERROR; logs: %v", logs())
}
