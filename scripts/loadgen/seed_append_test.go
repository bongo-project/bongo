package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

// folderIDs maps each non-INBOX folder to the planned Message-IDs in it.
func folderIDs(t *testing.T, o SeedOptions, user int) map[string][]string {
	t.Helper()
	spec, err := newPlanSpec("seed.test", o)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, mp := range spec.PlanUser(user).Messages {
		if mp.Folder != inboxFolder {
			out[mp.Folder] = append(out[mp.Folder], mp.ID)
		}
	}
	return out
}

func TestSeedAppendExactlyOnceAcrossInterruptAndResume(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) {
		o.FolderMix = "INBOX:1,Archive:2,Sent:2"
		o.Users, o.MessagesPerUser = 4, 10
	})
	_, total := planIDs(t, e.cfg.Seed)
	if total < 12 {
		t.Fatalf("plan has only %d folder messages; the interruption would prove nothing", total)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const k = 6
	f.imap.onAppend = func(n int64) {
		if n == k {
			cancel()
		}
	}
	code, stdout, _ := e.run(ctx)
	if code != exitFail || !strings.Contains(stdout, "interrupted") {
		t.Fatalf("first run: exit %d, stdout %q", code, stdout)
	}
	first := f.imap.count("APPEND")
	if first < k || first >= int64(total) {
		t.Fatalf("interruption was not part-way: %d of %d appended", first, total)
	}

	f.imap.onAppend = nil
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("second run: exit %d, stderr %s", code, stderr)
	}
	if got := f.imap.count("APPEND"); got != int64(total) {
		t.Errorf("server saw %d APPENDs over both runs, want exactly %d", got, total)
	}
	if !strings.Contains(stdout, "0 resent") {
		t.Errorf("a clean interrupt should resend nothing: %s", stdout)
	}
	e.checkFolders(f.imap)

	// A finished seed appends nothing more.
	if code, _, _ := e.run(context.Background()); code != exitOK || f.imap.count("APPEND") != int64(total) {
		t.Fatalf("completed seed was not a no-op: exit %d, %d appends", code, f.imap.count("APPEND"))
	}
}

// A state file from before APPEND existed has the INBOX messages done and no
// record at all for the others. It resumes by appending them.
func TestSeedAppendResumesAStateFileFromBeforeAppend(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.Users, o.MessagesPerUser = 2, 10 })
	spec, _ := newPlanSpec("seed.test", e.cfg.Seed)
	st, err := openState(e.state, spec.header())
	if err != nil {
		t.Fatal(err)
	}
	wantTotal := 0
	for i := range spec.Users {
		f.users[spec.email(i)] = "earlier-pw"
		if err := st.Pending(i, spec.email(i), "earlier-pw"); err != nil {
			t.Fatal(err)
		}
		if err := st.UserDone(i); err != nil {
			t.Fatal(err)
		}
		for _, mp := range spec.PlanUser(i).Messages {
			if mp.Folder == inboxFolder {
				if err := st.Sending(i, mp.Index); err != nil {
					t.Fatal(err)
				}
				if err := st.MsgDone(i, mp.Index); err != nil {
					t.Fatal(err)
				}
			} else {
				wantTotal++
			}
		}
	}
	_ = st.Close()
	if wantTotal == 0 {
		t.Fatal("degenerate plan")
	}
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if len(f.ids()) != 0 {
		t.Errorf("%d messages were sent over SMTP again", len(f.ids()))
	}
	if got := f.imap.count("APPEND"); got != int64(wantTotal) {
		t.Errorf("%d appends, want %d", got, wantTotal)
	}
	if !strings.Contains(stdout, "appended to folders") {
		t.Errorf("summary: %s", stdout)
	}
	e.checkFolders(f.imap)
}

func TestSeedAppendUnconfirmedMessage(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) {
		o.FolderMix = "INBOX:1,Archive:1"
		o.Users, o.MessagesPerUser = 1, 6
	})
	spec, _ := newPlanSpec("seed.test", e.cfg.Seed)
	var folder []MessagePlan
	for _, mp := range spec.PlanUser(0).Messages {
		if mp.Folder != inboxFolder {
			folder = append(folder, mp)
		}
	}
	if len(folder) < 2 {
		t.Fatalf("plan has %d folder messages", len(folder))
	}
	// State as a kill -9 leaves it: the account exists, the INBOX mail is
	// done, and two APPENDs were unconfirmed. The first did land, the second
	// did not.
	st, err := openState(e.state, spec.header())
	if err != nil {
		t.Fatal(err)
	}
	f.users[spec.email(0)] = "earlier-pw"
	f.imap.addUser(spec.email(0), "earlier-pw", "Archive")
	steps := []error{st.Pending(0, spec.email(0), "earlier-pw"), st.UserDone(0)}
	for _, mp := range spec.PlanUser(0).Messages {
		if mp.Folder == inboxFolder {
			steps = append(steps, st.Sending(0, mp.Index), st.MsgDone(0, mp.Index))
		}
	}
	steps = append(steps, st.Sending(0, folder[0].Index), st.Sending(0, folder[1].Index))
	for _, err := range steps {
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	landed := f.imap.users[spec.email(0)]
	raw := newAttachPool(spec).build(folder[0], spec.email(0))
	if _, err := landed.Append("Archive", literalOf(string(raw)), &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	f.imap.counts.Clear()

	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// The one that landed is not repeated; the one that did not is sent, and
	// counted as an unconfirmed resend.
	want := len(folder) - 1
	if got := f.imap.count("APPEND"); got != int64(want) {
		t.Errorf("%d appends, want %d (the landed message must not be repeated)", got, want)
	}
	if !strings.Contains(stdout, "1 resent") {
		t.Errorf("unconfirmed append not reported: %s", stdout)
	}
	var got []string
	for _, m := range f.imap.inspect(spec.email(0), "earlier-pw", "Archive") {
		got = append(got, m.ID)
	}
	var ids []string
	for _, mp := range folder {
		ids = append(ids, mp.ID)
	}
	sameSet(t, got, ids)
}

func TestSeedFolderMixNeedsIMAPAddress(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.IMAPAddr = "" })
	var out, errb bytes.Buffer
	code := seedMain(context.Background(), e.cfg, e.deps, &out, &errb)
	if code != exitRefuse || !strings.Contains(errb.String(), "--imap-addr") {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if n := e.dials.Load(); n != 0 {
		t.Errorf("%d dials before the refusal", n)
	}
	if len(f.users) != 0 || f.imap.accepted.Load() != 0 {
		t.Error("the fakes saw traffic")
	}
	if _, err := os.Stat(e.state); err == nil {
		t.Error("state file created before the refusal")
	}
}

func TestSeedInboxOnlyNeedsNoIMAP(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.FolderMix = defaultFolderMix; o.IMAPAddr = "" })
	if code, _, stderr := e.run(context.Background()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if f.imap.accepted.Load() != 0 {
		t.Error("an INBOX-only seed connected to IMAP")
	}
}

func TestSeedIMAPAddressOutsideAllowListDialsNothing(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, nil)
	e.cfg.Allow = e.cfg.Allow[:2]
	var out, errb bytes.Buffer
	code := seedMain(context.Background(), e.cfg, e.deps, &out, &errb)
	if code != exitRefuse || !strings.Contains(errb.String(), "--imap-addr") {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if e.dials.Load() != 0 || f.imap.accepted.Load() != 0 {
		t.Error("a connection was made to an address outside --target-allow")
	}
}

// An APPEND login that the server refuses must fail the run without printing
// the password or the address it tried.
func TestSeedAppendFailureOutputHasNoSecrets(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, nil)
	// The IMAP side knows a different password for everyone, so the login
	// is refused: a negative case fed with input that is otherwise valid.
	f.imap.lookup = func(string) (string, bool) { return "not-the-created-password-0000", true }
	code, stdout, stderr := e.run(context.Background())
	if code != exitFail || !strings.Contains(stderr, "append user") || !strings.Contains(stderr, "auth") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if f.imap.count("LOGIN_FAILED") == 0 {
		t.Fatal("the fake never refused a login, so the check is vacuous")
	}
	secrets := append([]string{"not-the-created-password-0000"}, f.passwords...)
	for email := range f.users {
		secrets = append(secrets, email)
	}
	for _, s := range secrets {
		for name, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
			if strings.Contains(out, s) {
				t.Errorf("%q leaked into %s", s[:min(len(s), 6)], name)
			}
		}
	}
}
