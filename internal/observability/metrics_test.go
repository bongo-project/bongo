package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesRegisteredMetrics(t *testing.T) {
	SMTPMessagesTotal.WithLabelValues("accepted").Inc()
	SMTPMessagesTotal.WithLabelValues("rejected").Inc()
	SMTPMessagesTotal.WithLabelValues("rejected").Inc()

	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	for _, want := range []string{
		`bongo_smtp_messages_total{verdict="accepted"} 1`,
		`bongo_smtp_messages_total{verdict="rejected"} 2`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
