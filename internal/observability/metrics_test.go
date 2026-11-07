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

// TestLoadTestMetricsExported covers every unlabelled metric the load test
// reads. Unlabelled collectors are exported at zero, so no observation is needed.
func TestLoadTestMetricsExported(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	for _, tt := range []struct{ name, kind string }{
		{"bongo_user_dbs_open", "gauge"},
		{"bongo_user_db_evictions_total", "counter"},
		{"bongo_search_index_backlog", "gauge"},
		{"bongo_raft_apply_seconds", "histogram"},
		{"bongo_queue_depth", "gauge"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if want := "# TYPE " + tt.name + " " + tt.kind; !strings.Contains(s, want) {
				t.Errorf("metrics output missing %q", want)
			}
		})
	}
}
