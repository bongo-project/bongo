package observability

import (
	"sync/atomic"

	"github.com/cockroachdb/pebble"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// PebbleMetricsSource returns the current Pebble metrics, or nil when the
// database is closed or absent.
type PebbleMetricsSource func() *pebble.Metrics

var pebbleSource atomic.Pointer[PebbleMetricsSource]

// SetPebbleMetricsSource points the bongo_pebble_* collector at a database.
// The returned function unhooks it, and only if it is still current.
func SetPebbleMetricsSource(fn PebbleMetricsSource) (clear func()) {
	src := fn
	pebbleSource.Store(&src)
	return func() { pebbleSource.CompareAndSwap(&src, nil) }
}

// fsyncTargets are the bounds, in seconds, the WAL fsync histogram is cut down
// to. Pebble keeps about 100 nanosecond buckets; each target snaps to the
// first real Pebble bound at or above it, because cumulative counts are only
// exact at Pebble's own bounds.
var fsyncTargets = []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.5, 1, 5}

type pebbleCollector struct {
	compactions *prometheus.Desc
	compactSecs *prometheus.Desc
	compactByte *prometheus.Desc
	debt        *prometheus.Desc
	inProgress  *prometheus.Desc
	sublevels   *prometheus.Desc
	fsync       *prometheus.Desc
}

func newPebbleCollector() *pebbleCollector {
	d := func(name, help string) *prometheus.Desc { return prometheus.NewDesc(name, help, nil, nil) }
	return &pebbleCollector{
		compactions: d("bongo_pebble_compactions_total", "Pebble compactions started since the database opened."),
		compactSecs: d("bongo_pebble_compaction_seconds_total", "Cumulative Pebble compaction time, including compactions still running, in seconds."),
		compactByte: d("bongo_pebble_compacted_bytes_total", "Bytes written by Pebble compactions."),
		debt:        d("bongo_pebble_compaction_debt_bytes", "Estimated bytes Pebble must compact for the LSM to settle."),
		inProgress:  d("bongo_pebble_compactions_in_progress", "Pebble compactions running now."),
		sublevels:   d("bongo_pebble_l0_sublevels", "Level 0 sublevels, the leading indicator of a write stall."),
		fsync:       d("bongo_pebble_wal_fsync_seconds", "Pebble WAL fsync latency in seconds. The Raft log shares this WAL."),
	}
}

func (c *pebbleCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.compactions, c.compactSecs, c.compactByte, c.debt, c.inProgress, c.sublevels, c.fsync} {
		ch <- d
	}
}

// Collect emits zeros when no database is registered so the names exist from
// the first scrape and a dashboard never sees a series appear late.
func (c *pebbleCollector) Collect(ch chan<- prometheus.Metric) {
	var m *pebble.Metrics
	if p := pebbleSource.Load(); p != nil && *p != nil {
		m = (*p)()
	}
	if m == nil {
		m = &pebble.Metrics{}
	}
	ch <- prometheus.MustNewConstMetric(c.compactions, prometheus.CounterValue, float64(m.Compact.Count))
	ch <- prometheus.MustNewConstMetric(c.compactSecs, prometheus.CounterValue, m.Compact.Duration.Seconds())
	ch <- prometheus.MustNewConstMetric(c.compactByte, prometheus.CounterValue, float64(m.Total().BytesCompacted))
	ch <- prometheus.MustNewConstMetric(c.debt, prometheus.GaugeValue, float64(m.Compact.EstimatedDebt))
	ch <- prometheus.MustNewConstMetric(c.inProgress, prometheus.GaugeValue, float64(m.Compact.NumInProgress))
	ch <- prometheus.MustNewConstMetric(c.sublevels, prometheus.GaugeValue, float64(m.Levels[0].Sublevels))

	count, sum, buckets := fsyncHistogram(m.LogWriter.FsyncLatency)
	ch <- prometheus.MustNewConstHistogram(c.fsync, count, sum, buckets)
}

// fsyncHistogram converts Pebble's nanosecond histogram to seconds and cuts it
// to fsyncTargets. A nil histogram yields an empty one.
func fsyncHistogram(h prometheus.Histogram) (count uint64, sum float64, buckets map[float64]uint64) {
	buckets = map[float64]uint64{}
	if h == nil {
		return 0, 0, buckets
	}
	var out dto.Metric
	if err := h.Write(&out); err != nil || out.GetHistogram() == nil {
		return 0, 0, buckets
	}
	hist := out.GetHistogram()
	const nsPerSecond = 1e9
	for _, target := range fsyncTargets {
		for _, b := range hist.GetBucket() {
			if sec := b.GetUpperBound() / nsPerSecond; sec >= target {
				buckets[sec] = b.GetCumulativeCount()
				break
			}
		}
	}
	return hist.GetSampleCount(), hist.GetSampleSum() / nsPerSecond, buckets
}

func init() {
	Registry.MustRegister(newPebbleCollector())
}
