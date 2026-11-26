package imap

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	shOwner     = "own1"
	shOwnerAddr = "own1@example.test"
	shOther     = "own2"
	shOtherAddr = "own2@example.test"
	shUserAddr  = "u1@localhost"
	shWork      = "Other Users/own1@example.test/Work"
	shSub       = "Other Users/own1@example.test/Work/Sub"
	shInbox     = "Other Users/own1@example.test/INBOX"
	shRaw       = "From: a@b.test\r\nSubject: shared hello\r\n\r\nbody\r\n"
)

// sharedEnv is one owner (own1) holding Work, Work/Sub and INBOX, and the
// grantee u1 (the account the stub authenticator logs in as). Grants go through
// the same applier dispatch the FSM uses, so a pending or revoked grant really
// is one.
type sharedEnv struct {
	t       *testing.T
	ps      *store.PebbleStore
	ms      *store.MailboxStore
	acl     *store.ACLStore
	blobs   *store.BlobStore
	ids     map[string]string
	uids    []uint32
	applier *imapDirectApplier
}

func seedSharedAccount(t *testing.T, ps *store.PebbleStore, id, email string, disabled bool) {
	t.Helper()
	p, err := proto.Marshal(&bongov1.UserProfile{Email: email, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), p); err != nil {
		t.Fatal(err)
	}
	a, err := proto.Marshal(&bongov1.AddressRecord{UserId: id, Kind: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/system/address/"+strings.ToLower(email)), a); err != nil {
		t.Fatal(err)
	}
}

func newSharedEnv(t *testing.T) *sharedEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	applier := &imapDirectApplier{store: ps}
	ms := store.NewMailboxStore(ps, applier)
	applier.mailboxes = ms
	e := &sharedEnv{t: t, ps: ps, ms: ms, acl: store.NewACLStore(ps, applier), blobs: blobs, ids: map[string]string{}, applier: applier}

	seedSharedAccount(t, ps, pollTestUser, shUserAddr, false)
	seedSharedAccount(t, ps, shOwner, shOwnerAddr, false)
	seedSharedAccount(t, ps, shOther, shOtherAddr, false)
	seedSharedAccount(t, ps, "slash", "we/ird@example.test", false)

	for _, mb := range []string{"INBOX", "Archive"} {
		if err := ms.CreateMailbox(pollTestUser, mb); err != nil {
			t.Fatal(err)
		}
	}
	for _, mb := range []string{"INBOX", "Work", "Work/Sub", "Private"} {
		if err := ms.CreateMailbox(shOwner, mb); err != nil {
			t.Fatal(err)
		}
		id, err := ms.EnsureMailboxID(shOwner, mb)
		if err != nil {
			t.Fatal(err)
		}
		e.ids[mb] = id
	}
	hash, err := blobs.Put([]byte(shRaw))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		uid, err := ms.DeliverMessage(shOwner, "Work", &bongov1.MailMeta{
			Guid: fmt.Sprintf("g%d", i), Subject: "shared hello", ReceivedAt: time.Now().Unix(),
			RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(shRaw)), ContentType: "message/rfc822"},
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		e.uids = append(e.uids, uid)
	}
	return e
}

func (e *sharedEnv) grantTo(user, folder, rights, state string, at time.Time) {
	e.t.Helper()
	if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOwner, e.ids[folder], store.GranteeUser(user), "", rights, state, shOwner, at); err != nil {
		e.t.Fatal(err)
	}
}

func (e *sharedEnv) grant(folder, rights string) {
	e.t.Helper()
	e.grantTo(pollTestUser, folder, rights, store.GrantStateAccepted, time.Now())
}

func (e *sharedEnv) revoke(folder string) {
	e.t.Helper()
	if err := e.acl.RevokeGrant(store.ACLKindMailbox, shOwner, e.ids[folder], store.GranteeUser(pollTestUser)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *sharedEnv) session() *session {
	return &session{
		namespaces: store.DefaultIMAPNamespaces(),
		acl:        e.acl,
		pebble:     e.ps,
		blobs:      e.blobs,
		mailboxes:  e.ms,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		userID:     pollTestUser,
	}
}

func (e *sharedEnv) flags(uid uint32) uint32 {
	e.t.Helper()
	m, err := e.ms.GetMessage(shOwner, "Work", uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return m.Flags
}

func (e *sharedEnv) startServer() string {
	e.t.Helper()
	return e.startServerAuth(stubAuthenticator{})
}

func (e *sharedEnv) startServerAuth(auth profileAuthenticator) string {
	e.t.Helper()
	notifier := NewNotifier()
	e.ms.SetNotifier(NewStoreNotifier(notifier))
	srv := NewServer(auth, e.ps, e.blobs, e.ms, nil, nil, nil, notifier, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetACLStore(e.acl)
	if err := srv.Listen("127.0.0.1:0", "", nil, false, nil); err != nil {
		e.t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	e.t.Cleanup(func() { _ = srv.Close() })
	return srv.PlainAddr()
}

func errString(err error) string {
	var ie *imap.Error
	if errors.As(err, &ie) {
		return fmt.Sprintf("%s [%s] %s", ie.Type, ie.Code, ie.Text)
	}
	if err == nil {
		return "OK"
	}
	return "non-imap: " + err.Error()
}

func hasCode(err error, code imap.ResponseCode) bool {
	var ie *imap.Error
	return errors.As(err, &ie) && ie.Code == code
}

func wantErr(t *testing.T, got error, want *imap.Error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("got %s, want OK", errString(got))
		}
		return
	}
	if got != error(want) && errString(got) != errString(want) {
		t.Fatalf("got %s, want %s", errString(got), errString(want))
	}
}

// ---- no existence oracle ------------------------------------------------

// Every command naming a folder the user cannot see answers as it does for a
// folder that does not exist. A pending or expired-pending grant, a grant
// without the lookup right, a missing owner and a missing folder are all the
// same answer; the CREATE case answers the reserved name, as it always did.
func TestSharedNoExistenceOracle(t *testing.T) {
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	scenarios := []struct {
		name   string
		setup  func(e *sharedEnv)
		target string
	}{
		{"no grant", func(e *sharedEnv) {}, shWork},
		{"pending", func(e *sharedEnv) {
			e.grantTo(pollTestUser, "Work", "lrswipkxtea", store.GrantStatePending, time.Now())
		}, shWork},
		{"expired pending", func(e *sharedEnv) {
			e.grantTo(pollTestUser, "Work", "lrswipkxtea", store.GrantStatePending, old)
		}, shWork},
		{"accepted without l", func(e *sharedEnv) { e.grant("Work", "rswipkxtea") }, shWork},
		{"missing owner", func(e *sharedEnv) { e.grant("Work", "lrswipkxtea") }, "Other Users/nobody@example.test/Work"},
		{"missing folder", func(e *sharedEnv) { e.grant("Work", "lrswipkxtea") }, "Other Users/own1@example.test/Nope"},
		{"self", func(e *sharedEnv) {}, "Other Users/u1@localhost/INBOX"},
	}
	calls := []struct {
		name string
		want *imap.Error
		call func(s *session, target string) error
	}{
		{"select", errNoSuchMailbox, func(s *session, n string) error { _, err := s.Select(n, nil); return err }},
		{"examine", errNoSuchMailbox, func(s *session, n string) error {
			_, err := s.Select(n, &imap.SelectOptions{ReadOnly: true})
			return err
		}},
		{"create", errReservedMailboxName, func(s *session, n string) error { return s.Create(n+"/Child", nil) }},
		{"delete", errNoSuchMailbox, func(s *session, n string) error { return s.Delete(n) }},
		{"rename", errNoSuchMailbox, func(s *session, n string) error { return s.Rename(n, "Elsewhere", nil) }},
		{"rename in tree", errNoSuchMailbox, func(s *session, n string) error { return s.Rename(n, n+"2", nil) }},
		{"subscribe", errNoSuchMailbox, func(s *session, n string) error { return s.Subscribe(n) }},
		{"unsubscribe", nil, func(s *session, n string) error { return s.Unsubscribe(n) }},
		{"status", errNoSuchMailbox, func(s *session, n string) error {
			_, err := s.Status(n, &imap.StatusOptions{NumMessages: true})
			return err
		}},
		{"append", errTryCreate, func(s *session, n string) error { _, err := s.Append(n, newLiteral([]byte(shRaw)), nil); return err }},
		{"quota root", errNoSuchMailbox, func(s *session, n string) error { _, err := s.GetQuotaRoot(n); return err }},
		{"copy", errTryCreate, func(s *session, n string) error {
			s.selectedMailbox = "INBOX"
			_, err := s.Copy(imap.UIDSetNum(1), n)
			return err
		}},
		{"move", errTryCreate, func(s *session, n string) error {
			s.selectedMailbox = "INBOX"
			return s.Move(nil, imap.UIDSetNum(1), n)
		}},
	}
	for _, sc := range scenarios {
		for _, c := range calls {
			t.Run(sc.name+"/"+c.name, func(t *testing.T) {
				e := newSharedEnv(t)
				sc.setup(e)
				wantErr(t, c.call(e.session(), sc.target), c.want)
			})
		}
	}
}

// A session that selected a shared folder and then lost the right to see it
// gets the not-found answer from every selected-state command, and no data.
func TestSharedLossAnswersNotFound(t *testing.T) {
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	losses := []struct {
		name string
		do   func(e *sharedEnv)
	}{
		{"revoked", func(e *sharedEnv) { e.revoke("Work") }},
		{"back to pending", func(e *sharedEnv) {
			e.revoke("Work")
			e.grantTo(pollTestUser, "Work", "lrswipkxtea", store.GrantStatePending, time.Now())
		}},
		{"back to expired pending", func(e *sharedEnv) {
			e.revoke("Work")
			e.grantTo(pollTestUser, "Work", "lrswipkxtea", store.GrantStatePending, old)
		}},
		{"lookup right removed", func(e *sharedEnv) {
			e.revoke("Work")
			e.grant("Work", "rswipkxtea")
		}},
		{"read right removed", func(e *sharedEnv) {
			e.revoke("Work")
			e.grant("Work", "lswipkxtea")
		}},
		{"folder deleted", func(e *sharedEnv) {
			if err := e.ms.DeleteMailbox(shOwner, "Work"); err != nil {
				t.Fatal(err)
			}
		}},
		{"owner disabled", func(e *sharedEnv) { seedSharedAccount(e.t, e.ps, shOwner, shOwnerAddr, true) }},
	}
	calls := []struct {
		name string
		want *imap.Error
		call func(s *session) error
	}{
		{"fetch", errNoSuchMailbox, func(s *session) error { return s.Fetch(nil, imap.UIDSetNum(1), &imap.FetchOptions{Flags: true}) }},
		{"search", errNoSuchMailbox, func(s *session) error {
			_, err := s.Search(imapserver.NumKindUID, &imap.SearchCriteria{}, nil)
			return err
		}},
		{"store", errNoSuchMailbox, func(s *session) error {
			return s.Store(nil, imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil)
		}},
		{"expunge", errNoSuchMailbox, func(s *session) error { return s.Expunge(nil, nil) }},
		{"copy", errNoSuchMailbox, func(s *session) error { _, err := s.Copy(imap.UIDSetNum(1), "Archive"); return err }},
		{"move", errNoSuchMailbox, func(s *session) error { return s.Move(nil, imap.UIDSetNum(1), "Archive") }},
		// Poll errors kill the connection and Poll runs after the command is
		// answered, so it reports nothing instead.
		{"poll", nil, func(s *session) error { return s.Poll(nil, true) }},
		{"idle", errNoSuchMailbox, func(s *session) error { return s.Idle(nil, make(chan struct{})) }},
		{"unselect", nil, func(s *session) error { return s.Unselect() }},
	}
	for _, l := range losses {
		for _, c := range calls {
			t.Run(l.name+"/"+c.name, func(t *testing.T) {
				e := newSharedEnv(t)
				e.grant("Work", "lrswipkxtea")
				s := e.session()
				if _, err := s.Select(shWork, nil); err != nil {
					t.Fatal(err)
				}
				l.do(e)
				wantErr(t, c.call(s), c.want)
				if c.name != "unselect" && (len(s.cachedMsgs) != 0 || len(s.reportedUIDs) != 0) {
					t.Errorf("session still holds the folder's messages after losing access")
				}
			})
		}
	}
}

func TestSharedPathTricks(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswipkxtea")
	e.grant("INBOX", "lrswipkxtea")
	e.grantTo(pollTestUser, "Private", "lr", store.GrantStatePending, time.Now())
	// The account whose address holds a slash owns a Work folder shared with u1.
	if err := e.ms.CreateMailbox("slash", "Work"); err != nil {
		t.Fatal(err)
	}
	id, err := e.ms.EnsureMailboxID("slash", "Work")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, "slash", id, store.GranteeUser(pollTestUser), "", "lr", store.GrantStateAccepted, "slash", time.Now()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"Other Users/own1@example.test/../Work",
		"Other Users/own1@example.test/Work/../Work",
		"Other Users/own1@example.test/../u1@localhost/INBOX",
		"Other Users//own1@example.test/Work",
		"Other Users/own1@example.test//Work",
		"Other Users/own1@example.test/Work/",
		"Other Users/own1@example.test/",
		"Other Users/own1@example.test",
		"Other Users/",
		"Other Users",
		"Other Users/own1@example.test/WORK",
		"Other Users/own1@example.test/work",
		"Other Users/own1@example.test/Private",
		"Other Users/we/ird@example.test/Work",
		"Other Users/we%2Fird@example.test/Work",
		"Shared/own1@example.test/Work",
		"Shared",
		"Other Users/u1@localhost/INBOX",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.session().Select(name, nil)
			wantErr(t, err, errNoSuchMailbox)
		})
	}

	for _, name := range []string{
		shWork,
		"other users/OWN1@Example.TEST/Work",
		"OTHER USERS/own1@example.test/Work",
		"Other Users/own1@example.test/inbox",
		"Other Users/own1@example.test/INBOX",
	} {
		t.Run("resolves "+name, func(t *testing.T) {
			if _, err := e.session().Select(name, nil); err != nil {
				t.Fatalf("Select: %s", errString(err))
			}
		})
	}
}

// A renamed folder stays shared: the grant names the folder by id.
func TestSharedFolderFollowsRename(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrs")
	s := e.session()
	if _, err := s.Select(shWork, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.ms.RenameMailbox(shOwner, "Work", "Projects"); err != nil {
		t.Fatal(err)
	}
	// The old name is gone for the grantee; a new folder taking it is not theirs.
	if err := e.ms.CreateMailbox(shOwner, "Work"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.session().Select(shWork, nil); !errors.Is(err, errNoSuchMailbox) {
		t.Fatalf("old name after rename: %s", errString(err))
	}
	if _, err := e.session().Select("Other Users/own1@example.test/Projects", nil); err != nil {
		t.Fatalf("new name: %s", errString(err))
	}
	// The open session follows the id, not the name that was reused.
	if err := s.checkSelected(); err != nil {
		t.Fatalf("checkSelected after rename: %s", errString(err))
	}
	if s.selName != "Projects" {
		t.Fatalf("selName = %q, want Projects", s.selName)
	}
	if err := s.Fetch(nil, imap.UIDSetNum(999), &imap.FetchOptions{Flags: true}); err != nil {
		t.Fatalf("Fetch after rename: %s", errString(err))
	}
}

// ---- rights ---------------------------------------------------------------

func TestSharedRightRead(t *testing.T) {
	for _, tt := range []struct {
		rights string
		want   *imap.Error
	}{{"lw", errNoPermission}, {"lr", nil}} {
		t.Run(tt.rights, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			_, err := e.session().Select(shWork, nil)
			wantErr(t, err, tt.want)
			_, err = e.session().Status(shWork, &imap.StatusOptions{NumMessages: true})
			wantErr(t, err, tt.want)
			_, err = e.session().Select(shWork, &imap.SelectOptions{ReadOnly: true})
			wantErr(t, err, tt.want)
		})
	}
}

func TestSharedRightsOnStore(t *testing.T) {
	flag := func(op imap.StoreFlagsOp, f ...imap.Flag) *imap.StoreFlags {
		return &imap.StoreFlags{Op: op, Silent: true, Flags: f}
	}
	tests := []struct {
		name   string
		rights string
		flags  *imap.StoreFlags
		want   *imap.Error
		bit    uint32
	}{
		{"seen without s", "lrw", flag(imap.StoreFlagsAdd, imap.FlagSeen), errNoPermission, 0},
		{"seen with s", "lrs", flag(imap.StoreFlagsAdd, imap.FlagSeen), nil, store.FlagSeen},
		{"flagged without w", "lrs", flag(imap.StoreFlagsAdd, imap.FlagFlagged), errNoPermission, 0},
		{"flagged with w", "lrw", flag(imap.StoreFlagsAdd, imap.FlagFlagged), nil, store.FlagFlagged},
		{"answered without w", "lrt", flag(imap.StoreFlagsAdd, imap.FlagAnswered), errNoPermission, 0},
		{"deleted without t", "lrw", flag(imap.StoreFlagsAdd, imap.FlagDeleted), errNoPermission, 0},
		{"deleted with t", "lrt", flag(imap.StoreFlagsAdd, imap.FlagDeleted), nil, store.FlagDeleted},
		{"seen and deleted need both", "lrs", flag(imap.StoreFlagsAdd, imap.FlagSeen, imap.FlagDeleted), errNoPermission, 0},
		{"replace changing seen only", "lrs", flag(imap.StoreFlagsSet, imap.FlagSeen), nil, store.FlagSeen},
		{"replace changing flagged without w", "lrs", flag(imap.StoreFlagsSet, imap.FlagSeen, imap.FlagFlagged), errNoPermission, 0},
		{"keyword with w", "lrw", flag(imap.StoreFlagsAdd, "$work"), nil, 0},
		{"keyword without w", "lrst", flag(imap.StoreFlagsAdd, "$work"), errNoPermission, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			s := e.session()
			if _, err := s.Select(shWork, nil); err != nil {
				t.Fatal(err)
			}
			err := s.Store(nil, imap.UIDSetNum(1), tt.flags, nil)
			wantErr(t, err, tt.want)
			if got := e.flags(e.uids[0]); got != tt.bit {
				t.Fatalf("flags = %d, want %d", got, tt.bit)
			}
		})
	}
}

// Nothing is written when one of several messages needs a right that is held
// by none: the refusal comes before the first write.
func TestSharedStoreRefusalWritesNothing(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrs")
	if err := e.ms.AddFlags(shOwner, "Work", e.uids[1], store.FlagFlagged); err != nil {
		t.Fatal(err)
	}
	s := e.session()
	if _, err := s.Select(shWork, nil); err != nil {
		t.Fatal(err)
	}
	set := imap.UIDSet{}
	set.AddRange(1, 2)
	err := s.Store(nil, set, &imap.StoreFlags{Op: imap.StoreFlagsSet, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil)
	wantErr(t, err, errNoPermission)
	if e.flags(e.uids[0]) != 0 || e.flags(e.uids[1]) != store.FlagFlagged {
		t.Fatalf("a refused STORE changed flags: %d, %d", e.flags(e.uids[0]), e.flags(e.uids[1]))
	}
}

func TestSharedRightExpunge(t *testing.T) {
	// EXPUNGE without e is refused (the nil writer is not CLOSE's zero writer).
	e := newSharedEnv(t)
	e.grant("Work", "lrt")
	s := e.session()
	if _, err := s.Select(shWork, nil); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Expunge(nil, nil), errNoPermission)
	// CLOSE's implicit expunge succeeds and removes nothing.
	if err := e.ms.AddFlags(shOwner, "Work", e.uids[0], store.FlagDeleted); err != nil {
		t.Fatal(err)
	}
	if err := s.Expunge(&imapserver.ExpungeWriter{}, nil); err != nil {
		t.Fatalf("CLOSE without e: %s", errString(err))
	}
	if msgs, _ := e.ms.ListMessages(shOwner, "Work"); len(msgs) != 2 {
		t.Fatalf("owner has %d messages after CLOSE without e, want 2", len(msgs))
	}
}

func TestSharedRightAppend(t *testing.T) {
	for _, tt := range []struct {
		rights string
		want   *imap.Error
	}{{"lrswtek", errNoPermission}, {"li", nil}} {
		t.Run(tt.rights, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			_, err := e.session().Append(shWork, newLiteral([]byte(shRaw)), nil)
			wantErr(t, err, tt.want)
			msgs, _ := e.ms.ListMessages(shOwner, "Work")
			wantN := 2
			if tt.want == nil {
				wantN = 3
			}
			if len(msgs) != wantN {
				t.Fatalf("owner has %d messages, want %d", len(msgs), wantN)
			}
			if own, _ := e.ms.ListMessages(pollTestUser, "INBOX"); len(own) != 0 {
				t.Fatalf("APPEND to a shared folder wrote into the grantee's own mailbox")
			}
		})
	}
}

func TestSharedRightDelete(t *testing.T) {
	for _, tt := range []struct {
		rights string
		want   *imap.Error
	}{{"lrswipkte", errNoPermission}, {"lx", nil}} {
		t.Run(tt.rights, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Private", tt.rights)
			err := e.session().Delete("Other Users/own1@example.test/Private")
			wantErr(t, err, tt.want)
			_, gerr := e.ms.GetMailbox(shOwner, "Private")
			if gone := errors.Is(gerr, store.ErrNotFound); gone != (tt.want == nil) {
				t.Fatalf("folder gone = %v, want %v", gone, tt.want == nil)
			}
			if tt.want == nil {
				if g, _ := e.acl.GetGrant(store.ACLKindMailbox, shOwner, e.ids["Private"], store.GranteeUser(pollTestUser)); g != nil {
					t.Fatal("grant survived the delete")
				}
			}
		})
	}
}

func TestSharedRightRename(t *testing.T) {
	to := "Other Users/own1@example.test/Work/Sub2"
	tests := []struct {
		name         string
		sub, work    string
		newName      string
		want         *imap.Error
		wantRenamed  bool
		sourceMissed bool
	}{
		{"allowed", "lx", "lk", to, nil, true, false},
		{"no x", "lrswipkte", "lk", to, errNoPermission, false, false},
		{"no k on new parent", "lx", "lrswipxte", to, errNoPermission, false, false},
		{"into personal tree", "lx", "lk", "Archive2", errCrossNamespace, false, false},
		{"top level of the owner", "lx", "lk", "Other Users/own1@example.test/Top", errReservedMailboxName, false, false},
		{"other owner's tree", "lx", "lk", "Other Users/own2@example.test/Work/Sub2", errReservedMailboxName, false, false},
		{"onto an existing folder", "lx", "lk", "Other Users/own1@example.test/Work/Sub", errAlreadyExists, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work/Sub", tt.sub)
			e.grant("Work", tt.work)
			err := e.session().Rename(shSub, tt.newName, nil)
			wantErr(t, err, tt.want)
			_, gerr := e.ms.GetMailbox(shOwner, "Work/Sub2")
			if renamed := gerr == nil; renamed != tt.wantRenamed {
				t.Fatalf("renamed = %v, want %v", renamed, tt.wantRenamed)
			}
		})
	}
}

// A personal folder cannot be renamed into the shared tree, as before.
func TestSharedRenamePersonalIntoTreeStaysReserved(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswipkxtea")
	wantErr(t, e.session().Rename("Archive", shWork+"/X", nil), errReservedMailboxName)
}

func TestSharedRightCreateInheritsGrants(t *testing.T) {
	t.Run("without k", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrswipxte")
		wantErr(t, e.session().Create(shWork+"/New", nil), errNoPermission)
		if _, err := e.ms.GetMailbox(shOwner, "Work/New"); err == nil {
			t.Fatal("folder created without k")
		}
	})
	t.Run("with k", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrk")
		e.grantTo(shOther, "Work", "lrs", store.GrantStateAccepted, time.Now())
		wantErr(t, e.session().Create(shWork+"/New", nil), nil)
		id, err := e.ms.EnsureMailboxID(shOwner, "Work/New")
		if err != nil {
			t.Fatal(err)
		}
		for user, want := range map[string]string{pollTestUser: "lrk", shOther: "lrs"} {
			g, err := e.acl.GetGrant(store.ACLKindMailbox, shOwner, id, store.GranteeUser(user))
			if err != nil || g == nil || g.Rights != want || g.State != store.GrantStateAccepted {
				t.Fatalf("grant of %s on the new folder = %+v, %v; want %s accepted", user, g, err, want)
			}
		}
		// The creator can open what they made.
		if _, err := e.session().Select(shWork+"/New", nil); err != nil {
			t.Fatalf("Select of the new folder: %s", errString(err))
		}
	})
	t.Run("pending grants are not copied", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrk")
		e.grantTo(shOther, "Work", "lrs", store.GrantStatePending, time.Now())
		wantErr(t, e.session().Create(shWork+"/New", nil), nil)
		id, err := e.ms.EnsureMailboxID(shOwner, "Work/New")
		if err != nil {
			t.Fatal(err)
		}
		if g, err := e.acl.GetGrant(store.ACLKindMailbox, shOwner, id, store.GranteeUser(shOther)); err == nil && g != nil {
			t.Fatalf("pending grant was copied to the new folder: %+v", g)
		}
	})
	t.Run("existing child", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lk")
		wantErr(t, e.session().Create(shSub, nil), errAlreadyExists)
	})
	t.Run("directly under the owner", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lk")
		wantErr(t, e.session().Create("Other Users/own1@example.test/Top", nil), errReservedMailboxName)
	})
}

func TestSharedCopyMoveOwnerRules(t *testing.T) {
	t.Run("destination without i", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrte")
		e.grant("Work/Sub", "lr")
		s := e.session()
		if _, err := s.Select(shWork, nil); err != nil {
			t.Fatal(err)
		}
		_, err := s.Copy(imap.UIDSetNum(1), shSub)
		wantErr(t, err, errNoPermission)
		wantErr(t, s.Move(nil, imap.UIDSetNum(1), shSub), errNoPermission)
	})
	t.Run("move needs t and e on the source", func(t *testing.T) {
		// Without w, s or t the folder is read-only and MOVE is refused as such.
		for rights, want := range map[string]*imap.Error{"lrw": errNoPermission, "lrt": errNoPermission, "lre": errMailboxReadOnly} {
			e := newSharedEnv(t)
			e.grant("Work", rights)
			e.grant("Work/Sub", "li")
			s := e.session()
			if _, err := s.Select(shWork, nil); err != nil {
				t.Fatal(err)
			}
			wantErr(t, s.Move(nil, imap.UIDSetNum(1), shSub), want)
		}
	})
}

// ---- over the wire ---------------------------------------------------------

func dialShared(t *testing.T, addr string) *imapclient.Client {
	return dialPollTestClient(t, addr, nil)
}

func TestSharedSelectReadOnlyOverTheWire(t *testing.T) {
	for _, tt := range []struct {
		rights   string
		readOnly bool
	}{{"lr", true}, {"lrs", false}, {"lrw", false}, {"lrt", false}, {"lre", true}} {
		t.Run(tt.rights, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			c := dialShared(t, e.startServer())
			data, err := c.Select(shWork, nil).Wait()
			if err != nil {
				t.Fatal(err)
			}
			if data.ReadOnly != tt.readOnly {
				t.Fatalf("ReadOnly = %v, want %v", data.ReadOnly, tt.readOnly)
			}
			if data.NumMessages != 2 {
				t.Fatalf("NumMessages = %d, want 2", data.NumMessages)
			}
			if got := slices.Contains(data.PermanentFlags, `\*`); got != strings.Contains(tt.rights, "w") {
				t.Fatalf("PERMANENTFLAGS %v for rights %s: arbitrary keywords offered = %v", data.PermanentFlags, tt.rights, got)
			}
		})
	}
	t.Run("personal stays read-write", func(t *testing.T) {
		e := newSharedEnv(t)
		c := dialShared(t, e.startServer())
		data, err := c.Select("INBOX", nil).Wait()
		if err != nil || data.ReadOnly || !slices.Contains(data.PermanentFlags, `\*`) {
			t.Fatalf("personal SELECT = %+v, %v", data, err)
		}
	})
}

func TestSharedFetchStoreExpungeOverTheWire(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswtea")
	c := dialShared(t, e.startServer())
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}

	// A non-PEEK body fetch sets \Seen on the owner's copy.
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(e.uids[0])), &imap.FetchOptions{
		UID: true, BodySection: []*imap.FetchItemBodySection{{}},
	}).Collect()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("FETCH = %v, %v", msgs, err)
	}
	if e.flags(e.uids[0])&store.FlagSeen == 0 {
		t.Fatal("the owner does not see \\Seen set by the grantee")
	}

	// \Deleted by the grantee, then EXPUNGE removes it from the owner's folder.
	if err := c.Store(imap.UIDSetNum(imap.UID(e.uids[1])), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Expunge().Collect(); err != nil {
		t.Fatalf("EXPUNGE: %v", err)
	}
	if left, _ := e.ms.ListMessages(shOwner, "Work"); len(left) != 1 {
		t.Fatalf("owner has %d messages, want 1", len(left))
	}
}

func TestSharedPeekDoesNotSetSeenWithoutRight(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrw")
	c := dialShared(t, e.startServer())
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(imap.UIDSetNum(imap.UID(e.uids[0])), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{}}}).Collect(); err != nil {
		t.Fatalf("FETCH: %v", err)
	}
	if e.flags(e.uids[0])&store.FlagSeen != 0 {
		t.Fatal("\\Seen set without the s right")
	}
}

func TestSharedCloseWithoutExpungeRight(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrt")
	c := dialShared(t, e.startServer())
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.UIDSetNum(imap.UID(e.uids[0])), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Expunge().Collect(); !hasCode(err, imap.ResponseCodeNoPerm) {
		t.Fatalf("EXPUNGE without e = %v, want NOPERM", err)
	}
	if err := c.UIDExpunge(imap.UIDSetNum(imap.UID(e.uids[0]))).Close(); !hasCode(err, imap.ResponseCodeNoPerm) {
		t.Fatalf("UID EXPUNGE without e = %v, want NOPERM", err)
	}
	// CLOSE succeeds and expunges nothing.
	if err := c.UnselectAndExpunge().Wait(); err != nil {
		t.Fatalf("CLOSE: %v", err)
	}
	if left, _ := e.ms.ListMessages(shOwner, "Work"); len(left) != 2 {
		t.Fatalf("owner has %d messages, want 2", len(left))
	}
}

func TestSharedCopyMoveWithinOwnerOverTheWire(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswtea")
	e.grant("Work/Sub", "li")
	c := dialShared(t, e.startServer())
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Copy(imap.UIDSetNum(imap.UID(e.uids[0])), shSub).Wait(); err != nil {
		t.Fatalf("COPY: %v", err)
	}
	if _, err := c.Move(imap.UIDSetNum(imap.UID(e.uids[1])), shSub).Wait(); err != nil {
		t.Fatalf("MOVE: %v", err)
	}
	if sub, _ := e.ms.ListMessages(shOwner, "Work/Sub"); len(sub) != 2 {
		t.Fatalf("Work/Sub has %d messages, want 2", len(sub))
	}
	if work, _ := e.ms.ListMessages(shOwner, "Work"); len(work) != 1 {
		t.Fatalf("Work has %d messages, want 1", len(work))
	}
	if own, _ := e.ms.ListMessages(pollTestUser, "INBOX"); len(own) != 0 {
		t.Fatal("grantee's own mailbox changed")
	}
}

// A revoke or downgrade takes effect on the next command of an open session.
func TestSharedRevokeAndDowngradeOverTheWire(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrsw")
	c := dialShared(t, e.startServer())
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	fetch := func() error {
		_, err := c.Fetch(imap.UIDSetNum(imap.UID(e.uids[0])), &imap.FetchOptions{UID: true, Flags: true}).Collect()
		return err
	}
	store1 := func() error {
		return c.Store(imap.UIDSetNum(imap.UID(e.uids[0])), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagFlagged}}, nil).Close()
	}
	if err := fetch(); err != nil {
		t.Fatalf("FETCH before: %v", err)
	}
	// Downgrade: w goes, STORE is refused at once, FETCH still works.
	e.revoke("Work")
	e.grant("Work", "lrs")
	if err := store1(); !hasCode(err, imap.ResponseCodeNoPerm) {
		t.Fatalf("STORE after downgrade = %v, want NOPERM", err)
	}
	if err := fetch(); err != nil {
		t.Fatalf("FETCH after downgrade: %v", err)
	}
	// Revoke: both are refused as a missing mailbox.
	e.revoke("Work")
	for name, f := range map[string]func() error{"FETCH": fetch, "STORE": store1} {
		if err := f(); !hasCode(err, imap.ResponseCodeNonExistent) {
			t.Fatalf("%s after revoke = %v, want NONEXISTENT", name, err)
		}
	}
	if e.flags(e.uids[0])&store.FlagFlagged != 0 {
		t.Fatal("a refused STORE wrote")
	}
}

// IDLE re-reads the grant on its timer. go-imap cannot interrupt the wait for
// DONE, so what the client observes is that pushes stop at the recheck and the
// IDLE is answered as a missing mailbox when it ends.
func TestSharedIdleStopsWhenAccessIsLost(t *testing.T) {
	old := idleRecheckInterval
	idleRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { idleRecheckInterval = old })

	e := newSharedEnv(t)
	e.grant("Work", "lr")
	updates := &mailboxUpdates{}
	c := dialPollTestClient(t, e.startServer(), updates)
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	idle, err := c.Idle()
	if err != nil {
		t.Fatal(err)
	}
	e.revoke("Work")
	time.Sleep(400 * time.Millisecond)
	if _, err := e.ms.DeliverMessage(shOwner, "Work", &bongov1.MailMeta{Guid: "g9", Subject: "new", ReceivedAt: time.Now().Unix()}, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := updates.exists(); len(got) != 0 {
		t.Fatalf("IDLE pushed %v after the grant was revoked", got)
	}
	if err := idle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := idle.Wait(); !hasCode(err, imap.ResponseCodeNonExistent) {
		t.Fatalf("IDLE ended with %v, want NONEXISTENT", err)
	}
}

// IDLE on a shared folder is told about the owner's new mail.
func TestSharedIdleSeesNewMail(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lr")
	updates := &mailboxUpdates{}
	c := dialPollTestClient(t, e.startServer(), updates)
	if _, err := c.Select(shWork, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	idle, err := c.Idle()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idle.Close() }()
	if _, err := e.ms.DeliverMessage(shOwner, "Work", &bongov1.MailMeta{Guid: "g9", Subject: "new", ReceivedAt: time.Now().Unix()}, 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(updates.exists(), uint32(3)) {
		if time.Now().After(deadline) {
			t.Fatalf("no EXISTS 3, got %v", updates.exists())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---- LIST and LSUB ---------------------------------------------------------

var listLine = regexp.MustCompile(`^\* (LIST|LSUB) \(([^)]*)\) "/" (.*)$`)

func (c *rawIMAPConn) listing(verb, ref, pattern string) map[string]string {
	c.t.Helper()
	untagged := c.exec(`%s "%s" "%s"`, verb, ref, pattern)
	out := map[string]string{}
	for _, line := range untagged {
		m := listLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out[strings.Trim(m[3], `"`)] = m[2]
	}
	return out
}

func names(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestSharedList(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lr")
	e.grant("Work/Sub", "l")
	e.grantTo(pollTestUser, "Private", "lr", store.GrantStatePending, time.Now())
	c := dialRawIMAP(t, e.startServer())

	tests := []struct {
		ref, pattern string
		want         []string
	}{
		{"", "*", []string{"Archive", "INBOX", "Other Users", "Other Users/own1@example.test", shWork, shSub}},
		{"", "%", []string{"Archive", "INBOX", "Other Users"}},
		{"", "Other Users/%", []string{"Other Users/own1@example.test"}},
		{"", "Other Users/*", []string{"Other Users/own1@example.test", shWork, shSub}},
		{"", "%/%", []string{"Other Users/own1@example.test"}},
		{"", "Other Users/own1@example.test/%", []string{shWork}},
		{"", "Other Users/own1@example.test/*", []string{shWork, shSub}},
		{"", "Other Users/%/%", []string{shWork}},
		{"Other Users/", "%", []string{"Other Users/own1@example.test"}},
		{"Other Users/own1@example.test/", "*", []string{shWork, shSub}},
	}
	for _, tt := range tests {
		t.Run(tt.ref+"|"+tt.pattern, func(t *testing.T) {
			got := names(c.listing("LIST", tt.ref, tt.pattern))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("LIST %q %q = %v, want %v", tt.ref, tt.pattern, got, tt.want)
			}
		})
	}

	all := c.listing("LIST", "", "*")
	for name, attrs := range map[string]string{
		"Other Users":                   `\Noselect \HasChildren`,
		"Other Users/own1@example.test": `\Noselect \HasChildren`,
		shWork:                          `\HasChildren`,
		shSub:                           `\HasNoChildren`,
	} {
		if !strings.Contains(all[name], attrs) {
			t.Errorf("%s attributes = %q, want %q", name, all[name], attrs)
		}
	}
	if strings.Contains(all[shWork], `\Noselect`) || strings.Contains(all[shSub], `\Noselect`) {
		t.Errorf("a selectable shared folder is marked \\Noselect")
	}
}

// LIST is unchanged for a user who holds no shares.
func TestSharedListUnchangedWithoutShares(t *testing.T) {
	e := newSharedEnv(t)
	c := dialRawIMAP(t, e.startServer())
	if got := names(c.listing("LIST", "", "*")); !slices.Equal(got, []string{"Archive", "INBOX"}) {
		t.Fatalf("LIST = %v", got)
	}
}

func TestSharedListHidesWhatIsNotVisible(t *testing.T) {
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	for name, setup := range map[string]func(e *sharedEnv){
		"pending":         func(e *sharedEnv) { e.grantTo(pollTestUser, "Work", "lr", store.GrantStatePending, time.Now()) },
		"expired pending": func(e *sharedEnv) { e.grantTo(pollTestUser, "Work", "lr", store.GrantStatePending, old) },
		"without l":       func(e *sharedEnv) { e.grant("Work", "rswipkxtea") },
		"none":            func(e *sharedEnv) {},
	} {
		t.Run(name, func(t *testing.T) {
			e := newSharedEnv(t)
			setup(e)
			c := dialRawIMAP(t, e.startServer())
			for _, name := range names(c.listing("LIST", "", "*")) {
				if strings.Contains(name, "Other Users") {
					t.Fatalf("LIST shows %q", name)
				}
			}
		})
	}
}

func TestSharedSubscribeLSUB(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lr")
	e.grant("Work/Sub", "lr")
	c := dialRawIMAP(t, e.startServer())

	// Subscribed by default: no SUBSCRIBE needed, in LSUB and LIST (SUBSCRIBED).
	got := c.listing("LSUB", "", "*")
	for _, n := range []string{shWork, shSub} {
		if _, ok := got[n]; !ok {
			t.Fatalf("LSUB = %v, want %s without SUBSCRIBE", names(got), n)
		}
	}
	sub := c.listing("LIST (SUBSCRIBED)", "", "Other Users/*")
	if attrs, ok := sub[shWork]; !ok || !strings.Contains(attrs, "Subscribed") {
		t.Fatalf("LIST (SUBSCRIBED) = %v, want %s with \\Subscribed", sub, shWork)
	}

	// UNSUBSCRIBE hides it from LSUB only; plain LIST still shows it.
	c.exec(`UNSUBSCRIBE "%s"`, shWork)
	if _, ok := c.listing("LSUB", "", "*")[shWork]; ok {
		t.Fatal("still in LSUB after UNSUBSCRIBE")
	}
	if _, ok := c.listing("LIST", "", "Other Users/*")[shWork]; !ok {
		t.Fatal("plain LIST lost the unsubscribed folder")
	}
	if _, ok := c.listing("LSUB", "", "*")[shSub]; !ok {
		t.Fatal("UNSUBSCRIBE of one folder hid another")
	}

	// The owner renames the folder: the opt-out follows the id.
	if err := e.ms.RenameMailbox(shOwner, "Work", "Projects"); err != nil {
		t.Fatal(err)
	}
	renamed := "Other Users/own1@example.test/Projects"
	got = c.listing("LSUB", "", "*")
	if _, ok := got[renamed]; ok {
		t.Fatalf("LSUB after rename = %v, want %s still unsubscribed", names(got), renamed)
	}

	// A rights change from Read to Write keeps it unsubscribed.
	e.grant("Work", "lrswite")
	if _, ok := c.listing("LSUB", "", "*")[renamed]; ok {
		t.Fatal("rights change re-subscribed the folder")
	}

	// SUBSCRIBE brings it back; a revoke hides it.
	c.exec(`SUBSCRIBE "%s"`, renamed)
	if _, ok := c.listing("LSUB", "", "*")[renamed]; !ok {
		t.Fatal("not in LSUB after SUBSCRIBE")
	}
	e.revoke("Work")
	if _, ok := c.listing("LSUB", "", "*")[renamed]; ok {
		t.Fatalf("LSUB shows %q after the grant was revoked", renamed)
	}
	if st := c.execStatus(`SUBSCRIBE "%s"`, renamed); !strings.HasPrefix(st, "NO") {
		t.Fatalf("SUBSCRIBE without a grant = %q", st)
	}
	if st := c.execStatus(`UNSUBSCRIBE "%s"`, renamed); !strings.HasPrefix(st, "OK") {
		t.Fatalf("UNSUBSCRIBE without a grant = %q", st)
	}
	// No marker for a folder the user cannot see.
	if _, err := e.ps.Get([]byte("/user/" + pollTestUser + "/sharedunsub/" + shOwner + "/" + e.ids["Work"])); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("marker written without a grant: %v", err)
	}
}

// Every way of holding an accepted lookup right lists in LSUB with no
// SUBSCRIBE; a pending grant and a grant without l do not.
func TestSharedLSUBByDefaultPerGrantPath(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *sharedEnv)
		want  bool
	}{
		{"direct accepted", func(t *testing.T, e *sharedEnv) { e.grant("Work", "lr") }, true},
		{"group", func(t *testing.T, e *sharedEnv) {
			if err := e.ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
				t.Fatal(err)
			}
			g, err := store.NewGroupStore(e.ps, e.applier).CreateGroup(&bongov1.Group{Address: "team@example.com", DisplayName: "Team", MemberIds: []string{pollTestUser}})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOwner, e.ids["Work"], store.GranteeGroup(g.Id), "", "lr",
				store.GrantStateAccepted, shOwner, time.Now()); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"shared inbox member", func(t *testing.T, e *sharedEnv) {
			p, err := proto.Marshal(&bongov1.UserProfile{Email: shOwnerAddr, Kind: store.SharedInboxKind, SharedMembers: []string{store.GranteeUser(pollTestUser)}})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.ps.Set([]byte("/user/"+shOwner+"/profile"), p); err != nil {
				t.Fatal(err)
			}
			if err := store.SyncSharedInboxGrants(e.acl, e.ms, shOwner, []string{store.GranteeUser(pollTestUser)}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"pending", func(t *testing.T, e *sharedEnv) {
			e.grantTo(pollTestUser, "Work", "lr", store.GrantStatePending, time.Now())
		}, false},
		{"no lookup right", func(t *testing.T, e *sharedEnv) { e.grant("Work", "r") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSharedEnv(t)
			tt.setup(t, e)
			rights, err := e.acl.MailboxRights(shOwner, e.ids["Work"], pollTestUser, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if tt.want && !strings.Contains(rights, "l") {
				t.Fatalf("setup left no l right (%q): positive case would be vacuous", rights)
			}
			c := dialRawIMAP(t, e.startServer())
			_, ok := c.listing("LSUB", "", "*")[shWork]
			if ok != tt.want {
				t.Fatalf("LSUB lists %s = %v, want %v", shWork, ok, tt.want)
			}
		})
	}
}

func TestSharedListReturnStatusSkipsUnreadable(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lr")
	e.grant("Work/Sub", "l")
	c := dialRawIMAP(t, e.startServer())
	untagged := c.exec(`LIST "" "Other Users/own1@example.test/*" RETURN (STATUS (MESSAGES))`)
	var withStatus []string
	for _, l := range untagged {
		if strings.HasPrefix(l, "* STATUS ") {
			withStatus = append(withStatus, l)
		}
	}
	if len(withStatus) != 1 || !strings.Contains(withStatus[0], "Work") || strings.Contains(withStatus[0], "Work/Sub") {
		t.Fatalf("STATUS lines = %v, want only Work", withStatus)
	}
}

// GETQUOTAROOT on a shared name goes through the resolver.
func TestSharedQuotaRootUsesResolver(t *testing.T) {
	e := newSharedEnv(t)
	s := e.session()
	_, err := s.GetQuotaRoot(shWork)
	wantErr(t, err, errNoSuchMailbox)
	e.grant("Work", "l")
	roots, err := s.GetQuotaRoot(shWork)
	if err != nil || len(roots) != 0 {
		t.Fatalf("GetQuotaRoot = %v, %v; want none", roots, err)
	}
}

// With no ACL store nothing under the prefix resolves.
func TestSharedFailsClosedWithoutACLStore(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswipkxtea")
	s := e.session()
	s.acl = nil
	_, err := s.Select(shWork, nil)
	wantErr(t, err, errNoSuchMailbox)
}

func TestSharedAppendMasksFlags(t *testing.T) {
	all := []imap.Flag{imap.FlagSeen, imap.FlagDeleted, imap.FlagFlagged, imap.FlagAnswered, "$custom"}
	for _, tt := range []struct {
		rights string
		want   uint32
	}{
		{"li", 0},
		{"lis", store.FlagSeen},
		{"lit", store.FlagDeleted},
		{"liw", store.FlagFlagged | store.FlagAnswered},
		{"liswt", store.FlagSeen | store.FlagDeleted | store.FlagFlagged | store.FlagAnswered},
	} {
		t.Run(tt.rights, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			if _, err := e.session().Append(shWork, newLiteral([]byte(shRaw)), &imap.AppendOptions{Flags: all}); err != nil {
				t.Fatal(errString(err))
			}
			msgs, _ := e.ms.ListMessages(shOwner, "Work")
			if len(msgs) != 3 {
				t.Fatalf("owner has %d messages, want 3", len(msgs))
			}
			var got uint32
			for _, m := range msgs {
				if m.Uid != e.uids[0] && m.Uid != e.uids[1] {
					got = m.Flags
				}
			}
			if got != tt.want {
				t.Fatalf("stored flags = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSharedCloseAfterRevokeSucceeds(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrstek")
	s := e.session()
	if _, err := s.Select(shWork, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.ms.AddFlags(shOwner, "Work", e.uids[0], store.FlagDeleted); err != nil {
		t.Fatal(err)
	}
	e.revoke("Work")
	if err := s.Expunge(&imapserver.ExpungeWriter{}, nil); err != nil {
		t.Fatalf("CLOSE after revoke: %s", errString(err))
	}
	if msgs, _ := e.ms.ListMessages(shOwner, "Work"); len(msgs) != 2 {
		t.Fatalf("owner has %d messages after CLOSE with the grant revoked, want 2", len(msgs))
	}
}

func TestSharedFolderViaGroup(t *testing.T) {
	e := newSharedEnv(t)
	if err := e.ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	gs := store.NewGroupStore(e.ps, e.applier)
	g, err := gs.CreateGroup(&bongov1.Group{Address: "team@example.com", DisplayName: "Team", MemberIds: []string{pollTestUser}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOwner, e.ids["Work"], store.GranteeGroup(g.Id), "", "lr",
		store.GrantStateAccepted, shOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.session().Select(shWork, nil); err != nil {
		t.Fatalf("member Select: %s", errString(err))
	}
	outsider := e.session()
	outsider.userID = shOther
	_, err = outsider.Select(shWork, nil)
	wantErr(t, err, errNoSuchMailbox)
}
