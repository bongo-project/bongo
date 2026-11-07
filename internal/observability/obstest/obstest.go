// Package obstest reads bongo metrics back from the registry for tests in
// other packages. Production code does not import it.
package obstest

import (
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/bongo-project/bongo/internal/observability"
)

func find(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := observability.Registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue next
				}
			}
			if len(got) != len(labels) {
				continue
			}
			return m
		}
	}
	t.Fatalf("metric %s%v not found", name, labels)
	return nil
}

// HistogramCount returns the sample count of a histogram series.
func HistogramCount(t *testing.T, name string, labels map[string]string) uint64 {
	t.Helper()
	return find(t, name, labels).GetHistogram().GetSampleCount()
}

// Gauge returns the value of a gauge series.
func Gauge(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	return find(t, name, labels).GetGauge().GetValue()
}

// Counter returns the value of a counter series.
func Counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	return find(t, name, labels).GetCounter().GetValue()
}

// Command returns the sample count of bongo_protocol_command_seconds for one
// protocol and command.
func Command(t *testing.T, protocol, command string) uint64 {
	t.Helper()
	return HistogramCount(t, "bongo_protocol_command_seconds", map[string]string{"protocol": protocol, "command": command})
}
