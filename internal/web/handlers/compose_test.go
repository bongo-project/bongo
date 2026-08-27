package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeRecipient(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare address", "user@example.com", "user@example.com"},
		{"display name with angle brackets", "Test <user@example.com>", "user@example.com"},
		{"angle brackets only", "<user@example.com>", "user@example.com"},
		{"surrounding whitespace", "  admin@example.com  ", "admin@example.com"},
		{"quoted display name", "\"Test User\" <user@example.com>", "user@example.com"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"unparseable falls back to trimmed input", "not-an-address", "not-an-address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRecipient(tt.in); got != tt.want {
				t.Errorf("normalizeRecipient(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestComposeSendRejectsOversizedBody(t *testing.T) {
	h := NewComposeHandler(nil, nil, nil, nil, nil, discardLogger())
	req := httptest.NewRequest(http.MethodPost, "/compose/send", io.LimitReader(zeroReader{}, composeMultipartMaxBytes+1))
	req = withUserContext(req, "user-oversized-compose", "user@example.com")
	rec := httptest.NewRecorder()

	h.Send(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
