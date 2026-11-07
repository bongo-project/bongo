package observability

import "github.com/prometheus/client_golang/prometheus"

// BlobCount is the number of blobs in the content-addressed store as listed
// by the last blob garbage collection pass. It is set on the leader only,
// once per orphan GC tick (every 15 minutes, the first 15 minutes after
// start), and only when storage.blob_gc is "dryrun" or "on"; with the default
// "off" the pass is skipped and it reads 0. It describes the listing taken at
// the start of the pass, before that pass's deletions, and keeps its last
// value when a pass fails or the node stops being leader.
var BlobCount = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_blobs",
		Help: "Blobs in the blob store at the start of the last garbage collection pass. Leader only, every 15 minutes, only when storage.blob_gc is dryrun or on; keeps its last value if a pass fails.",
	},
)

// BlobBytes is the total size of those blobs, in bytes. Same update rules as
// BlobCount.
var BlobBytes = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_blob_bytes",
		Help: "Total bytes of the blobs counted by bongo_blobs. Same update rules: leader only, every 15 minutes, only when storage.blob_gc is dryrun or on.",
	},
)

// BlobGCPassSeconds observes the duration of a whole blob garbage collection
// pass: mark, listing, stat, re-validation and deletion. Only passes that
// reached the sweep are observed.
var BlobGCPassSeconds = factory.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "bongo_blob_gc_pass_seconds",
		Help:    "Duration of a blob garbage collection pass (mark, list, stat, delete), in seconds. Only observed when storage.blob_gc is dryrun or on.",
		Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 300, 900, 1800, 3600},
	},
)

// BlobGCDeleted counts blobs deleted by garbage collection. In dryrun mode
// nothing is deleted and it does not move.
var BlobGCDeleted = factory.NewCounter(
	prometheus.CounterOpts{
		Name: "bongo_blob_gc_deleted_total",
		Help: "Blobs deleted by garbage collection. Does not move in dryrun mode.",
	},
)

// BlobGCReferenced is the size of the reference set built by the mark phase of
// the last pass. Same update rules as BlobCount.
var BlobGCReferenced = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_blob_gc_referenced_blobs",
		Help: "Distinct blobs referenced by mail and the queue in the last garbage collection pass (mark phase). Same update rules as bongo_blobs.",
	},
)

// The two phases of a Raft snapshot. The checkpoint runs on the FSM goroutine
// and blocks applies, so it is the phase that shows up in write latency;
// persist streams the checkpoint to the snapshot sink in the background.
var (
	raftSnapshotSeconds = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "bongo_raft_snapshot_seconds",
			Help:    "Duration of a Raft snapshot phase in seconds: checkpoint (blocks FSM applies) or persist (writes the snapshot).",
			Buckets: []float64{0.001, 0.005, 0.025, 0.1, 0.5, 1, 5, 15, 60, 300, 900},
		},
		[]string{"phase"},
	)
	RaftSnapshotCheckpoint = CommandTimer{raftSnapshotSeconds.WithLabelValues("checkpoint")}
	RaftSnapshotPersist    = CommandTimer{raftSnapshotSeconds.WithLabelValues("persist")}
)

// RaftSnapshotBytes is the size of the last successfully persisted snapshot,
// in bytes (the tar stream written to the sink).
var RaftSnapshotBytes = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_raft_snapshot_bytes",
		Help: "Size in bytes of the last successfully persisted Raft snapshot.",
	},
)

// SearchIndexesOpen is the number of per-user Bleve indexes currently held
// open. Maintained with Inc and Dec at each open and removal. The shared
// global address index is not counted.
var SearchIndexesOpen = factory.NewGauge(
	prometheus.GaugeOpts{
		Name: "bongo_search_indexes_open",
		Help: "Number of per-user search indexes currently open.",
	},
)

// SearchIndexEvictions counts per-user indexes closed to stay under the
// open-index limit. Explicit closes and user deletion are not counted.
var SearchIndexEvictions = factory.NewCounter(
	prometheus.CounterOpts{
		Name: "bongo_search_index_evictions_total",
		Help: "Per-user search indexes closed because the open-index limit was reached.",
	},
)
