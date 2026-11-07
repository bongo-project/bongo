package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fillAll sets every settable field of v: pointers non-nil, numbers 1, maps
// with one entry. It is how the completeness test learns of a field added to
// reportDoc without anyone classifying it.
func fillAll(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fillAll(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() || v.Type().Field(i).Anonymous {
				fillAll(v.Field(i))
			}
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		e := reflect.New(v.Type().Elem()).Elem()
		fillAll(e)
		v.SetMapIndex(reflect.ValueOf("op"), e)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	}
}

func TestEveryReportKeyIsClassified(t *testing.T) {
	var d reportDoc
	fillAll(reflect.ValueOf(&d).Elem())
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	flat, err := flattenMetrics(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) < 40 {
		t.Fatalf("only %d keys: the fixture is not fully populated", len(flat))
	}
	for k := range flat {
		if _, known := classifyMetric(k); !known {
			t.Errorf("report key %q is not in metricClasses", k)
		}
	}
	// The table must not name a key no report holds.
	for _, c := range metricClasses {
		found := false
		for k := range flat {
			if patternMatches(c.pattern, k) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("metricClasses entry %q matches no report key", c.pattern)
		}
	}
}

// compareDoc is a report with fixed measured results. scale multiplies every
// bookkeeping figure; p99 is the SMTP latency p99.
func compareDoc(scale, p99, errs float64) map[string]any {
	return map[string]any{
		"generated": "2026-01-01T00:00:00Z",
		"meta":      map[string]any{"machine_type": "4 vCPU"},
		"throughput": map[string]any{"accepted_messages": 1000 * scale, "elapsed_seconds": 10 * scale,
			"accepted_per_second": 100.0, "sink_messages": 998 * scale},
		"smtp_latency": map[string]any{"count": 1000 * scale, "p50_ms": 5.0, "p95_ms": 9.0, "p99_ms": p99, "max_ms": 20 * scale},
		"errors": map[string]any{"rejected_5xx": errs, "deferred_4xx": 0.0, "timed_out": 0.0, "conn_errors": 0.0,
			"other_errors": 0.0, "unsent": 0.0},
		"delivery_delay": map[string]any{"count": 100 * scale, "sent": 100 * scale, "seen": 99 * scale, "seen_by_noop": 7 * scale,
			"unresolved": 3 * scale, "visible_before_accept": 1 * scale, "lost": 0.0, "p50_ms": 100.0, "p95_ms": 200.0, "p99_ms": 300.0},
		"search_lag": map[string]any{"count": 90 * scale, "found": 90 * scale, "not_found": 0.0, "resolution_ms": 5000 * scale,
			"p50_ms": 300.0, "p95_ms": 400.0, "p99_ms": 500.0},
		"clients": map[string]any{"ops": map[string]any{"login": map[string]any{"count": 50 * scale, "errors": 0.0,
			"p50_ms": 4.0, "p95_ms": 16.0, "p99_ms": 32.0}}},
		"server": map[string]any{"samples": 100 * scale, "failed_scrapes": 2 * scale, "max_gap_seconds": 3 * scale,
			"restarts_seen": scale - 1, "wal_fsync_count": 5000 * scale, "cpu_cores_avg": 0.5, "cpu_cores_max": 0.9,
			"resident_memory_bytes": map[string]any{"avg": 1000.0, "max": 2000.0},
			"samples_file":          map[string]any{"bytes": 4000 * scale}, "raw_scrapes_file": map[string]any{"bytes": 9000 * scale}},
	}
}

func TestReportCompareThroughRun(t *testing.T) {
	dir := t.TempDir()
	n := 0
	write := func(doc map[string]any) string {
		n++
		p := filepath.Join(dir, "r"+strconv.Itoa(n)+".json")
		data, _ := json.Marshal(doc)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	exit := func(a, b string) (int, string) {
		var so, se bytes.Buffer
		code := run([]string{"report-compare", "--target-allow", "127.0.0.1", "--test-domain", "x.test",
			"--report-compare-baseline", a, "--report-compare-candidate", b}, &so, &se)
		return code, so.String() + se.String()
	}
	sub := func(doc map[string]any, section string) map[string]any { return doc[section].(map[string]any) }

	base := compareDoc(1, 50, 0)

	t.Run("bookkeeping only", func(t *testing.T) {
		other := compareDoc(2.5, 50, 0)
		// The fixture must change bookkeeping by far more than the threshold,
		// or this test would pass without exercising the filter.
		rb, _ := json.Marshal(base)
		ro, _ := json.Marshal(other)
		fb, _ := flattenMetrics(rb)
		fo, _ := flattenMetrics(ro)
		changed := 0
		for k, v := range fb {
			if kind, _ := classifyMetric(k); kind == kindBookkeeping && fo[k] != v {
				changed++
			}
		}
		if changed < 10 {
			t.Fatalf("only %d bookkeeping figures differ in the fixture", changed)
		}
		if code, out := exit(write(base), write(other)); code != exitOK {
			t.Errorf("exit %d, want 0:\n%s", code, out)
		}
	})
	t.Run("p99 11 percent up", func(t *testing.T) {
		if code, out := exit(write(base), write(compareDoc(1, 55.5, 0))); code != exitCompareDiffers || !strings.Contains(out, "smtp_latency.p99_ms") {
			t.Errorf("exit %d, want 5:\n%s", code, out)
		}
	})
	t.Run("p99 9 percent up", func(t *testing.T) {
		if code, out := exit(write(base), write(compareDoc(1, 54.5, 0))); code != exitOK {
			t.Errorf("exit %d, want 0:\n%s", code, out)
		}
	})
	t.Run("max_ms alone does not gate", func(t *testing.T) {
		other := compareDoc(1, 50, 0)
		sub(other, "smtp_latency")["max_ms"] = 5000.0
		if code, out := exit(write(base), write(other)); code != exitOK || !strings.Contains(out, "not gated") {
			t.Errorf("exit %d, want 0:\n%s", code, out)
		}
	})
	t.Run("error rates", func(t *testing.T) {
		// 3 against 4 in a million attempts agree; both are listed.
		a, b := compareDoc(1, 50, 3), compareDoc(1, 50, 4)
		sub(a, "throughput")["accepted_messages"] = 1e6
		sub(b, "throughput")["accepted_messages"] = 1e6
		code, out := exit(write(a), write(b))
		if code != exitOK || !strings.Contains(out, "errors.rejected_5xx") {
			t.Errorf("3 against 4 in 1e6: exit %d:\n%s", code, out)
		}
		// No errors against 11% of attempts differs.
		c, d := compareDoc(1, 50, 0), compareDoc(1, 50, 11)
		sub(c, "throughput")["accepted_messages"] = 89.0
		sub(d, "throughput")["accepted_messages"] = 89.0
		if code, out := exit(write(c), write(d)); code != exitCompareDiffers {
			t.Errorf("0%% against 11%%: exit %d:\n%s", code, out)
		}
	})
	t.Run("measured result missing", func(t *testing.T) {
		other := compareDoc(1, 50, 0)
		delete(sub(other, "smtp_latency"), "p99_ms")
		if code, out := exit(write(base), write(other)); code != exitCompareDiffers {
			t.Errorf("exit %d:\n%s", code, out)
		}
	})
	t.Run("bookkeeping missing on one side", func(t *testing.T) {
		other := compareDoc(1, 50, 0)
		delete(sub(other, "server"), "samples_file")
		if code, out := exit(write(base), write(other)); code != exitOK {
			t.Errorf("exit %d:\n%s", code, out)
		}
	})
}
