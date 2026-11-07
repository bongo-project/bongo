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

// StorageLow is 1 while free space on the data directory is below
// storage.min_free_mb (new mail, uploads and DAV writes are being refused), else 0.
var StorageLow = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_storage_low",
		Help: "1 while free disk space is below storage.min_free_mb and new work is refused, else 0.",
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

// UserDBsOpen is the number of per-user SQLite databases currently held open
// by the metadata manager. Maintained with Inc and Dec at each open and
// removal, so it stays the sum across managers in one process. Unlabelled:
// users are unbounded.
var UserDBsOpen = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_user_dbs_open",
		Help: "Number of per-user SQLite databases currently open.",
	},
)

// UserDBEvictions counts per-user databases closed because the open-database
// limit (storage.max_open_user_dbs) was reached. Explicit closes and user
// deletion are not counted. A fast-rising value means the working set of
// active users exceeds the limit and databases are being closed and reopened.
var UserDBEvictions = factory.NewCounter(
	prometheus.CounterOpts{
		Name: "bongo_user_db_evictions_total",
		Help: "Per-user SQLite databases closed to stay under storage.max_open_user_dbs.",
	},
)

// SearchIndexBacklog is the number of derived-index commands queued behind the
// FSM indexer worker. It does not count the item being processed. When the
// queue is full indexing falls back to inline and nothing is queued, so a
// value pinned near the queue size is the saturation signal.
var SearchIndexBacklog = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_search_index_backlog",
		Help: "Derived-index commands waiting for the FSM indexer worker, excluding the one in progress.",
	},
)

// RaftApplySeconds observes the time a leader write waits in ApplyCommand, from
// submitting the entry until the FSM has applied it. It is the latency callers
// see: it includes the log append and disk sync, quorum replication on a
// cluster, and the FSM apply. Writes rejected before submission (not leader)
// are not observed.
var RaftApplySeconds = factory.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "bongo_raft_apply_seconds",
		Help:    "Time for a Raft write to commit and apply, as seen by the caller, in seconds.",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	},
)

// RaftCommitLagEntries is the number of entries in the local Raft log that
// have not yet been handed to the FSM (last log index minus applied index). On
// a single node it is nonzero only while a batch is between append and apply;
// on a cluster it also shows replication falling behind. It reads a source set
// by SetRaftIndexSource and reports 0 when none is set.
var RaftCommitLagEntries = factory.NewGaugeFunc(
	prometheus.GaugeOpts{
		Name: "bongo_raft_commit_lag_entries",
		Help: "Raft log entries appended locally but not yet applied, last index minus applied index.",
	},
	raftCommitLag,
)

// RaftFSMApplySeconds observes the time FSM.Apply itself takes for one log
// entry. Subtracting it from RaftApplySeconds leaves the log append, disk
// sync and replication time, which is the split a commit-lag investigation
// needs. Entries skipped on replay are observed too; they take microseconds.
var RaftFSMApplySeconds = factory.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "bongo_raft_fsm_apply_seconds",
		Help:    "Time the FSM spends applying one Raft log entry, in seconds.",
		Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	},
)

// PebbleWriteStalls counts write stalls: the times Pebble paused writers
// because memtables or level 0 backed up behind compaction.
var PebbleWriteStalls = factory.NewCounter(
	prometheus.CounterOpts{
		Name: "bongo_pebble_write_stalls_total",
		Help: "Times Pebble stalled writes waiting for flush or compaction.",
	},
)

// PebbleWriteStallSeconds is the total time writers spent stalled.
var PebbleWriteStallSeconds = factory.NewCounter(
	prometheus.CounterOpts{
		Name: "bongo_pebble_write_stall_seconds_total",
		Help: "Total time Pebble held writes stalled, in seconds.",
	},
)

// Handler returns an http.Handler that serves Prometheus metrics. Mount at /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		Registry: Registry,
	})
}

// VacationRepliesTotal counts vacation reply outcomes, labelled by result. It
// has no user label: users are unbounded.
//
// Results:
//   - "sent":           a reply was queued or delivered
//   - "rate_limited":   skipped because the user reached the per-hour cap
//   - "track_failed":   skipped because the tracking record could not be written
//   - "enqueue_failed": the tracking record was written but the reply could not
//     be queued or delivered, so one reply was lost
var VacationRepliesTotal = factory.NewCounterVec(
	prometheus.CounterOpts{
		Name: "bongo_vacation_replies_total",
		Help: "Vacation reply outcomes, by result.",
	},
	[]string{"result"},
)
