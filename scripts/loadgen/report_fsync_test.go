package main

import (
	"strings"
	"testing"
)

// fsyncServer runs analyseFsync over two scrapes, the second holding the given
// cumulative bucket counts for 100 observations.
func fsyncServer(counts [3]uint64) *serverSection {
	var s serverSection
	les := [3]float64{0.001, 0.005, 5}
	a := &fsyncSample{Buckets: []fsyncBucket{{les[0], 0}, {les[1], 0}, {les[2], 0}}}
	b := &fsyncSample{Count: 100, Buckets: []fsyncBucket{{les[0], counts[0]}, {les[1], counts[1]}, {les[2], counts[2]}}}
	analyseFsync([]sampleRecord{{OK: true, Fsync: a}, {OK: true, Fsync: b}}, &s)
	return &s
}

func TestFsyncRankInInfBucketIsALowerBound(t *testing.T) {
	t.Run("all beyond the last bucket", func(t *testing.T) {
		s := fsyncServer([3]uint64{0, 0, 0})
		if s.FsyncP50Ms != nil || s.FsyncP95Ms != nil || s.FsyncP99Ms != nil || s.FsyncMaxIntP99Ms != nil {
			t.Errorf("an upper bound was reported for observations beyond the last bucket: %+v", s)
		}
		if s.FsyncP99OverMs == nil || *s.FsyncP99OverMs != 5000 || s.FsyncP50OverMs == nil || s.FsyncMaxIntP99OverMs == nil {
			t.Errorf("no lower bound: %+v", s)
		}
		if md := renderMarkdown(&reportDoc{Server: s}); !strings.Contains(md, ">5000") {
			t.Errorf("markdown lacks >5000:\n%s", md)
		}
	})
	t.Run("p50 finite while p99 is beyond", func(t *testing.T) {
		s := fsyncServer([3]uint64{60, 90, 90})
		if s.FsyncP50Ms == nil || *s.FsyncP50Ms != 1 || s.FsyncP50OverMs != nil {
			t.Errorf("p50 %v over %v", s.FsyncP50Ms, s.FsyncP50OverMs)
		}
		if s.FsyncP99Ms != nil || s.FsyncP99OverMs == nil || *s.FsyncP99OverMs != 5000 {
			t.Errorf("p99 %v over %v", s.FsyncP99Ms, s.FsyncP99OverMs)
		}
		if s.FsyncMaxIntP99Ms != nil || s.FsyncMaxIntP99OverMs == nil {
			t.Errorf("worst interval is not marked as a lower bound: %+v", s)
		}
	})
	t.Run("all inside", func(t *testing.T) {
		s := fsyncServer([3]uint64{100, 100, 100})
		if s.FsyncP99Ms == nil || *s.FsyncP99Ms != 1 || s.FsyncP99OverMs != nil || s.FsyncMaxIntP99OverMs != nil {
			t.Errorf("%+v", s)
		}
		if md := renderMarkdown(&reportDoc{Server: s}); strings.Contains(md, ">1 ") || strings.Contains(md, ">1/") {
			t.Errorf("a finite figure rendered as a lower bound:\n%s", md)
		}
	})
}
