// Package observability provides metrics, tracing, and logging primitives
// for bongo-go. All Prometheus metric definitions live here — individual
// packages import this one and call the exported variables rather than
// defining their own collectors.
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the Prometheus registry all bongo-go metrics live in.
var Registry = prometheus.NewRegistry()

var factory = promauto.With(Registry)

// SMTPMessagesTotal counts SMTP messages processed, labelled by final verdict.
//
// Verdicts:
//   - "accepted": message accepted and passed to the delivery pipeline
//   - "rejected": message rejected (no recipients, delivery error, etc.)
var SMTPMessagesTotal = factory.NewCounterVec(
	prometheus.CounterOpts{
		Name: "bongo_smtp_messages_total",
		Help: "Total number of SMTP messages processed, by final verdict.",
	},
	[]string{"verdict"},
)

// Handler returns an http.Handler that serves Prometheus metrics. Mount at /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		Registry: Registry,
	})
}
