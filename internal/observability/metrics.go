// Package observability provides metrics, tracing, and logging primitives
// for bongo. All Prometheus metric definitions live here — individual
// packages import this one and call the exported variables rather than
// defining their own collectors.
package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the Prometheus registry all bongo metrics live in.
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

// QueueDepth is the number of messages currently pending in the outbound queue.
// Set by the queue worker each poll (leader only).
var QueueDepth = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_queue_depth",
		Help: "Number of messages currently pending in the outbound delivery queue.",
	},
)

// QueueOldestPendingSeconds is the age of the oldest pending message. A rising
// value indicates the queue is backing up (delivery stalled).
var QueueOldestPendingSeconds = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_queue_oldest_pending_seconds",
		Help: "Age in seconds of the oldest pending message in the outbound queue.",
	},
)

// QueueMessagesTotal counts outbound queue outcomes, labelled by result.
//
// Results:
//   - "delivered": relayed successfully
//   - "failed":    transient failure, retry scheduled
//   - "bounced":   permanent failure or retries exhausted
var QueueMessagesTotal = factory.NewCounterVec(
	prometheus.CounterOpts{
		Name: "bongo_queue_messages_total",
		Help: "Total outbound queue outcomes, by result.",
	},
	[]string{"result"},
)

// QueueDeliverySeconds observes the time from enqueue to successful delivery,
// covering retry backoff. Useful for spotting delivery latency regressions.
var QueueDeliverySeconds = factory.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "bongo_queue_delivery_seconds",
		Help:    "Time from enqueue to successful outbound delivery, in seconds.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 300, 900, 3600},
	},
)

// AntispamScore observes the spam score assigned to each inbound message. The
// distribution shows how mail clusters around the junk/quarantine thresholds.
var AntispamScore = factory.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "bongo_antispam_score",
		Help:    "Distribution of antispam scores assigned to inbound messages.",
		Buckets: []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	},
)

// AttachmentDedupBlobsTotal counts attachment blobs processed while splitting a
// message into a skeleton + content-addressed attachment blobs, labelled by
// result.
//
// Results:
//   - "written": the attachment blob was new and stored on disk
//   - "deduped": an identical attachment blob already existed; the write was skipped
var AttachmentDedupBlobsTotal = factory.NewCounterVec(
	prometheus.CounterOpts{
		Name: "bongo_attachment_dedup_blobs_total",
		Help: "Attachment blobs processed during message split, by result (written vs deduped).",
	},
	[]string{"result"},
)

// AttachmentDedupBytesSavedTotal counts the encoded attachment bytes that were
// NOT re-stored because an identical blob already existed. It is the running
// storage saving delivered by attachment deduplication.
var AttachmentDedupBytesSavedTotal = factory.NewCounter(
	prometheus.CounterOpts{
		Name: "bongo_attachment_dedup_bytes_saved_total",
		Help: "Total encoded attachment bytes not re-stored because an identical blob already existed.",
	},
)

// Handler returns an http.Handler that serves Prometheus metrics. Mount at /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		Registry: Registry,
	})
}
