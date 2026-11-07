package main

import (
	"bytes"
	"math"
	"net/mail"
	"reflect"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

func testOptions() SeedOptions {
	return SeedOptions{
		Users: 10, MessagesPerUser: 20, Seed: 42,
		SizeMix: defaultSizeMix, FolderMix: defaultFolderMix,
		DedupShare: defaultDedupShare, DedupMin: defaultDedupMin, PoolSize: defaultPoolSize,
		Concurrency: 4,
	}
}

func testSpec(t *testing.T, mod func(*SeedOptions)) PlanSpec {
	t.Helper()
	o := testOptions()
	if mod != nil {
		mod(&o)
	}
	s, err := newPlanSpec("seed.test", o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlanDeterminism(t *testing.T) {
	a, b := testSpec(t, nil).Plan(), testSpec(t, nil).Plan()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two plans from the same seed differ")
	}
	if len(a) != 10 || len(a[0].Messages) != 20 {
		t.Fatalf("plan shape %d users, %d messages", len(a), len(a[0].Messages))
	}
	other := testSpec(t, func(o *SeedOptions) { o.Seed = 43 }).Plan()
	if reflect.DeepEqual(a, other) {
		t.Fatal("a different seed gave the same plan")
	}
	// User i must not depend on how many users there are.
	big := testSpec(t, func(o *SeedOptions) { o.Users = 20 }).Plan()
	if !reflect.DeepEqual(a, big[:10]) {
		t.Fatal("user plans changed when the user count changed")
	}
	// Nor on the messages-per-user of other runs: the first messages match.
	fewer := testSpec(t, func(o *SeedOptions) { o.MessagesPerUser = 5 }).Plan()
	for i := range fewer {
		if !reflect.DeepEqual(fewer[i].Messages, a[i].Messages[:5]) {
			t.Fatalf("user %d: message prefix changed with messages-per-user", i)
		}
	}
}

func TestPlanIdentity(t *testing.T) {
	s := testSpec(t, func(o *SeedOptions) { o.Seed = 0xabc })
	up := s.PlanUser(7)
	if up.Email != "u000007@seed.test" {
		t.Errorf("email %q", up.Email)
	}
	if got, want := up.Messages[3].ID, "<loadgen-abc-u7-m3@seed.test>"; got != want {
		t.Errorf("id %q, want %q", got, want)
	}
	for _, a := range []string{up.Email, s.sender()} {
		if err := checkAddress(a, "seed.test"); err != nil {
			t.Errorf("generated address refused: %v", err)
		}
	}
}

func TestSizeDistributionAndDedupShare(t *testing.T) {
	s := testSpec(t, func(o *SeedOptions) {
		o.Users = 400
		o.MessagesPerUser = 50
		o.FolderMix = "INBOX:70,Archive:20,Sent:10"
	})
	buckets := map[int]int{}
	folders := map[string]int{}
	total, attached := 0, 0
	threshold := s.poolEncLen(0) + messageOverhead
	for i := range s.Users {
		for _, m := range s.PlanUser(i).Messages {
			total++
			folders[m.Folder]++
			found := false
			for _, w := range s.Sizes {
				if float64(m.Size) >= 0.75*float64(w.Val) && float64(m.Size) <= 1.25*float64(w.Val) {
					buckets[w.Val]++
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("size %d is in no bucket", m.Size)
			}
			if m.Attach >= 0 {
				attached++
				if m.Size < threshold {
					t.Fatalf("size %d too small for an attachment (needs %d)", m.Size, threshold)
				}
				if m.Attach >= s.eligible(m.Size) {
					t.Fatalf("attachment %d does not fit size %d", m.Attach, m.Size)
				}
			}
		}
	}
	sum := 0
	for _, w := range s.Sizes {
		sum += w.Weight
	}
	for _, w := range s.Sizes {
		want := float64(w.Weight) / float64(sum)
		got := float64(buckets[w.Val]) / float64(total)
		if math.Abs(got-want) > 0.02 {
			t.Errorf("bucket %d: share %.3f, want %.3f", w.Val, got, want)
		}
	}
	if got := float64(folders["INBOX"]) / float64(total); math.Abs(got-0.7) > 0.02 {
		t.Errorf("INBOX share %.3f, want 0.70", got)
	}
	got, want := float64(attached)/float64(total), s.EffectiveDedupShare()
	if want < 0.03 || math.Abs(got-want) > 0.01 {
		t.Errorf("attachment share %.4f, effective share %.4f", got, want)
	}
}

func TestDedupShareExtremes(t *testing.T) {
	for _, tc := range []struct {
		share float64
		check func(attached, eligibleN int) bool
	}{
		{0, func(a, _ int) bool { return a == 0 }},
		{1, func(a, e int) bool { return a == e && e > 0 }},
	} {
		s := testSpec(t, func(o *SeedOptions) { o.DedupShare = tc.share; o.Users = 50 })
		attached, elig := 0, 0
		for i := range s.Users {
			for _, m := range s.PlanUser(i).Messages {
				if m.Attach >= 0 {
					attached++
				}
				if s.eligible(m.Size) > 0 {
					elig++
				}
			}
		}
		if !tc.check(attached, elig) {
			t.Errorf("share %v: %d attached of %d eligible", tc.share, attached, elig)
		}
	}
}

func TestMixValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  func(*SeedOptions)
		ok   bool
	}{
		{"default", func(*SeedOptions) {}, true},
		{"bytes", func(o *SeedOptions) { o.SizeMix = "1000:1,2KB:2" }, true},
		{"empty", func(o *SeedOptions) { o.SizeMix = "" }, false},
		{"no weight", func(o *SeedOptions) { o.SizeMix = "2KB" }, false},
		{"zero weight", func(o *SeedOptions) { o.SizeMix = "2KB:0,4KB:0" }, false},
		{"negative weight", func(o *SeedOptions) { o.SizeMix = "2KB:-1,4KB:3" }, false},
		{"bad size", func(o *SeedOptions) { o.SizeMix = "big:3" }, false},
		{"huge size", func(o *SeedOptions) { o.SizeMix = "999MB:3" }, false},
		{"bad folder", func(o *SeedOptions) { o.FolderMix = "a/b:1" }, false},
		{"share high", func(o *SeedOptions) { o.DedupShare = 1.5 }, false},
		{"share nan", func(o *SeedOptions) { o.DedupShare = math.NaN() }, false},
		{"no users", func(o *SeedOptions) { o.Users = 0 }, false},
		{"pool zero", func(o *SeedOptions) { o.PoolSize = 0 }, false},
	} {
		o := testOptions()
		tc.mod(&o)
		_, err := newPlanSpec("seed.test", o)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestBuildDeterministicAndSized(t *testing.T) {
	s := testSpec(t, nil)
	pool := newAttachPool(s)
	pool2 := newAttachPool(s)
	var withAtt, without int
	for i := range 6 {
		up := s.PlanUser(i)
		for _, m := range up.Messages {
			a, b := pool.build(m, up.Email), pool2.build(m, up.Email)
			if !bytes.Equal(a, b) {
				t.Fatalf("message %s built twice differs", m.ID)
			}
			msg, err := mail.ReadMessage(bytes.NewReader(a))
			if err != nil {
				t.Fatal(err)
			}
			if msg.Header.Get("Message-ID") != m.ID || msg.Header.Get("X-Loadgen-Id") != m.ID {
				t.Fatalf("ids %q %q, want %q", msg.Header.Get("Message-ID"), msg.Header.Get("X-Loadgen-Id"), m.ID)
			}
			if d := math.Abs(float64(len(a)-m.Size)) / float64(m.Size); d > 0.05 && len(a) > minMessageSize+400 {
				t.Errorf("%s: built %d bytes for planned %d", m.ID, len(a), m.Size)
			}
			if m.Attach >= 0 {
				withAtt++
			} else {
				without++
			}
		}
	}
	if withAtt == 0 || without == 0 {
		t.Fatalf("sample has %d with and %d without attachments", withAtt, without)
	}
}

func TestPoolEncodedLength(t *testing.T) {
	s := testSpec(t, nil)
	pool := newAttachPool(s)
	for j := range s.PoolSize {
		if got, want := len(pool.enc[j]), s.poolEncLen(j); got != want {
			t.Errorf("entry %d: %d bytes, formula says %d", j, got, want)
		}
		if len(pool.enc[j]) < s.DedupMin {
			t.Errorf("entry %d: %d bytes is under the dedup minimum", j, len(pool.enc[j]))
		}
	}
}

// The server splits out a deduplicated blob only for an attachment part whose
// encoded body reaches the minimum, so a repeated body alone proves nothing.
// This asks the server's own splitter.
func TestRepeatedAttachmentsDedupInStoreSplit(t *testing.T) {
	s := testSpec(t, func(o *SeedOptions) { o.DedupShare = 1; o.Users = 60 })
	pool := newAttachPool(s)
	hashes := map[int][]string{}
	var plainSplit int
	for i := range s.Users {
		up := s.PlanUser(i)
		for _, m := range up.Messages {
			sm := store.PrepareSplit(pool.build(m, up.Email), s.DedupMin)
			if m.Attach < 0 {
				if sm != nil {
					plainSplit++
				}
				continue
			}
			if sm == nil || len(sm.Attachments) != 1 {
				t.Fatalf("%s: pooled attachment was not split out", m.ID)
			}
			hashes[m.Attach] = append(hashes[m.Attach], sm.Attachments[0].Hash)
		}
	}
	if plainSplit != 0 {
		t.Errorf("%d messages without a pooled attachment were split", plainSplit)
	}
	repeats := 0
	for j, hs := range hashes {
		for _, h := range hs {
			if h != hs[0] {
				t.Fatalf("pool entry %d gave different blob hashes", j)
			}
		}
		if len(hs) > 1 {
			repeats++
		}
	}
	if repeats == 0 {
		t.Fatal("no pool entry was used twice, so nothing deduplicates")
	}
}

func TestEffectiveShareInHelp(t *testing.T) {
	s := testSpec(t, nil)
	if e := s.EffectiveDedupShare(); e <= 0 || e >= s.DedupShare {
		t.Fatalf("effective share %v should be between 0 and %v", e, s.DedupShare)
	}
	if strings.Contains(seedHelp, "TODO") {
		t.Fatal("help has a placeholder")
	}
}
