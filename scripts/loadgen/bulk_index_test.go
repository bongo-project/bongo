package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// indexFacts is what two loads of one plan must agree on for a user. Document
// IDs and the date field are left out: GUIDs come from the clock and INBOX
// mail is dated at load time, so they differ between any two runs. The
// store-level test pins date and ID equivalence exactly.
type indexFacts struct {
	Count uint64
	Terms map[string]map[string]uint64
	Hits  map[string]uint64
}

var loaderFacts = []string{"subject", "body", "from", "to", "has_attachment", "spam_score", "tags"}

// indexUsers maps each user's email to the search index directory by reading
// the profiles of a loaded store.
func indexUsers(t *testing.T, dir string) map[string]string {
	t.Helper()
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	it, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	out := map[string]string{}
	for it.Next() {
		parts := strings.Split(string(it.Key()), "/")
		if len(parts) != 4 || parts[3] != "profile" {
			continue
		}
		val, err := it.Value()
		if err != nil {
			t.Fatal(err)
		}
		var p bongov1.UserProfile
		if err := proto.Unmarshal(val, &p); err != nil {
			t.Fatal(err)
		}
		out[p.Email] = filepath.Join(dir, "search", "users", parts[2])
	}
	return out
}

func readFacts(t *testing.T, path string, queries []string) (indexFacts, []string) {
	t.Helper()
	idx, err := bleve.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	f := indexFacts{Terms: map[string]map[string]uint64{}, Hits: map[string]uint64{}}
	if f.Count, err = idx.DocCount(); err != nil {
		t.Fatal(err)
	}
	var common []string
	for _, field := range loaderFacts {
		d, err := idx.FieldDict(field)
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
			terms[e.Term] += e.Count
		}
		_ = d.Close()
		f.Terms[field] = terms
		if field == "body" || field == "subject" {
			var ts []string
			for term, n := range terms {
				if n > 1 && len(term) > 3 {
					ts = append(ts, field+":"+term)
				}
			}
			sort.Strings(ts)
			if len(ts) > 6 {
				ts = ts[:6]
			}
			common = append(common, ts...)
		}
	}
	if queries == nil {
		queries = common
	}
	for _, q := range queries {
		res, err := idx.Search(bleve.NewSearchRequestOptions(bleve.NewQueryStringQuery(q), 1, 0, false))
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		f.Hits[q] = res.Total
	}
	return f, queries
}

// TestBulkLoadBatchedIndexMatchesPerMessage loads one plan with the batched
// index writes and once with the server's per-message ones, and compares the
// resulting Bleve indexes user by user.
func TestBulkLoadBatchedIndexMatchesPerMessage(t *testing.T) {
	o := bulkTestOptions()
	o.Users, o.MessagesPerUser = 4, 30
	spec := bulkTestSpec(t, o)

	load := func(perMessage bool) string {
		dir := filepath.Join(t.TempDir(), "data")
		res, err := bulkLoad(context.Background(), spec, bulkOptions{
			DataDir: dir, Hostname: "bulk.test", Workers: 3, perMessageIndex: perMessage,
		}, filepath.Join(t.TempDir(), "bulk.state"), io.Discard)
		if err != nil {
			t.Fatalf("bulkLoad(perMessage=%v): %v", perMessage, err)
		}
		if res.Warnings != 0 {
			t.Errorf("perMessage=%v: %d warnings: %v", perMessage, res.Warnings, res.WarningSamples)
		}
		return dir
	}
	per, batched := indexUsers(t, load(true)), indexUsers(t, load(false))
	if len(per) != spec.Users || len(batched) != spec.Users {
		t.Fatalf("users: per-message %d, batched %d, want %d", len(per), len(batched), spec.Users)
	}
	for email, pPath := range per {
		bPath, ok := batched[email]
		if !ok {
			t.Fatalf("%s missing from the batched load", email)
		}
		want, queries := readFacts(t, pPath, nil)
		got, _ := readFacts(t, bPath, queries)
		if want.Count != uint64(o.MessagesPerUser) {
			t.Errorf("%s: per-message index holds %d documents, want %d", email, want.Count, o.MessagesPerUser)
		}
		hits := 0
		for _, n := range want.Hits {
			if n > 0 {
				hits++
			}
		}
		if len(queries) == 0 || hits == 0 {
			t.Errorf("%s: the query set found nothing, so the comparison proves nothing", email)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: batched index differs from per-message index\ncount %d vs %d\nhits %v vs %v\n%s",
				email, want.Count, got.Count, want.Hits, got.Hits, termDiff(want.Terms, got.Terms))
		}
	}
}

func termDiff(want, got map[string]map[string]uint64) string {
	var out []string
	for f, wt := range want {
		for term, n := range wt {
			if got[f][term] != n {
				out = append(out, fmt.Sprintf("%s %q: %d != %d", f, term, n, got[f][term]))
			}
		}
		for term := range got[f] {
			if _, ok := wt[term]; !ok {
				out = append(out, fmt.Sprintf("%s %q only in batched", f, term))
			}
		}
	}
	return strings.Join(out, "\n")
}
