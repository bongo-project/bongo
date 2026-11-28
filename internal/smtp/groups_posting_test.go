package smtp

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// groupApplier applies group commands to a real store as the FSM does. Without
// the dispatch a group would be stored as a literal key and every refusal test
// would pass vacuously as "group gone".
type groupApplier struct{ ps *store.PebbleStore }

func (a *groupApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsGroupSyntheticCommand(cmd.Key) {
		return store.ApplyGroupCommand(a.ps, cmd)
	}
	return errors.New("unexpected command " + string(cmd.Key))
}

// userAuth authenticates a username to its own user ID with any password.
type userAuth struct{ ids map[string]string }

func (u *userAuth) AuthenticateProtocol(_, username, _ string) (string, *bongov1.UserProfile, string, error) {
	id, ok := u.ids[username]
	if !ok {
		return "", nil, "", auth.ErrBadCredentials
	}
	return id, &bongov1.UserProfile{}, "", nil
}

// addrResolver maps local addresses to "uid-<address>" and group addresses to
// a GroupRecipientError.
type addrResolver struct {
	users  map[string]bool
	groups map[string]string
}

func (r *addrResolver) ResolveRecipient(email string) (string, error) {
	email = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(email), ">"), "<")
	if r.users[email] {
		return "uid-" + email, nil
	}
	if id, ok := r.groups[email]; ok {
		return "", &store.GroupRecipientError{Email: email, GroupID: id}
	}
	return "", io.EOF
}

type postingEnv struct {
	*groupEnv
	gs     *store.GroupStore
	groups map[string]string
}

// newPostingEnv builds a backend over a real GroupStore. Accounts: member
// (in the group), owner (an owner, not a member), colleague (same domain, not
// in the group) and eve (another domain). Each ID is "uid-<address>" so the
// sender policy accepts the account's own envelope sender.
func newPostingEnv(t *testing.T, policy string) (*postingEnv, string) {
	t.Helper()
	e := newGroupEnv(t, 0)
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	accounts := map[string]string{}
	for _, a := range []string{"member@example.com", "owner@example.com", "colleague@example.com", "eve@other.example"} {
		id := "uid-" + a
		data, err := proto.Marshal(&bongov1.UserProfile{Email: a})
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
		accounts[a] = id
	}
	gs := store.NewGroupStore(ps, &groupApplier{ps: ps})
	g, err := gs.CreateGroup(&bongov1.Group{
		Address:       "team@example.com",
		MemberIds:     []string{"uid-member@example.com"},
		OwnerIds:      []string{"uid-owner@example.com"},
		PostingPolicy: policy,
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if got, err := gs.GetGroup(g.Id); err != nil || got.PostingPolicy != policy {
		t.Fatalf("stored group = %v, %v", got, err)
	}
	e.b.SetGroups(gs)
	e.b.resolver = &addrResolver{
		users:  map[string]bool{"member@example.com": true},
		groups: map[string]string{"team@example.com": g.Id},
	}
	e.b.auth = &userAuth{ids: accounts}
	return &postingEnv{groupEnv: e, gs: gs}, g.Id
}

func startSubmissionServer(t *testing.T, b *Backend) string {
	t.Helper()
	srv := gosmtp.NewServer(implicitTLSBackend{b})
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// postOverWire sends one message to team@ and returns the RCPT error, the DATA
// error and whether DATA was reached. user "" means the MX port; otherwise the
// account signs in on submission. envFrom is the MAIL FROM and hdrFrom the
// From header; both are free for the sender to choose, and neither may matter.
func postOverWire(t *testing.T, e *postingEnv, user, envFrom, hdrFrom string) (rcptErr, dataErr error) {
	t.Helper()
	var c *gosmtp.Client
	var err error
	if user == "" {
		c, err = gosmtp.Dial(startSizedServer(t, e.b))
	} else {
		c, err = gosmtp.Dial(startSubmissionServer(t, e.b))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if user != "" {
		if err := c.Auth(sasl.NewPlainClient("", user, "pw")); err != nil {
			t.Fatalf("AUTH %s: %v", user, err)
		}
	}
	if err := c.Mail(envFrom, nil); err != nil {
		t.Fatalf("MAIL: %v", err)
	}
	if rcptErr = c.Rcpt("team@example.com", nil); rcptErr != nil {
		return rcptErr, nil
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "From: "+hdrFrom+"\r\nTo: team@example.com\r\nSubject: hi\r\n\r\nbody\r\n")
	return nil, w.Close()
}

func wantRestricted(t *testing.T, err error) {
	t.Helper()
	var se *gosmtp.SMTPError
	if !errors.As(err, &se) || se.Code != 550 || se.EnhancedCode != (gosmtp.EnhancedCode{5, 7, 1}) ||
		se.Message != "Posting to this group is restricted" {
		t.Fatalf("RCPT = %v, want 550 5.7.1 Posting to this group is restricted", err)
	}
}

func TestGroupPostingPolicyOverTheWire(t *testing.T) {
	const (
		member, owner, colleague, eve, mx = "member@example.com", "owner@example.com", "colleague@example.com", "eve@other.example", ""
	)
	allow := map[string]map[string]bool{
		"anyone":  {member: true, owner: true, colleague: true, eve: true, mx: true},
		"domain":  {member: true, owner: true, colleague: true},
		"members": {member: true, owner: true},
		"owners":  {owner: true},
	}
	for _, policy := range []string{"anyone", "domain", "members", "owners"} {
		for _, actor := range []string{member, owner, colleague, eve, mx} {
			name := policy + "/" + actor
			if actor == mx {
				name = policy + "/mx-outsider"
			}
			t.Run(name, func(t *testing.T) {
				e, _ := newPostingEnv(t, policy)
				from := actor
				if actor == mx {
					from = "outsider@remote.example"
				}
				rcptErr, dataErr := postOverWire(t, e, actor, from, from)
				envs := e.envelopes(t)
				if allow[policy][actor] {
					if rcptErr != nil || dataErr != nil {
						t.Fatalf("RCPT %v, DATA %v, want accepted", rcptErr, dataErr)
					}
					if got := recipientsOf(envs); len(got) != 1 || got[0] != "member@example.com" {
						t.Fatalf("envelopes for %v, want the member", got)
					}
					return
				}
				wantRestricted(t, rcptErr)
				if len(envs) != 0 {
					t.Fatalf("%d envelopes spooled after a refusal", len(envs))
				}
			})
		}
	}
}

// The envelope sender and the From header cost nothing to forge; neither may
// pass the check.
func TestGroupPostingForgedSenderCannotPass(t *testing.T) {
	t.Run("mx envelope and header name the owner", func(t *testing.T) {
		e, _ := newPostingEnv(t, "owners")
		rcptErr, _ := postOverWire(t, e, "", "owner@example.com", "owner@example.com")
		wantRestricted(t, rcptErr)
		if n := len(e.envelopes(t)); n != 0 {
			t.Fatalf("%d envelopes spooled", n)
		}
	})
	t.Run("submission non-member with the owner in the From header", func(t *testing.T) {
		e, _ := newPostingEnv(t, "owners")
		rcptErr, _ := postOverWire(t, e, "colleague@example.com", "colleague@example.com", "owner@example.com")
		wantRestricted(t, rcptErr)
		if n := len(e.envelopes(t)); n != 0 {
			t.Fatalf("%d envelopes spooled", n)
		}
	})
	t.Run("mx outsider claiming a member under members", func(t *testing.T) {
		e, _ := newPostingEnv(t, "members")
		rcptErr, _ := postOverWire(t, e, "", "member@example.com", "member@example.com")
		wantRestricted(t, rcptErr)
	})
}

// The policy runs before the quota pass: an outsider is told 550 5.7.1, never
// 552, so refusal reveals nothing about member mailboxes, and no fan-out budget
// is spent.
func TestGroupPostingRefusalComesBeforeQuota(t *testing.T) {
	e, _ := newPostingEnv(t, "owners")
	e.b.SetGroupFanoutLimiter(mail.NewFanoutLimiter(5))
	e.quota.overQuota["uid-member@example.com"] = true
	rcptErr, _ := postOverWire(t, e, "", "x@remote.example", "x@remote.example")
	wantRestricted(t, rcptErr)
	if got := e.b.GroupFanoutLimiter().Remaining("ip:127.0.0.1"); got != 5 {
		t.Errorf("fan-out remaining = %d, want 5", got)
	}
}
