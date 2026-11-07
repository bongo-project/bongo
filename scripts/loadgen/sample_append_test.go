package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

const goodSampleLine = `{"time":"2026-01-01T00:00:00Z","ok":true,"duration_ms":1}` + "\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSampleRefusesAFileThatHoldsData(t *testing.T) {
	srv, hits := fakeMetrics(t, nil)
	for _, which := range []string{"out", "raw"} {
		t.Run(which, func(t *testing.T) {
			cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
			o := &cfg.Report.Sample
			path := o.Out
			other := o.Raw
			if which == "raw" {
				path, other = o.Raw, o.Out
			}
			const old = "earlier run\n"
			writeFile(t, path, old)
			before := hits.Load()

			var so, se bytes.Buffer
			if code := runSample(cfg, &so, &se); code != exitRefuse {
				t.Fatalf("exit %d, want %d; stderr %s", code, exitRefuse, se.String())
			}
			if got, _ := os.ReadFile(path); string(got) != old {
				t.Errorf("the refused file was changed: %q", got)
			}
			if _, err := os.Stat(other); err == nil {
				t.Errorf("the other file was created although the run was refused")
			}
			if hits.Load() != before {
				t.Error("a request was made before the refusal")
			}
			if !strings.Contains(se.String(), "--sample-"+which) || strings.Contains(se.String(), old) {
				t.Errorf("the refusal must name the flag and not the content: %q", se.String())
			}
		})
	}
}

func TestSampleAcceptsAnEmptyFile(t *testing.T) {
	srv, _ := fakeMetrics(t, nil)
	cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
	writeFile(t, cfg.Report.Sample.Out, "")
	var so, se bytes.Buffer
	if code := runSample(cfg, &so, &se); code != exitOK {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	if recs, err := readSamples(cfg.Report.Sample.Out); err != nil || len(recs) == 0 {
		t.Errorf("%d records, err %v", len(recs), err)
	}
}

func TestSampleAppendKeepsOldLinesAndRepairsATornTail(t *testing.T) {
	srv, _ := fakeMetrics(t, nil)
	t.Run("whole lines", func(t *testing.T) {
		cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
		cfg.Report.Sample.Append = true
		writeFile(t, cfg.Report.Sample.Out, goodSampleLine+goodSampleLine)
		writeFile(t, cfg.Report.Sample.Raw, "old raw\n")
		var so, se bytes.Buffer
		if code := runSample(cfg, &so, &se); code != exitOK {
			t.Fatalf("exit %d: %s", code, se.String())
		}
		got, _ := os.ReadFile(cfg.Report.Sample.Out)
		if !strings.HasPrefix(string(got), goodSampleLine+goodSampleLine) || len(got) <= 2*len(goodSampleLine) {
			t.Errorf("old lines lost or nothing added: %q", got)
		}
		if raw, _ := os.ReadFile(cfg.Report.Sample.Raw); !strings.HasPrefix(string(raw), "old raw\n") || len(raw) <= len("old raw\n") {
			t.Errorf("raw file not appended: %q", raw)
		}
		if strings.Contains(se.String(), "torn") {
			t.Errorf("a whole file was reported torn: %q", se.String())
		}
	})
	t.Run("torn tail", func(t *testing.T) {
		cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
		cfg.Report.Sample.Append = true
		writeFile(t, cfg.Report.Sample.Out, goodSampleLine+`{"time":"2026-01-01T00:0`)
		var so, se bytes.Buffer
		if code := runSample(cfg, &so, &se); code != exitOK {
			t.Fatalf("exit %d: %s", code, se.String())
		}
		if !strings.Contains(se.String(), "torn line") {
			t.Errorf("the repair was not announced: %q", se.String())
		}
		recs, err := readSamples(cfg.Report.Sample.Out)
		if err != nil || len(recs) < 2 {
			t.Fatalf("%d records, err %v: the report cannot read the appended file", len(recs), err)
		}
	})
}

// TestOpenSampleFileRefusesFromTheOpenFile: the pre-check can be raced by a
// file appearing after it, so the open itself must refuse too, and must leave
// the file as it was.
func TestOpenSampleFileRefusesFromTheOpenFile(t *testing.T) {
	p := t.TempDir() + "/f"
	writeFile(t, p, "data\n")
	if f, _, err := openSampleFile(p, false); err == nil || f != nil {
		t.Fatal("a file holding data was opened without append")
	}
	if got, _ := os.ReadFile(p); string(got) != "data\n" {
		t.Errorf("file changed: %q", got)
	}
	f, size, err := openSampleFile(p, true)
	if err != nil || size != 5 {
		t.Fatalf("append open: size %d err %v", size, err)
	}
	_ = f.Close()
}

func TestTrimTornLine(t *testing.T) {
	long := strings.Repeat("x", 200<<10)
	tests := []struct {
		name, in, want string
		dropped        int64
	}{
		{"empty", "", "", 0},
		{"whole", "a\nb\n", "a\nb\n", 0},
		{"torn", "a\nb\nfrag", "a\nb\n", 4},
		{"only a fragment", "frag", "", 4},
		{"fragment longer than a chunk", "a\n" + long, "a\n", int64(len(long))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := t.TempDir() + "/f"
			writeFile(t, p, tt.in)
			f, size, err := openSampleFile(p, true)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			n, err := trimTornLine(f, size)
			if err != nil || n != tt.dropped {
				t.Fatalf("dropped %d, err %v, want %d", n, err, tt.dropped)
			}
			if got, _ := os.ReadFile(p); string(got) != tt.want {
				t.Errorf("file %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSampleRawCapCountsWhatTheFileHolds(t *testing.T) {
	srv, _ := fakeMetrics(t, nil)
	cfg := sampleConfig(t, srv.URL, 40*time.Millisecond, 100*time.Millisecond)
	cfg.Report.Sample.Append = true
	cfg.Report.Sample.RawMaxMB = 1
	before := strings.Repeat("r", 1<<20)
	writeFile(t, cfg.Report.Sample.Raw, before)
	var so, se bytes.Buffer
	if code := runSample(cfg, &so, &se); code != exitOK {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	if raw, _ := os.ReadFile(cfg.Report.Sample.Raw); len(raw) != len(before) {
		t.Errorf("raw grew from %d to %d bytes past a full cap", len(before), len(raw))
	}
}
