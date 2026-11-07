package observability

import "sync/atomic"

// raftIndexSource reports a Raft node's last log index and applied index.
type raftIndexSource func() (last, applied uint64)

var raftIndexes atomic.Pointer[raftIndexSource]

// SetRaftIndexSource points the commit lag gauge at a Raft node. The returned
// function unhooks it, and only if it is still the current source, so closing
// an older node never unhooks a newer one.
func SetRaftIndexSource(fn func() (last, applied uint64)) (clear func()) {
	src := raftIndexSource(fn)
	raftIndexes.Store(&src)
	return func() { raftIndexes.CompareAndSwap(&src, nil) }
}

func raftCommitLag() float64 {
	p := raftIndexes.Load()
	if p == nil {
		return 0
	}
	last, applied := (*p)()
	if applied >= last {
		return 0
	}
	return float64(last - applied)
}
