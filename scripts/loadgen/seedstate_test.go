package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHeader(t *testing.T) stateHeader {
	t.Helper()
	return testSpec(t, nil).header()
}

func TestStateRoundTripKeepsRealPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s", "x.state")
	h := testHeader(t)
	st, err := openState(path, h)
	if err != nil {
		t.Fatal(err)
	}
	pw, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []func() error{
		func() error { return st.Pending(3, "u000003@seed.test", pw.Reveal()) },
		func() error { return st.UserDone(3) },
		func() error { return st.Sending(3, 1) },
		func() error { return st.Sending(3, 2) },
		func() error { return st.MsgDone(3, 2) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, err %v", info.Mode().Perm(), err)
	}
	st2, err := openState(path, h)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	u := st2.User(3)
	if u.pw != pw.Reveal() || u.pw == redacted || !u.done || !u.pending {
		t.Fatalf("round trip lost the user record: %+v", u)
	}
	if st2.Msg(3, 1) != msgSending || st2.Msg(3, 2) != msgDone || st2.Msg(3, 9) != msgUnknown {
		t.Fatal("message states not restored")
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), redacted) {
		t.Fatal("state file holds a redacted password")
	}
}

func TestStateRefusesLooseModeAndAcceptsStrict(t *testing.T) {
	h := testHeader(t)
	for _, tc := range []struct {
		mode os.FileMode
		ok   bool
	}{{0o600, true}, {0o640, false}, {0o604, false}, {0o644, false}, {0o660, false}} {
		path := filepath.Join(t.TempDir(), "x.state")
		st, err := openState(path, h)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		st, err = openState(path, h)
		if (err == nil) != tc.ok {
			t.Errorf("mode %v: err = %v, want ok=%v", tc.mode, err, tc.ok)
		}
		if st != nil {
			_ = st.Close()
		}
	}
}

func TestStateRefusesDifferentHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.state")
	h := testHeader(t)
	st, err := openState(path, h)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	for field, mod := range map[string]func(*stateHeader){
		"seed":              func(h *stateHeader) { h.Seed++ },
		"users":             func(h *stateHeader) { h.Users++ },
		"messages-per-user": func(h *stateHeader) { h.MPU++ },
		"test-domain":       func(h *stateHeader) { h.Domain = "other.test" },
		"size-mix":          func(h *stateHeader) { h.SizeMix = "2048:1" },
		"folder-mix":        func(h *stateHeader) { h.FolderMix = "INBOX:1,Archive:1" },
		"dedup-share":       func(h *stateHeader) { h.DedupShare = "0.1" },
		"dedup-min-size":    func(h *stateHeader) { h.DedupMin++ },
		"attachment-pool":   func(h *stateHeader) { h.Pool++ },
	} {
		o := h
		mod(&o)
		st, err := openState(path, o)
		if err == nil {
			_ = st.Close()
			t.Errorf("%s: resume with a different value was accepted", field)
			continue
		}
		if !strings.Contains(err.Error(), field) {
			t.Errorf("%s: error %q does not name the field", field, err)
		}
	}
	st, err = openState(path, h)
	if err != nil {
		t.Fatalf("same header refused: %v", err)
	}
	_ = st.Close()
}

func TestStateToleratesTornLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.state")
	h := testHeader(t)
	st, err := openState(path, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MsgDone(1, 1); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"t":"msg-done","u":1,"m":`)
	_ = f.Close()

	st, err = openState(path, h)
	if err != nil {
		t.Fatalf("torn line refused: %v", err)
	}
	if st.Msg(1, 1) != msgDone {
		t.Fatal("record before the torn line lost")
	}
	if err := st.MsgDone(1, 2); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	st, err = openState(path, h)
	if err != nil {
		t.Fatalf("file unreadable after repair: %v", err)
	}
	defer st.Close()
	if st.Msg(1, 2) != msgDone {
		t.Fatal("record written after repair lost")
	}
}

func TestDefaultStatePathIsOutsideRepo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	p, err := defaultStatePath("1", "seed.test", "127.0.0.1:1", "127.0.0.1:2")
	if err != nil {
		t.Skip("no user config dir on this platform")
	}
	wd, _ := os.Getwd()
	if strings.HasPrefix(p, wd) || !strings.Contains(p, "bongo-loadgen") {
		t.Errorf("state path %q", p)
	}
}
