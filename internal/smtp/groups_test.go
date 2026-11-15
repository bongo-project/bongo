package smtp

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// groupTestResolver resolves user addresses to "uid-<address>" and group
// addresses to a GroupRecipientError, as store.RecipientResolver does.
type groupTestResolver struct {
	users  map[string]bool
	groups map[string]string // group address -> group id
}

func (r *groupTestResolver) ResolveRecipient(email string) (string, error) {
	email = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(email), ">"), "<")
	if r.users[email] {
		return "uid-" + email, nil
	}
	if id, ok := r.groups[email]; ok {
		return "", &store.GroupRecipientError{Email: email, GroupID: id}
	}
	return "", io.EOF
}

type fakeGroups struct {
	groups map[string]*bongov1.Group
	member map[string][]string // group id -> member addresses
	calls  int
}

func (f *fakeGroups) ExpandMembers(id string) (*bongov1.Group, []store.GroupMember, error) {
	f.calls++
	g, ok := f.groups[id]
	if !ok {
		return nil, nil, store.ErrGroupNotFound
	}
	var out []store.GroupMember
	for _, a := range f.member[id] {
		out = append(out, store.GroupMember{UserID: "uid-" + a, Address: a})
	}
	return g, out, nil
}

// PostingAllowed lets everyone post: the policy itself is tested against the
// real store in groups_posting_test.go.
func (f *fakeGroups) PostingAllowed(*bongov1.Group, string) error { return nil }

type groupEnv struct {
	b      *Backend
	groups *fakeGroups
	blobs  interface {
		Get(string) (io.ReadCloser, error)
	}
	spool *pipeline.SpoolStore
	relay *fakeRelayQueue
	fan   *mail.FanoutLimiter
	quota *fakeQuotaChecker
}

const (
	gidA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	gidB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newGroupEnv(t *testing.T, fanoutLimit int) *groupEnv {
	t.Helper()
	dp, blobs, spool := setupTestPipeline(t)
	users := map[string]bool{"alice@example.com": true}
	for _, u := range []string{"m1", "m2", "m3", "m4"} {
		users[u+"@example.com"] = true
	}
	res := &groupTestResolver{
		users:  users,
		groups: map[string]string{"team@example.com": gidA, "ops@example.com": gidB, "gone@example.com": "cccccccccccccccccccccccccccccccc"},
	}
	fg := &fakeGroups{
		groups: map[string]*bongov1.Group{
			gidA: {Id: gidA, Address: "team@example.com"},
			gidB: {Id: gidB, Address: "ops@example.com"},
		},
		member: map[string][]string{
			gidA: {"m1@example.com", "m2@example.com", "m3@example.com"},
			gidB: {"m2@example.com", "m4@example.com"},
		},
	}
	relay := &fakeRelayQueue{}
	quota := &fakeQuotaChecker{overQuota: map[string]bool{}}
	fan := mail.NewFanoutLimiter(fanoutLimit)
	b := &Backend{
		auth:         &fakeAuth{userID: "uid-alice@example.com"},
		deliverer:    dp,
		resolver:     res,
		relayQueue:   relay,
		senderPolicy: &fakePolicy{},
		quotaChecker: quota,
		logger:       testLogger(),
	}
	b.SetGroups(fg)
	b.SetGroupFanoutLimiter(fan)
	return &groupEnv{b: b, groups: fg, blobs: blobs, spool: spool, relay: relay, fan: fan, quota: quota}
}

func (e *groupEnv) mx() *Session {
	return &Session{backend: e.b, logger: e.b.logger, from: "sender@remote.example", remoteIP: net.ParseIP("203.0.113.9")}
}

func (e *groupEnv) submission() *Session {
	return &Session{
		backend: e.b, logger: e.b.logger, from: "alice@example.com", submission: true,
		authed: true, userID: "uid-alice@example.com", remoteIP: net.ParseIP("198.51.100.7"),
	}
}

func rcptAll(t *testing.T, s *Session, to ...string) {
	t.Helper()
	for _, a := range to {
		if err := s.Rcpt(a, &gosmtp.RcptOptions{}); err != nil {
			t.Fatalf("RCPT %s: %v", a, err)
		}
	}
}

func (e *groupEnv) envelopes(t *testing.T) []*bongov1.QueueEnvelope {
	t.Helper()
	envs, err := e.spool.List("incoming")
	if err != nil {
		t.Fatal(err)
	}
	return envs
}

func (e *groupEnv) blob(t *testing.T, hash string) string {
	t.Helper()
	rc, err := e.blobs.Get(hash)
	if err != nil {
		t.Fatalf("blob %s: %v", hash, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func recipientsOf(envs []*bongov1.QueueEnvelope) []string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Recipients...)
	}
	slices.Sort(out)
	return out
}

func distinctBlobs(envs []*bongov1.QueueEnvelope) map[string]bool {
	m := map[string]bool{}
	for _, e := range envs {
		m[e.BlobHash] = true
	}
	return m
}

const mxMsg = "From: sender@remote.example\r\nTo: team@example.com\r\nSubject: hi\r\n\r\nbody\r\n"

func TestGroupRcptNilExpanderKeepsRefusal(t *testing.T) {
	e := newGroupEnv(t, 0)
	e.b.SetGroups(nil)
	err := e.mx().Rcpt("team@example.com", &gosmtp.RcptOptions{})
	if smtpCode(err) != 550 {
		t.Fatalf("RCPT to a group with no expander = %v, want 550", err)
	}
}

func TestGroupRcptUnknownAndDeadGroups(t *testing.T) {
	e := newGroupEnv(t, 0)
	if c := smtpCode(e.mx().Rcpt("gone@example.com", &gosmtp.RcptOptions{})); c != 550 {
		t.Errorf("RCPT to a deleted group = %d, want 550", c)
	}
	e.groups.member[gidB] = nil
	if c := smtpCode(e.mx().Rcpt("ops@example.com", &gosmtp.RcptOptions{})); c != 550 {
		t.Errorf("RCPT to a group with no live member = %d, want 550", c)
	}
}

func TestGroupExpansionPerTransport(t *testing.T) {
	for _, tt := range []struct {
		name    string
		session func(*groupEnv) *Session
		msg     string
	}{
		{"MX", (*groupEnv).mx, mxMsg},
		{"submission", (*groupEnv).submission, authedMsg},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newGroupEnv(t, 0)
			s := tt.session(e)
			// m2 is in both groups and addressed directly; m1 is also direct.
			rcptAll(t, s, "team@example.com", "ops@example.com", "m2@example.com", "m1@example.com")
			if err := s.Data(strings.NewReader(tt.msg)); err != nil {
				t.Fatalf("Data: %v", err)
			}
			envs := e.envelopes(t)
			want := []string{"m1@example.com", "m2@example.com", "m3@example.com", "m4@example.com"}
			if got := recipientsOf(envs); !slices.Equal(got, want) {
				t.Fatalf("recipients = %v, want one envelope each of %v", got, want)
			}
			if n := len(distinctBlobs(envs)); n != 1 {
				t.Fatalf("envelopes reference %d blobs, want 1 shared", n)
			}
			body := e.blob(t, envs[0].BlobHash)
			for _, id := range []string{gidA, gidB} {
				if !strings.Contains(body, "X-Bongo-Group: "+id+"\r\n") {
					t.Errorf("stored blob lacks the loop header for %s", id)
				}
			}
			byRcpt := map[string]*bongov1.QueueEnvelope{}
			for _, env := range envs {
				byRcpt[env.Recipients[0]] = env
			}
			if got := byRcpt["m3@example.com"].DsnOrcpt; got != "team@example.com" {
				t.Errorf("m3 ORCPT = %q, want the group address", got)
			}
		})
	}
}

func TestGroupOverQuotaMemberSkipped(t *testing.T) {
	e := newGroupEnv(t, 0)
	e.quota.overQuota["uid-m2@example.com"] = true
	s := e.mx()
	rcptAll(t, s, "team@example.com")
	if err := s.Data(strings.NewReader(mxMsg)); err != nil {
		t.Fatalf("Data: %v", err)
	}
	if got, want := recipientsOf(e.envelopes(t)), []string{"m1@example.com", "m3@example.com"}; !slices.Equal(got, want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}

	// Every member over quota is a refusal, like a direct recipient.
	e.quota.overQuota["uid-m1@example.com"] = true
	e.quota.overQuota["uid-m3@example.com"] = true
	if c := smtpCode(e.mx().Rcpt("team@example.com", &gosmtp.RcptOptions{})); c != 552 {
		t.Errorf("RCPT with every member over quota = %d, want 552", c)
	}
}

func TestGroupDSNNotifyNotAmplified(t *testing.T) {
	tests := []struct {
		name string
		in   uint32
		want uint32
	}{
		{"none", 0, 0},
		{"failure", mail.NotifyFailure, mail.NotifyFailure},
		{"success only", mail.NotifySuccess, mail.NotifyNever},
		{"success and delay", mail.NotifySuccess | mail.NotifyDelay, mail.NotifyNever},
		{"success and failure", mail.NotifySuccess | mail.NotifyFailure, mail.NotifyFailure},
		{"never", mail.NotifyNever, mail.NotifyNever},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memberDSN(RcptDSN{Notify: tt.in, ORCPT: "x", ORCPTType: "utf-8"}, "team@example.com")
			if got.Notify != tt.want || got.ORCPT != "team@example.com" || got.ORCPTType != "rfc822" {
				t.Errorf("memberDSN = %+v, want notify %d and the group as rfc822 ORCPT", got, tt.want)
			}
		})
	}
}

func TestGroupLoopHeaderStopsReExpansion(t *testing.T) {
	e := newGroupEnv(t, 0)
	s := e.mx()
	rcptAll(t, s, "team@example.com")
	if err := s.Data(strings.NewReader(mxMsg)); err != nil {
		t.Fatal(err)
	}
	envs := e.envelopes(t)
	if len(envs) != 3 {
		t.Fatalf("first hop envelopes = %d, want 3", len(envs))
	}
	// A member forwards the stored message back to the group: the stored blob
	// is exactly what the second hop sees.
	stored := e.blob(t, envs[0].BlobHash)
	s2 := e.mx()
	rcptAll(t, s2, "team@example.com")
	if err := s2.Data(strings.NewReader(stored)); err != nil {
		t.Fatalf("second hop Data: %v (a looped message must be accepted and discarded)", err)
	}
	if n := len(e.envelopes(t)); n != 3 {
		t.Fatalf("envelopes after the second hop = %d, want still 3", n)
	}
}

func TestGroupLoopKeepsDirectAndOtherGroupCopies(t *testing.T) {
	e := newGroupEnv(t, 0)
	s := e.mx()
	// Group A is looped; m1 is addressed directly (kept); m2 is also in group B
	// (kept); m3 is only in A (dropped); m4 is only in B (kept).
	rcptAll(t, s, "team@example.com", "m1@example.com", "ops@example.com")
	msg := "X-Bongo-Group: " + gidA + "\r\nFrom: sender@remote.example\r\nSubject: hi\r\n\r\nbody\r\n"
	if err := s.Data(strings.NewReader(msg)); err != nil {
		t.Fatal(err)
	}
	envs := e.envelopes(t)
	if got, want := recipientsOf(envs), []string{"m1@example.com", "m2@example.com", "m4@example.com"}; !slices.Equal(got, want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}
	body := e.blob(t, envs[0].BlobHash)
	if strings.Count(body, "X-Bongo-Group: "+gidA) != 1 || !strings.Contains(body, "X-Bongo-Group: "+gidB) {
		t.Errorf("blob headers wrong: only the live group is stamped on top of the incoming one\n%s", body)
	}
}

// aliasResolver maps extra addresses onto an existing user, as a subaddress
// or an alias does in store.RecipientResolver.
type aliasResolver struct {
	*groupTestResolver
	alias map[string]string // address -> user ID
}

func (r aliasResolver) ResolveRecipient(email string) (string, error) {
	if id, ok := r.alias[strings.Trim(strings.TrimSpace(email), "<>")]; ok {
		return id, nil
	}
	return r.groupTestResolver.ResolveRecipient(email)
}

// Direct recipients are never merged: two addresses of one account keep two
// envelopes each with its own DSN parameters, exactly as before groups.
func TestDirectRecipientsOfOneAccountKeepTheirEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		to   []string
	}{
		{"subaddress", []string{"m1@example.com", "m1+lists@example.com"}},
		{"alias", []string{"m1@example.com", "first@example.com"}},
		{"two aliases", []string{"first@example.com", "second@example.com"}},
		{"same address twice", []string{"m1@example.com", "m1@example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newGroupEnv(t, 0)
			e.b.resolver = aliasResolver{
				groupTestResolver: e.b.resolver.(*groupTestResolver),
				alias: map[string]string{
					"m1+lists@example.com": "uid-m1@example.com",
					"first@example.com":    "uid-m1@example.com",
					"second@example.com":   "uid-m1@example.com",
				},
			}
			s := e.mx()
			for i, a := range tt.to {
				opts := &gosmtp.RcptOptions{}
				if i == 1 {
					opts.Notify = []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess}
				}
				if err := s.Rcpt(a, opts); err != nil {
					t.Fatalf("RCPT %s: %v", a, err)
				}
			}
			if !slices.Equal(s.localRcpts, tt.to) {
				t.Fatalf("localRcpts = %v, want %v", s.localRcpts, tt.to)
			}
			if s.localDSN[0].Notify != 0 || s.localDSN[1].Notify != mail.NotifySuccess {
				t.Errorf("DSN parameters lost: %+v", s.localDSN)
			}
			if err := s.Data(strings.NewReader(mxMsg)); err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(tt.to)
			slices.Sort(want)
			if got := recipientsOf(e.envelopes(t)); !slices.Equal(got, want) {
				t.Errorf("delivered to %v, want %v", got, want)
			}
		})
	}
}

// A group member who is also addressed directly gets one copy, whichever RCPT
// came first, and the copy carries the direct recipient's own parameters. The
// fan-out cap is charged only for the copies the group alone added.
func TestGroupMemberAlsoDirectGetsOneCopy(t *testing.T) {
	tests := []struct {
		name string
		to   []string
	}{
		{"direct first", []string{"m1@example.com", "team@example.com"}},
		{"group first", []string{"team@example.com", "m1@example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newGroupEnv(t, 10)
			s := e.mx()
			direct := &gosmtp.RcptOptions{Notify: []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess}}
			for _, a := range tt.to {
				opts := &gosmtp.RcptOptions{}
				if a == "m1@example.com" {
					opts = direct
				}
				if err := s.Rcpt(a, opts); err != nil {
					t.Fatalf("RCPT %s: %v", a, err)
				}
			}
			i := slices.Index(s.localRcpts, "m1@example.com")
			if i < 0 || s.localDSN[i].Notify != mail.NotifySuccess || s.localDSN[i].ORCPT != "" {
				t.Fatalf("m1's envelope does not carry the direct RCPT's parameters: %v %+v", s.localRcpts, s.localDSN)
			}
			if err := s.Data(strings.NewReader(mxMsg)); err != nil {
				t.Fatal(err)
			}
			got := recipientsOf(e.envelopes(t))
			want := []string{"m1@example.com", "m2@example.com", "m3@example.com"}
			if !slices.Equal(got, want) {
				t.Fatalf("delivered to %v, want %v (one copy for m1)", got, want)
			}
			if r := e.fan.Remaining("ip:203.0.113.9"); r != 8 {
				t.Errorf("fan-out Remaining = %d, want 8 (m1's copy was asked for directly)", r)
			}
		})
	}
}

func TestGroupSubmissionWithExternalKeepsGroupIDOutOfRelayBlob(t *testing.T) {
	e := newGroupEnv(t, 0)
	s := e.submission()
	rcptAll(t, s, "team@example.com", "outside@elsewhere.example")
	if err := s.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatalf("Data: %v", err)
	}
	envs := e.envelopes(t)
	if len(envs) != 3 || len(distinctBlobs(envs)) != 1 {
		t.Fatalf("envelopes = %d over %d blobs, want 3 over 1", len(envs), len(distinctBlobs(envs)))
	}
	if !strings.Contains(e.blob(t, envs[0].BlobHash), "X-Bongo-Group: "+gidA) {
		t.Error("member blob is not stamped")
	}
	if len(e.relay.enqueued) != 1 {
		t.Fatalf("relay messages = %d, want 1", len(e.relay.enqueued))
	}
	relayBlob := e.blob(t, e.relay.enqueued[0].BlobHash)
	if strings.Contains(relayBlob, store.GroupLoopHeader) {
		t.Error("the relay blob carries a group id out to outside recipients")
	}
	if e.relay.enqueued[0].BlobHash == envs[0].BlobHash {
		t.Error("relay and member blobs are the same blob")
	}
}

func TestGroupFanoutCap(t *testing.T) {
	e := newGroupEnv(t, 3)

	// RCPT only peeks: a transaction that never reaches DATA spends nothing.
	s := e.mx()
	rcptAll(t, s, "team@example.com")
	if got := e.fan.Remaining("ip:203.0.113.9"); got != 3 {
		t.Fatalf("Remaining after RCPT = %d, want 3 (charge is at DATA)", got)
	}
	// Within one transaction the pending copies count: 3 pending + 1 more > 3.
	err := s.Rcpt("ops@example.com", &gosmtp.RcptOptions{})
	if smtpCode(err) != 452 || !strings.Contains(err.Error(), "Too many group recipients") {
		t.Fatalf("second group RCPT = %v, want 452 Too many group recipients", err)
	}
	if len(s.localRcpts) != 3 {
		t.Fatalf("a refused group RCPT left %d recipients, want the first group's 3 and nothing partial", len(s.localRcpts))
	}
	if err := s.Data(strings.NewReader(mxMsg)); err != nil {
		t.Fatal(err)
	}
	if got := e.fan.Remaining("ip:203.0.113.9"); got != 0 {
		t.Fatalf("Remaining after DATA = %d, want 0", got)
	}
	// Another transaction from the same source is refused whole at RCPT.
	if c := smtpCode(e.mx().Rcpt("team@example.com", &gosmtp.RcptOptions{})); c != 452 {
		t.Errorf("RCPT over the cap = %d, want 452", c)
	}
	// The envelope sender is not the key: another sender from the same IP shares it.
	other := e.mx()
	other.from = "someone-else@remote.example"
	if c := smtpCode(other.Rcpt("team@example.com", &gosmtp.RcptOptions{})); c != 452 {
		t.Errorf("a different envelope sender from the same IP = %d, want the same 452", c)
	}
	// Another IP has its own allowance.
	third := e.mx()
	third.remoteIP = net.ParseIP("203.0.113.10")
	third.from = "x@remote.example"
	if err := third.Rcpt("ops@example.com", &gosmtp.RcptOptions{}); err != nil {
		t.Errorf("RCPT from another IP: %v", err)
	}
}

func TestGroupFanoutSubmissionKeyedByAccount(t *testing.T) {
	e := newGroupEnv(t, 10)
	s := e.submission()
	rcptAll(t, s, "team@example.com")
	if err := s.Data(strings.NewReader(authedMsg)); err != nil {
		t.Fatal(err)
	}
	if got := e.fan.Remaining("u:uid-alice@example.com"); got != 7 {
		t.Errorf("account Remaining = %d, want 7", got)
	}
	if got := e.fan.Remaining("ip:198.51.100.7"); got != 10 {
		t.Errorf("submission must not charge the source IP, Remaining = %d", got)
	}
}

func TestGroupFanoutChargeRefusedAtData(t *testing.T) {
	e := newGroupEnv(t, 3)
	a, b := e.mx(), e.mx()
	rcptAll(t, a, "team@example.com")
	rcptAll(t, b, "team@example.com") // both peek 3 of 3
	if err := a.Data(strings.NewReader(mxMsg)); err != nil {
		t.Fatal(err)
	}
	err := b.Data(strings.NewReader(mxMsg))
	if smtpCode(err) != 452 {
		t.Fatalf("second Data = %v, want 452 at the charge", err)
	}
	if n := len(e.envelopes(t)); n != 3 {
		t.Errorf("envelopes = %d, want only the first transaction's 3", n)
	}
}

func TestGroupResetClearsState(t *testing.T) {
	e := newGroupEnv(t, 0)
	s := e.mx()
	rcptAll(t, s, "team@example.com", "m4@example.com")
	s.Reset()
	if len(s.localRcpts) != 0 || len(s.groupState.groups) != 0 || len(s.groupState.users) != 0 ||
		len(s.groupState.fanout) != 0 || len(s.groupState.direct) != 0 || s.groupState.pending != 0 {
		t.Fatalf("Reset left state behind: %+v %v", s.groupState, s.localRcpts)
	}
	// The same recipients are accepted again as new ones.
	rcptAll(t, s, "m1@example.com")
	if len(s.localRcpts) != 1 {
		t.Errorf("localRcpts = %v after Reset", s.localRcpts)
	}
}

func TestGroupMemberDedupFirstGroupWins(t *testing.T) {
	e := newGroupEnv(t, 0)
	s := e.mx()
	rcptAll(t, s, "team@example.com", "ops@example.com")
	got := slices.Clone(s.localRcpts)
	slices.Sort(got)
	want := []string{"m1@example.com", "m2@example.com", "m3@example.com", "m4@example.com"}
	if !slices.Equal(got, want) {
		t.Fatalf("localRcpts = %v, want %v", got, want)
	}
	if len(s.localDSN) != len(s.localRcpts) {
		t.Fatalf("localDSN (%d) is not aligned with localRcpts (%d)", len(s.localDSN), len(s.localRcpts))
	}
}

// The same flow over a real go-smtp server and connection: members receive one
// envelope each, and the cap answers a 4xx at RCPT on the wire.
func TestGroupExpansionOverTheWire(t *testing.T) {
	e := newGroupEnv(t, 3)
	addr := startSizedServer(t, e.b)

	c, err := gosmtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Mail("sender@remote.example", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("team@example.com", nil); err != nil {
		t.Fatalf("RCPT team: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, mxMsg); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("DATA: %v", err)
	}
	envs := e.envelopes(t)
	if len(envs) != 3 || len(distinctBlobs(envs)) != 1 {
		t.Fatalf("envelopes = %d over %d blobs, want 3 over 1", len(envs), len(distinctBlobs(envs)))
	}

	// The loopback source has now used its allowance; the next group RCPT in a
	// new transaction on the same connection is a temporary failure.
	if err := c.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("sender@remote.example", nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt("team@example.com", nil)
	var se *gosmtp.SMTPError
	if !errors.As(err, &se) || se.Code != 452 || se.EnhancedCode != (gosmtp.EnhancedCode{4, 5, 3}) {
		t.Fatalf("RCPT over the cap = %v, want 452 4.5.3", err)
	}
}

// An MX message whose header block does not parse is still delivered, but the
// skipped loop check is logged, with the group id and no message content.
func TestGroupUnparseableHeaderLogsSkippedLoopCheck(t *testing.T) {
	e := newGroupEnv(t, 0)
	var buf bytes.Buffer
	e.b.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := e.mx()
	rcptAll(t, s, "team@example.com")
	msg := "X-Bongo-Group: " + gidA + "\r\nsecret-line-without-colon\r\n\r\nbody\r\n"
	if err := s.Data(strings.NewReader(msg)); err != nil {
		t.Fatal(err)
	}
	if n := len(e.envelopes(t)); n != 3 {
		t.Fatalf("envelopes = %d, want 3 (delivery is unchanged)", n)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "loop check skipped") || !strings.Contains(out, "group="+gidA) {
		t.Errorf("no warning for the skipped loop check:\n%s", out)
	}
	if strings.Contains(out, "secret-line") {
		t.Errorf("the warning carries message content:\n%s", out)
	}
}
