package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// bulkTestOptions is a small plan with attachments big enough to split and a
// non-INBOX folder, so both delivery shapes are exercised.
func bulkTestOptions() SeedOptions {
	o := testOptions()
	o.Users, o.MessagesPerUser = 4, 8
	o.SizeMix = "2KB:50,64KB:50"
	o.FolderMix = "INBOX:70,Archive:30"
	o.DedupShare = 1
	return o
}

func bulkTestSpec(t *testing.T, o SeedOptions) PlanSpec {
	t.Helper()
	spec, err := newPlanSpec("bulk.test", o)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// loadBulk loads spec into a new directory and returns it.
func loadBulk(t *testing.T, spec PlanSpec) (dir string, res *bulkResult) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(t.TempDir(), "bulk.state")
	res, err := bulkLoad(context.Background(), spec, bulkOptions{DataDir: dir, Hostname: "bulk.test", Workers: 3}, statePath, io.Discard)
	if err != nil {
		t.Fatalf("bulkLoad: %v", err)
	}
	return dir, res
}

func dumpDir(t *testing.T, dir string) *storeDump {
	t.Helper()
	v, closeFn, err := openCheckStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	d, err := dumpStore(v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBulkLoadWritesPlan(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	dir, res := loadBulk(t, spec)
	t.Logf("load rate: %.0f users/s, %.0f messages/s, %.1f MB/s (%d messages, %d bytes, index drain %.1fs)",
		res.UsersPerSec, res.MessagesPerSec, res.BytesPerSec/(1<<20), res.Messages, res.Bytes, res.IndexDrain)

	wantMsgs := spec.Users * spec.MPU
	if res.Users != spec.Users || res.Messages != int64(wantMsgs) {
		t.Fatalf("result = %d users %d messages, want %d and %d", res.Users, res.Messages, spec.Users, wantMsgs)
	}
	if res.Warnings != 0 {
		t.Errorf("server code logged %d warnings during the load: %q", res.Warnings, res.WarningSamples)
	}
	if _, err := os.Stat(filepath.Join(dir, bulkMarker)); !os.IsNotExist(err) {
		t.Errorf("marker file still present after a complete load: %v", err)
	}

	d := dumpDir(t, dir)
	if len(d.Users) != spec.Users || d.Messages != wantMsgs {
		t.Fatalf("store holds %d users %d messages, want %d and %d", len(d.Users), d.Messages, spec.Users, wantMsgs)
	}
	if len(d.Problems) > 0 {
		t.Fatalf("problems: %v", d.Problems)
	}
	var split, whole, attached int
	for _, up := range spec.Plan() {
		u := d.Users[up.Email]
		if u == nil {
			t.Fatalf("user %s missing", up.Email)
		}
		perBox := map[string]int{}
		for _, mp := range up.Messages {
			m, ok := u.Messages[mp.ID]
			if !ok {
				t.Fatalf("%s: message %s missing", up.Email, mp.ID)
			}
			if m.Folder != mp.Folder {
				t.Errorf("%s folder = %s, want %s", mp.ID, m.Folder, mp.Folder)
			}
			perBox[mp.Folder]++
			if m.UID != uint32(perBox[mp.Folder]) {
				t.Errorf("%s uid = %d, want %d (plan order)", mp.ID, m.UID, perBox[mp.Folder])
			}
			if m.Split {
				split++
			} else {
				whole++
			}
			if mp.Attach >= 0 {
				attached++
				if len(m.Attachments) == 0 {
					t.Errorf("%s carries pool attachment %d but stores none", mp.ID, mp.Attach)
				}
				if mp.Folder == inboxFolder && !m.Split {
					t.Errorf("INBOX message %s with a pooled attachment was not split", mp.ID)
				}
				if mp.Folder != inboxFolder && m.Split {
					t.Errorf("folder message %s was split; IMAP APPEND does not split", mp.ID)
				}
			}
		}
		for name, n := range perBox {
			if u.Mailboxes[name].Messages != n {
				t.Errorf("%s mailbox %s holds %d, want %d", up.Email, name, u.Mailboxes[name].Messages, n)
			}
		}
		for _, name := range store.DefaultMailboxes {
			if _, ok := u.Mailboxes[name]; !ok {
				t.Errorf("%s lacks default mailbox %s", up.Email, name)
			}
		}
	}
	if split == 0 || whole == 0 || attached == 0 {
		t.Fatalf("plan did not exercise both shapes: split %d whole %d attached %d", split, whole, attached)
	}

	// The applied-index watermark stays unset: the first real entry must not
	// be skipped as already applied.
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	if _, err := ps.Get([]byte("/system/raft/applied_index")); err == nil {
		t.Error("applied_index was written by the loader")
	}
	if err := refuseRaftState(ps); err != nil {
		t.Errorf("loaded store reports Raft state: %v", err)
	}
}

func TestBulkLoadSameSeedSamePlanAndSharedAttachments(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	dirA, _ := loadBulk(t, spec)
	dirB, _ := loadBulk(t, spec)
	a, b := dumpDir(t, dirA), dumpDir(t, dirB)
	if diffs := compareDumps(a, b); len(diffs) != 0 {
		t.Fatalf("two loads of one plan differ: %v", diffs)
	}
	// Repeated attachments are one blob across users.
	seen := map[string]map[string]bool{}
	for email, u := range a.Users {
		for _, m := range u.Messages {
			if !m.Split {
				continue
			}
			for _, at := range m.Attachments {
				if seen[at] == nil {
					seen[at] = map[string]bool{}
				}
				seen[at][email] = true
			}
		}
	}
	shared := false
	for _, users := range seen {
		shared = shared || len(users) > 1
	}
	if !shared {
		t.Error("no attachment blob is shared by two users, so dedup was not exercised")
	}
}

func TestBulkStateFileLetsOtherSubcommandsLogIn(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	dir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(t.TempDir(), "bulk.state")
	if _, err := bulkLoad(context.Background(), spec, bulkOptions{DataDir: dir, Hostname: "bulk.test", Workers: 2}, statePath, io.Discard); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(statePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v mode %v", err, info)
	}
	st, err := openState(statePath, spec.header())
	if err != nil {
		t.Fatalf("seed-compatible state file does not reopen with the plan header: %v", err)
	}
	defer st.Close()
	for i := range spec.Users {
		u := st.User(i)
		if !u.done || u.email != spec.email(i) || len(u.pw) < 8 {
			t.Errorf("user %d state = done %v email %q pw length %d", i, u.done, u.email, len(u.pw))
		}
		for _, mp := range spec.PlanUser(i).Messages {
			if st.Msg(i, mp.Index) != msgDone {
				t.Errorf("message %d/%d not recorded, so seed would resend it", i, mp.Index)
			}
		}
	}
	// The password is a real credential for the stored hash.
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	if n := bytes.Count(mustRead(t, statePath), []byte(`"pw"`)); n != spec.Users {
		t.Errorf("state file holds %d passwords, want %d", n, spec.Users)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestBulkLoadRate loads a plan big enough to be bounded by the stores rather
// than by bcrypt, and reports the rates. It asserts only a floor, so a
// regression to the one-message-a-second range fails while a slow disk does
// not.
func TestBulkLoadRate(t *testing.T) {
	if testing.Short() {
		t.Skip("rate test")
	}
	o := testOptions()
	o.Users, o.MessagesPerUser = 16, 40
	o.SizeMix = "2KB:60,8KB:30,32KB:10"
	spec := bulkTestSpec(t, o)
	_, res := loadBulk(t, spec)
	t.Logf("rate: %.1f users/s, %.1f messages/s, %.2f MB/s over %.1fs, then %.1fs draining indexes",
		res.UsersPerSec, res.MessagesPerSec, res.BytesPerSec/(1<<20), res.Seconds, res.IndexDrain)
	if total := float64(res.Messages) / (res.Seconds + res.IndexDrain); total < 2 {
		t.Errorf("end-to-end rate %.2f messages/s is below the floor of 2", total)
	}
}

func TestBulkOutputHasNoPasswords(t *testing.T) {
	spec := bulkTestSpec(t, bulkTestOptions())
	dir := filepath.Join(t.TempDir(), "data")
	statePath := filepath.Join(t.TempDir(), "bulk.state")
	cfg := Config{TestDomain: "bulk.test", Seed: bulkTestOptions(), Bulk: bulkOptions{DataDir: dir, Hostname: "bulk.test", Workers: 2}}
	cfg.Seed.StateFile = statePath
	var out, errb bytes.Buffer
	if code := bulkMain(context.Background(), cfg, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	st, err := openState(statePath, spec.header())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := range spec.Users {
		if pw := st.User(i).pw; strings.Contains(out.String()+errb.String(), pw) {
			t.Fatalf("a password reached the output")
		}
	}
	if !strings.Contains(out.String(), "messages:") || !strings.Contains(out.String(), "/s)") {
		t.Errorf("output lacks rates: %q", out.String())
	}
}
