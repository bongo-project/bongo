package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeState builds a state file by hand so a test controls every byte.
func writeState(t *testing.T, mode os.FileMode, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seed.state")
	// Every line ends in a newline, except one marked torn, which stops
	// where a crash would leave it.
	var data string
	for _, l := range lines {
		if l == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(l, tornMark); ok {
			data += rest
			continue
		}
		data += l + "\n"
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

const tornMark = "TORN:"

const (
	stHeader = `{"t":"header","header":{"seed":1,"users":3,"messages_per_user":1,"domain":"seed.test"}}`
	stP0     = `{"t":"user-pending","u":0,"email":"u000000@seed.test","pw":"secret-zero-0000000000"}`
	stD0     = `{"t":"user-done","u":0}`
	stP1     = `{"t":"user-pending","u":1,"email":"u000001@seed.test","pw":"secret-one-00000000000"}`
	stD1     = `{"t":"user-done","u":1}`
)

func TestLoadSeedAccounts(t *testing.T) {
	t.Run("finished accounts in index order", func(t *testing.T) {
		path := writeState(t, 0o600, stHeader, stP1, stD1, stP0, stD0, "")
		accts, err := loadSeedAccounts(path, "seed.test")
		if err != nil {
			t.Fatal(err)
		}
		if len(accts) != 2 || accts[0].index != 0 || accts[1].index != 1 || accts[0].pw.Reveal() != "secret-zero-0000000000" {
			t.Fatalf("accounts %+v", accts)
		}
	})
	t.Run("an unfinished account and a torn last line are ignored", func(t *testing.T) {
		path := writeState(t, 0o600, stHeader, stP0, stD0, stP1, tornMark+`{"t":"user-do`)
		accts, err := loadSeedAccounts(path, "seed.test")
		if err != nil {
			t.Fatal(err)
		}
		if len(accts) != 1 || accts[0].index != 0 {
			t.Fatalf("accounts %+v", accts)
		}
	})
	t.Run("the file is left exactly as it was", func(t *testing.T) {
		path := writeState(t, 0o600, stHeader, stP0, stD0, tornMark+`{"t":"user-do`)
		before, _ := os.ReadFile(path)
		infoBefore, _ := os.Stat(path)
		if _, err := loadSeedAccounts(path, "seed.test"); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		infoAfter, _ := os.Stat(path)
		if !bytes.Equal(before, after) || !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
			t.Error("loading changed the state file: the torn line was repaired or a record appended")
		}
		entries, _ := os.ReadDir(filepath.Dir(path))
		if len(entries) != 1 {
			t.Errorf("%d files next to the state file", len(entries))
		}
	})
	for name, tc := range map[string]struct {
		mode  os.FileMode
		lines []string
		want  string
	}{
		"group readable":      {0o640, []string{stHeader, stP0, stD0}, "0600"},
		"world readable":      {0o644, []string{stHeader, stP0, stD0}, "0600"},
		"no accounts":         {0o600, []string{stHeader}, "no finished accounts"},
		"only unfinished":     {0o600, []string{stHeader, stP0}, "no finished accounts"},
		"no header":           {0o600, []string{stP0, stD0}, "no header"},
		"corrupt line":        {0o600, []string{stHeader, "not json at all", stD0, ""}, "line 2"},
		"outside test domain": {0o600, []string{stHeader, `{"t":"user-pending","u":0,"email":"u000000@other.test","pw":"secret-zero-0000000000"}`, stD0}, "outside the test domain"},
		"production address":  {0o600, []string{stHeader, `{"t":"user-pending","u":0,"email":"lance@mail.tydewhatmay.eu","pw":"secret-zero-0000000000"}`, stD0}, "outside the test domain"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeState(t, tc.mode, tc.lines...)
			_, err := loadSeedAccounts(path, "seed.test")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			for _, secret := range []string{"secret-zero", "u000000@", "lance@", path} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the error mentions %q", secret)
				}
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		_, err := loadSeedAccounts(filepath.Join(t.TempDir(), "nope"), "seed.test")
		if err == nil {
			t.Fatal("accepted")
		}
		if _, statErr := os.Stat(filepath.Join(t.TempDir(), "nope")); statErr == nil {
			t.Error("created")
		}
	})
	t.Run("a directory is not a state file", func(t *testing.T) {
		if _, err := loadSeedAccounts(t.TempDir(), "seed.test"); err == nil {
			t.Fatal("accepted")
		}
	})
}

func TestAccountPasswordIsRedacted(t *testing.T) {
	path := writeState(t, 0o600, stHeader, stP0, stD0)
	accts, err := loadSeedAccounts(path, "seed.test")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for _, f := range []func() string{
		func() string { return fmt.Sprint(accts[0]) },
		func() string { return fmt.Sprintf("%+v %#v", accts[0], accts[0]) },
		func() string { return fmt.Sprint(accts[0].pw) },
	} {
		b.WriteString(f())
	}
	if strings.Contains(b.String(), "secret-zero") {
		t.Errorf("formatting an account printed its password: %s", b.String())
	}
}
