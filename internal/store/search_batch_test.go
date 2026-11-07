package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/blevesearch/bleve/v2"
)

// mailOp is one step the FSM would take against the search side.
type mailOp struct {
	del                 bool
	user, guid          string
	subject, from, body string
	to, tags            []string
	date                int64
	attach              bool
	spam                float64
}

// batchOps builds an interleaved, multi-user sequence with a re-indexed guid
// and a delete, so apply order matters.
func batchOps(n int) []mailOp {
	users := []string{"u1", "u2", "u3"}
	froms := []string{"alice@example.com", "bob@example.com", "carol@example.org"}
	words := []string{"zebra", "quarterly report", "invoice overdue", "holiday plans", "lunch"}
	var ops []mailOp
	for i := range n {
		w := words[i%len(words)]
		op := mailOp{
			user:    users[i%len(users)],
			guid:    fmt.Sprintf("g%03d", i),
			subject: fmt.Sprintf("%s number %d", w, i),
			from:    froms[i%4%len(froms)],
			body:    fmt.Sprintf("body text about %s and item%d", w, i),
			to:      []string{"me@example.com", froms[(i/3+1)%len(froms)]},
			date:    1700000000 + int64(i)*10,
			attach:  i%4 < 2,
			spam:    float64(i%5) * 1.5,
		}
		switch i % 5 {
		case 0, 1:
			op.tags = []string{"invoice", "work"}
		case 2:
			op.tags = []string{"personal"}
		}
		ops = append(ops, op)
		if i == 20 {
			// Same guid again with different content: the last write wins.
			re := op
			re.subject, re.body, re.tags = "rewritten walrus subject", "rewritten walrus body", []string{"rewritten"}
			ops = append(ops, re)
		}
		if i == 30 {
			// g017 belongs to u3, the user that also holds the re-indexed guid.
			ops = append(ops, mailOp{del: true, user: "u3", guid: "g017"})
		}
	}
	return ops
}

func applyDirect(sm *SearchManager, ops []mailOp) error {
	for _, o := range ops {
		var err error
		if o.del {
			err = sm.DeleteMailIndex(o.user, o.guid)
		} else {
			err = sm.IndexMail(o.user, o.guid, o.subject, o.from, o.to, o.body, o.date, o.attach, o.tags, o.spam)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// applyBatched feeds ops to the batcher and checks after every step that
// neither limit is left exceeded: the final index is the same whenever a
// flush happens, so only this shows the limits at work.
func applyBatched(b *MailBatcher, ops []mailOp) error {
	for _, o := range ops {
		var err error
		if o.del {
			err = b.DeleteMailIndex(o.user, o.guid)
		} else {
			err = b.IndexMail(o.user, o.guid, o.subject, o.from, o.to, o.body, o.date, o.attach, o.tags, o.spam)
		}
		if err != nil {
			return err
		}
		b.mu.Lock()
		n, sz := len(b.pending), b.bytes
		b.mu.Unlock()
		if n >= b.maxDocs || sz >= b.maxBytes {
			return fmt.Errorf("limits not enforced: %d pending docs (max %d), %d bytes (max %d)", n, b.maxDocs, sz, b.maxBytes)
		}
	}
	return nil
}

// groupByUser reorders ops the way the loader produces them: one user at a
// time, each user's own order kept. Interleaved input flushes on every user
// change and so never builds a real batch.
func groupByUser(ops []mailOp) []mailOp {
	out := append([]mailOp(nil), ops...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].user < out[j].user })
	return out
}

var mailFields = []string{"subject", "body", "from", "to", "date", "has_attachment", "spam_score", "tags"}

// indexShape is everything compared between two indexes: the document count,
// every mapped field's terms with their document counts, and the sorted hit
// IDs of a set of queries. Scores are left out; they are not content. Term
// dictionaries keep the terms and counts of deleted and overwritten documents
// until Scorch merges, and when it merges is timing, so they are compared only
// for users that saw neither (u3 does).
type indexShape struct {
	Count uint64
	Terms map[string]map[string]uint64
	Hits  map[string][]string
}

var shapeQueries = []string{
	"body:zebra",
	`subject:"quarterly report"`,
	"body:walrus",
	"from:alice@example.com",
	"to:me@example.com",
	"tags:invoice",
	"tags:rewritten",
	"date:>=1700000100 date:<1700000300",
	"+has_attachment:true +tags:personal",
	"spam_score:>=3",
	"body:item17",
	"body:item20",
}

func shapeOf(t *testing.T, sm *SearchManager, user string, terms bool) indexShape {
	t.Helper()
	idx, err := sm.openUser(user)
	if err != nil {
		t.Fatal(err)
	}
	sh := indexShape{Terms: map[string]map[string]uint64{}, Hits: map[string][]string{}}
	if sh.Count, err = idx.DocCount(); err != nil {
		t.Fatal(err)
	}
	for _, f := range mailFields {
		if !terms {
			break
		}
		d, err := idx.FieldDict(f)
		if err != nil {
			t.Fatal(err)
		}
		terms := map[string]uint64{}
		for {
			e, err := d.Next()
			if err != nil {
				t.Fatal(err)
			}
			if e == nil {
				break
			}
			// A term can come back once per segment.
			terms[e.Term] += e.Count
			if f == "date" {
				// The coarse prefix terms of a numeric field report a
				// count that follows the segment layout, which is the one
				// thing batching changes. The term set and the range
				// queries still pin the content.
				terms[e.Term] = 1
			}
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		sh.Terms[f] = terms
	}
	for _, q := range shapeQueries {
		res, err := sm.Search(user, q, 10000)
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		ids := []string{}
		for _, r := range res {
			ids = append(ids, r.GUID)
		}
		sort.Strings(ids)
		sh.Hits[q] = ids
	}
	return sh
}

func TestMailBatcherMatchesPerMessageIndexing(t *testing.T) {
	const docs = 50
	tests := []struct {
		name     string
		maxDocs  int
		maxBytes int
	}{
		// 7 does not divide the per-user counts, so a lost tail shows.
		{"count-limited", 7, 1 << 30},
		{"byte-limited", 1000, 300},
		{"user-change-only", 1000, 1 << 30},
		{"one-per-batch", 1, 1 << 30},
	}
	for _, tc := range tests {
		for _, grouped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/grouped=%v", tc.name, grouped), func(t *testing.T) {
				ops := batchOps(docs)
				if grouped {
					ops = groupByUser(ops)
				}
				dirA, dirB := t.TempDir(), t.TempDir()

				a, err := NewSearchManager(dirA)
				if err != nil {
					t.Fatal(err)
				}
				if err := applyDirect(a, ops); err != nil {
					t.Fatal(err)
				}
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}

				bsm, err := NewSearchManager(dirB)
				if err != nil {
					t.Fatal(err)
				}
				b := bsm.NewMailBatcher(tc.maxDocs, tc.maxBytes)
				if err := applyBatched(b, ops); err != nil {
					t.Fatal(err)
				}
				if err := b.Flush(); err != nil {
					t.Fatal(err)
				}
				if err := bsm.Close(); err != nil {
					t.Fatal(err)
				}

				// Reopened from disk: memory alone proves nothing.
				a, err = NewSearchManager(dirA)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				bsm, err = NewSearchManager(dirB)
				if err != nil {
					t.Fatal(err)
				}
				defer bsm.Close()

				for _, user := range []string{"u1", "u2", "u3"} {
					terms := user != "u3"
					want, got := shapeOf(t, a, user, terms), shapeOf(t, bsm, user, terms)
					if want.Count == 0 {
						t.Fatalf("%s: reference index is empty", user)
					}
					if !reflect.DeepEqual(want, got) {
						t.Errorf("%s: batched index differs from per-message index:\n%s", user, shapeDiff(want, got))
					}
					// Vacuity guard: the queries must discriminate.
					hit := 0
					for _, ids := range want.Hits {
						if len(ids) > 0 {
							hit++
						}
					}
					if hit < 6 {
						t.Errorf("%s: only %d of %d queries hit anything", user, hit, len(shapeQueries))
					}
				}

				// Ordering: the re-indexed guid holds its last content and the
				// deleted one is gone, in both.
				u3 := shapeOf(t, bsm, "u3", false)
				if ids := u3.Hits["body:walrus"]; len(ids) != 1 {
					t.Errorf("re-indexed guid: hits %v, want one", ids)
				}
				for _, q := range []string{"body:item17", "body:item20"} {
					if ids := u3.Hits[q]; len(ids) != 0 {
						t.Errorf("%s: stale document survived: %v", q, ids)
					}
				}
			})
		}
	}
}

func TestMailBatcherFlushErrorIsKept(t *testing.T) {
	sm := newTestSearchManager(t)
	b := sm.NewMailBatcher(2, 0)
	// An invalid user ID cannot be opened, so the flush fails.
	if err := b.IndexMail("../bad", "g1", "s", "f", nil, "SECRETBODY", 1, false, nil); err != nil {
		t.Fatalf("buffering must not fail: %v", err)
	}
	err := b.Flush()
	if err == nil {
		t.Fatal("Flush: want an error for an unopenable index")
	}
	if got := err.Error(); containsAny(got, "SECRETBODY") {
		t.Errorf("error holds document content: %q", got)
	}
	if err2 := b.IndexMail("u1", "g2", "s", "f", nil, "b", 1, false, nil); err2 == nil {
		t.Error("IndexMail after a failed flush must return the kept error")
	}
	if err3 := b.Flush(); err3 == nil {
		t.Error("later Flush must return the kept error")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		for i := 0; i+len(x) <= len(s); i++ {
			if s[i:i+len(x)] == x {
				return true
			}
		}
	}
	return false
}

func TestMailBatcherConcurrentCallers(t *testing.T) {
	sm := newTestSearchManager(t)
	b := sm.NewMailBatcher(5, 0)
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 23 {
				if err := b.IndexMail("u1", fmt.Sprintf("g%d-%d", g, i), "s", "f@x.org", nil, "b", 1, false, nil); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := b.Flush(); err != nil {
		t.Fatal(err)
	}
	idx, err := sm.openUser("u1")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := idx.DocCount(); n != 4*23 {
		t.Errorf("DocCount = %d, want %d", n, 4*23)
	}
}

// TestBatchedIndexMetaMatchesServerCreated pins that batching leaves the
// index's persisted config alone: bleve saves any config given at creation
// and every later server open re-applies it.
func TestBatchedIndexMetaMatchesServerCreated(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a, err := NewSearchManager(dirA)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.IndexMail("u1", "g", "s", "f", nil, "b", 1, false, nil); err != nil {
		t.Fatal(err)
	}
	b, err := NewSearchManager(dirB)
	if err != nil {
		t.Fatal(err)
	}
	bt := b.NewMailBatcher(0, 0)
	if err := bt.IndexMail("u1", "g", "s", "f", nil, "b", 1, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := bt.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := errJoin(a.Close(), b.Close()); err != nil {
		t.Fatal(err)
	}
	read := func(dir string) string {
		raw, err := os.ReadFile(filepath.Join(dir, "search", "users", "u1", "index_meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if ra, rb := read(dirA), read(dirB); ra != rb {
		t.Errorf("index_meta.json differs:\nserver  %s\nbatched %s", ra, rb)
	}
	// Opening with plain bleve.Open, as the server does, must work.
	idx, err := bleve.Open(filepath.Join(dirB, "search", "users", "u1"))
	if err != nil {
		t.Fatal(err)
	}
	_ = idx.Close()
}

func errJoin(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// TestOnlyLoaderUsesMailBatcher is the server guard: the batched write path
// must not be reachable from any server code. cmd/bongo does not import
// scripts/loadgen, so confining the type to its own file plus the loader is
// enough.
func TestOnlyLoaderUsesMailBatcher(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch rel {
			case ".git", ".claude", "scripts", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || len(path) > 8 && path[len(path)-8:] == "_test.go" {
			return nil
		}
		if rel == filepath.Join("internal", "store", "search_batch.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if containsAny(string(raw), "NewMailBatcher", "MailBatcher") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("server code references the loader-only MailBatcher: %v", offenders)
	}
}

// shapeDiff names what differs, without dumping the binary numeric terms.
func shapeDiff(want, got indexShape) string {
	var out []string
	if want.Count != got.Count {
		out = append(out, fmt.Sprintf("doc count %d != %d", want.Count, got.Count))
	}
	for f, wt := range want.Terms {
		gt := got.Terms[f]
		for term, n := range wt {
			if gt[term] != n {
				out = append(out, fmt.Sprintf("field %s term %q: %d != %d", f, term, n, gt[term]))
			}
		}
		for term, n := range gt {
			if _, ok := wt[term]; !ok {
				out = append(out, fmt.Sprintf("field %s term %q only in batched (%d)", f, term, n))
			}
		}
	}
	for q, w := range want.Hits {
		if !reflect.DeepEqual(w, got.Hits[q]) {
			out = append(out, fmt.Sprintf("query %q: %v != %v", q, w, got.Hits[q]))
		}
	}
	return strings.Join(out, "\n")
}
