package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func commandSamples(t *testing.T, protocol, command string) uint64 {
	t.Helper()
	var m dto.Metric
	obs := protocolCommandSeconds.WithLabelValues(protocol, command)
	if err := obs.(prometheus.Metric).Write(&m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

// TestCommandLabelsAreBounded checks that client-chosen command names and
// methods collapse into "other" and known ones keep their own series.
func TestCommandLabelsAreBounded(t *testing.T) {
	for _, tt := range []struct {
		name     string
		protocol string
		label    string
		observe  func()
	}{
		{"pop3 unknown", ProtocolPOP3, "other", func() { POP3Command("XYZZY").Since(time.Now()) }},
		{"pop3 known", ProtocolPOP3, "STAT", func() { POP3Command("STAT").Since(time.Now()) }},
		{"caldav unknown method", ProtocolCalDAV, "other", func() {
			DAVRequest(ProtocolCalDAV, "BREW-COFFEE").Since(time.Now())
			DAVDone(ProtocolCalDAV)
		}},
		{"carddav lower case is unknown", ProtocolCardDAV, "other", func() {
			DAVRequest(ProtocolCardDAV, "propfind").Since(time.Now())
			DAVDone(ProtocolCardDAV)
		}},
		{"carddav known", ProtocolCardDAV, "PROPFIND", func() {
			DAVRequest(ProtocolCardDAV, "PROPFIND").Since(time.Now())
			DAVDone(ProtocolCardDAV)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := commandSamples(t, tt.protocol, tt.label)
			tt.observe()
			if got := commandSamples(t, tt.protocol, tt.label); got != before+1 {
				t.Fatalf("%s/%s samples = %d, want %d", tt.protocol, tt.label, got, before+1)
			}
		})
	}
}
