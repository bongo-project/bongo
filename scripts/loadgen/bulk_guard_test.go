package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

func TestValidateDataDir(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, files ...string) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.MkdirAll(filepath.Join(d, f), 0o750); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	real := mk("real")
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		dir  string
		want string // "" accepts
	}{
		{"absent", filepath.Join(root, "new"), ""},
		{"empty", mk("empty"), ""},
		{"no flag", "", "required"},
		{"relative", "data", "absolute"},
		{"root", "/", "root"},
		{"symlink", link, "symlink"},
		{"a file", file, "not a directory"},
		{"holds pebble", mk("pebbled", "pebble"), "not empty"},
		{"holds raft snapshots", mk("snapped", "raft-snapshots"), "not empty"},
		{"holds an interrupted load", mk("half", bulkMarker), "not empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDataDir(tc.dir)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused a valid directory: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestBulkMainRefusalsWriteNothing(t *testing.T) {
	good := func(t *testing.T) Config {
		return Config{TestDomain: "bulk.test", Seed: bulkTestOptions(), Bulk: bulkOptions{
			DataDir: filepath.Join(t.TempDir(), "data"), Hostname: "bulk.test", Workers: 2,
		}}
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, c *Config)
		want  string
	}{
		{"relative data dir", func(t *testing.T, c *Config) { c.Bulk.DataDir = "rel" }, "absolute"},
		{"no data dir", func(t *testing.T, c *Config) { c.Bulk.DataDir = "" }, "required"},
		{"raft snapshots present", func(t *testing.T, c *Config) {
			if err := os.MkdirAll(filepath.Join(c.Bulk.DataDir, "raft-snapshots"), 0o750); err != nil {
				t.Fatal(err)
			}
		}, "not empty"},
		{"state file exists", func(t *testing.T, c *Config) {
			c.Seed.StateFile = filepath.Join(t.TempDir(), "old.state")
			if err := os.WriteFile(c.Seed.StateFile, []byte("old\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "already exists"},
		{"state file inside the data dir", func(t *testing.T, c *Config) {
			c.Seed.StateFile = filepath.Join(c.Bulk.DataDir, "x.state")
		}, "inside the data directory"},
		{"control: a valid run", func(t *testing.T, c *Config) { c.Seed.Users = 1 }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := good(t)
			c.Seed.StateFile = filepath.Join(t.TempDir(), "bulk.state")
			tc.setup(t, &c)
			before := dirNames(t, c.Bulk.DataDir)
			var out, errb bytes.Buffer
			code := bulkMain(context.Background(), c, &out, &errb)
			if tc.want == "" {
				// The control must load, proving the refusals above are
				// the cause of their failures.
				if code != exitOK {
					t.Fatalf("control run failed: %s", errb.String())
				}
				return
			}
			if code != exitRefuse || !strings.Contains(errb.String(), tc.want) {
				t.Fatalf("code %d stderr %q, want a refusal containing %q", code, errb.String(), tc.want)
			}
			if after := dirNames(t, c.Bulk.DataDir); strings.Join(after, ",") != strings.Join(before, ",") {
				t.Errorf("a refused run changed the data directory: %v -> %v", before, after)
			}
		})
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	if !filepath.IsAbs(dir) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRefuseRaftState(t *testing.T) {
	for _, key := range []string{"_raft/log/0000000001", "_raft/stable/CurrentTerm", "/system/raft/applied_index"} {
		t.Run(key, func(t *testing.T) {
			ps, err := store.NewPebbleStore(filepath.Join(t.TempDir(), "pebble"))
			if err != nil {
				t.Fatal(err)
			}
			defer ps.Close()
			if err := refuseRaftState(ps); err != nil {
				t.Fatalf("an empty database was refused: %v", err)
			}
			if err := ps.Set([]byte(key), []byte("x")); err != nil {
				t.Fatal(err)
			}
			if err := refuseRaftState(ps); err == nil {
				t.Fatal("a database with Raft state was accepted")
			}
		})
	}
}

// Pebble relies on its own in-process lock table: a second open in the same
// process must fail, which is how a running server is detected.
func TestOpenLoaderStoresRefusesHeldDirectory(t *testing.T) {
	dir := t.TempDir()
	held, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = openLoaderStores(dir, nil)
	if !errors.Is(err, errDataDirInUse) {
		t.Fatalf("err = %v, want errDataDirInUse while the database is open", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	ls, err := openLoaderStores(dir, nil)
	if err != nil {
		t.Fatalf("open after release: %v", err)
	}
	if _, err := ls.drain(0); err != nil {
		t.Fatal(err)
	}
}

func TestBulkLoadRefusalIsNotAnIncompleteLoad(t *testing.T) {
	dir := t.TempDir()
	held, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	spec := bulkTestSpec(t, bulkTestOptions())
	_, err = bulkLoad(context.Background(), spec, bulkOptions{DataDir: dir, Workers: 1}, filepath.Join(t.TempDir(), "s"), io.Discard)
	var ref *refusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, bulkMarker)); !os.IsNotExist(statErr) {
		t.Error("a refused load left the incomplete marker")
	}
}

func TestBulkSubcommandFlagsAndGuard(t *testing.T) {
	var out, errb bytes.Buffer
	args := []string{"bulk", "--target-allow", "127.0.0.1", "--test-domain", "bulk.test", "--users", "2", "--messages-per-user", "1", "--seed", "9", "--bulk-data-dir", "relative"}
	if code := run(args, &out, &errb); code != exitRefuse || !strings.Contains(errb.String(), "absolute") {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
	// Without the safety flags the shared guard refuses before bulk runs.
	errb.Reset()
	if code := run([]string{"bulk", "--bulk-data-dir", filepath.Join(t.TempDir(), "d")}, &out, &errb); code != exitRefuse || !strings.Contains(errb.String(), "refused") {
		t.Fatalf("guard not applied: code %d stderr %q", code, errb.String())
	}
	// The plan flags are the seed subcommand's, with the same defaults.
	var cb, cs Config
	fb := newFlagSet("bulk", &cb, io.Discard)
	fsd := newFlagSet("seed", &cs, io.Discard)
	if err := fb.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := fsd.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if cb.Seed.Users != cs.Seed.Users || cb.Seed.SizeMix != cs.Seed.SizeMix || cb.Seed.DedupMin != cs.Seed.DedupMin || cb.Seed.PoolSize != cs.Seed.PoolSize {
		t.Errorf("bulk and seed plan defaults differ: %+v vs %+v", cb.Seed, cs.Seed)
	}
}
