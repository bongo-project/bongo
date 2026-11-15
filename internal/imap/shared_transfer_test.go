package imap

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ---- helpers ---------------------------------------------------------------

// multiAuth logs in several accounts, all with the shared test password.
type multiAuth map[string]string

func (m multiAuth) AuthenticateProtocol(username, password string) (string, *bongov1.UserProfile, string, error) {
	id, ok := m[username]
	if !ok || password != pollTestPassword {
		return "", nil, "", errors.New("invalid credentials")
	}
	return id, &bongov1.UserProfile{}, "", nil
}

func dialSharedAs(t *testing.T, addr, username string, updates *mailboxUpdates) *imapclient.Client {
	t.Helper()
	var opts *imapclient.Options
	if updates != nil {
		opts = &imapclient.Options{UnilateralDataHandler: updates.handler()}
	}
	c, err := imapclient.DialInsecure(addr, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Login(username, pollTestPassword).Wait(); err != nil {
		t.Fatalf("Login(%s): %v", username, err)
	}
	return c
}

// stubQuota answers per destination owner and records what it was asked.
type stubQuota struct {
	mu    sync.Mutex
	over  map[string]bool
	err   error
	asked map[string]int64
}

func (q *stubQuota) RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.asked == nil {
		q.asked = map[string]int64{}
	}
	q.asked[userID] += addBytes
	return q.over[userID], q.err
}

// sessionWithTags is a session that also knows the tag definitions, as the
// server wires it.
func (e *sharedEnv) sessionWithTags() *session {
	s := e.session()
	s.tags = store.NewTagStore(e.ps, e.applier)
	return s
}

func (e *sharedEnv) mailMeta(user, guid string) *bongov1.MailMeta {
	e.t.Helper()
	data, err := e.ps.Get([]byte("/user/" + user + "/mail/" + guid))
	if err != nil {
		e.t.Fatalf("MailMeta %s/%s: %v", user, guid, err)
	}
	var m bongov1.MailMeta
	if err := proto.Unmarshal(data, &m); err != nil {
		e.t.Fatal(err)
	}
	return &m
}

func (e *sharedEnv) hasMailMeta(user, guid string) bool {
	_, err := e.ps.Get([]byte("/user/" + user + "/mail/" + guid))
	return err == nil
}

// presetOwnerTags gives one of the owner's Work messages tags, in both the
// MailMeta and the denormalised MailboxMessage.
func (e *sharedEnv) presetOwnerTags(i int, tags ...string) {
	e.t.Helper()
	m := e.mailMeta(shOwner, fmt.Sprintf("g%d", i))
	m.Tags = tags
	if err := e.ms.StoreMailMeta(shOwner, m); err != nil {
		e.t.Fatal(err)
	}
	if err := e.ms.WriteMessageTags(shOwner, "Work", e.uids[i], tags); err != nil {
		e.t.Fatal(err)
	}
}

// ownerTags returns the tags on the owner's message i in the MailMeta and in the
// MailboxMessage, which must agree.
func (e *sharedEnv) ownerTags(i int) []string {
	e.t.Helper()
	meta := e.mailMeta(shOwner, fmt.Sprintf("g%d", i)).Tags
	msg, err := e.ms.GetMessage(shOwner, "Work", e.uids[i])
	if err != nil {
		e.t.Fatal(err)
	}
	if len(meta) != len(msg.Tags) || (len(meta) > 0 && !slices.Equal(meta, msg.Tags)) {
		e.t.Fatalf("MailMeta tags %v and MailboxMessage tags %v differ", meta, msg.Tags)
	}
	return meta
}

func (e *sharedEnv) tagDefined(user, name string) bool {
	_, err := e.ps.Get([]byte("/user/" + user + "/tag/" + name))
	return err == nil
}

// deliverOwn puts a message with a real blob into the grantee's own folder.
func (e *sharedEnv) deliverOwn(mailbox, guid string, flags uint32) uint32 {
	e.t.Helper()
	hash, err := e.blobs.Put([]byte(shRaw))
	if err != nil {
		e.t.Fatal(err)
	}
	uid, err := e.ms.DeliverMessage(pollTestUser, mailbox, &bongov1.MailMeta{
		Guid: guid, Subject: "mine", ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(shRaw)), ContentType: "message/rfc822"},
	}, flags)
	if err != nil {
		e.t.Fatal(err)
	}
	return uid
}

// otherBox creates own2's Box and grants the grantee rights on it. It returns
// the name the grantee sees.
func (e *sharedEnv) otherBox(rights string) string {
	e.t.Helper()
	if err := e.ms.CreateMailbox(shOther, "Box"); err != nil {
		e.t.Fatal(err)
	}
	id, err := e.ms.EnsureMailboxID(shOther, "Box")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.acl.SetGrantKeepState(store.ACLKindMailbox, shOther, id, store.GranteeUser(pollTestUser), "", rights, store.GrantStateAccepted, shOther, time.Now()); err != nil {
		e.t.Fatal(err)
	}
	return "Other Users/own2@example.test/Box"
}

func (e *sharedEnv) count(user, mailbox string) int {
	e.t.Helper()
	msgs, err := e.ms.ListMessages(user, mailbox)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(msgs)
}

func (e *sharedEnv) selectOn(s *session, name string) {
	e.t.Helper()
	if _, err := s.Select(name, nil); err != nil {
		e.t.Fatalf("Select(%s): %s", name, errString(err))
	}
}

func storeFlags(op imap.StoreFlagsOp, f ...imap.Flag) *imap.StoreFlags {
	return &imap.StoreFlags{Op: op, Silent: true, Flags: f}
}

// ---- keywords on shared folders ----------------------------------------------

func TestSharedKeywordsChangeTheOwnersTags(t *testing.T) {
	tests := []struct {
		name     string
		rights   string
		preset   []string
		flags    *imap.StoreFlags
		want     *imap.Error
		wantTags []string
	}{
		{"add with w", "lrw", nil, storeFlags(imap.StoreFlagsAdd, "$work"), nil, []string{"work"}},
		{"add without w", "lrst", nil, storeFlags(imap.StoreFlagsAdd, "$work"), errNoPermission, nil},
		{"remove with w", "lrw", []string{"work", "other"}, storeFlags(imap.StoreFlagsDel, "$work"), nil, []string{"other"}},
		{"remove without w", "lrs", []string{"work"}, storeFlags(imap.StoreFlagsDel, "$work"), errNoPermission, []string{"work"}},
		{"replace with w", "lrw", []string{"old"}, storeFlags(imap.StoreFlagsSet, "$new"), nil, []string{"new"}},
		{"replace clearing with w", "lrws", []string{"old"}, storeFlags(imap.StoreFlagsSet, imap.FlagSeen), nil, nil},
		{"replace changing keywords without w", "lrs", []string{"old"}, storeFlags(imap.StoreFlagsSet, imap.FlagSeen), errNoPermission, []string{"old"}},
		{"replace keeping keywords without w", "lrs", []string{"old"}, storeFlags(imap.StoreFlagsSet, imap.FlagSeen, "$old"), nil, []string{"old"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			if tt.preset != nil {
				e.presetOwnerTags(0, tt.preset...)
			}
			s := e.sessionWithTags()
			e.selectOn(s, shWork)
			wantErr(t, s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), tt.flags, nil), tt.want)
			got := e.ownerTags(0)
			if len(got) != len(tt.wantTags) || (len(got) > 0 && !slices.Equal(got, tt.wantTags)) {
				t.Fatalf("owner tags = %v, want %v", got, tt.wantTags)
			}
			// The keyword is the owner's tag, never the grantee's.
			if tt.want == nil && len(tt.wantTags) > 0 && tt.preset == nil {
				if !e.tagDefined(shOwner, tt.wantTags[0]) {
					t.Fatal("the owner has no tag definition for the new keyword")
				}
			}
			for _, name := range []string{"work", "new", "old", "other"} {
				if e.tagDefined(pollTestUser, name) {
					t.Fatalf("the grantee gained a tag %q", name)
				}
			}
		})
	}
}

// A failed write of the denormalised tags on a shared folder fails the STORE:
// the MailMeta is already written, so an OK would hide that the two disagree.
// Own folders keep warn-and-continue.
func TestSharedKeywordTagWriteFailureFailsTheStore(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			e := newSharedEnv(t)
			s := e.sessionWithTags()
			mailbox := "Archive"
			uid := e.deliverOwn("Archive", "mine1", 0)
			if shared {
				e.grant("Work", "lrw")
				mailbox, uid = shWork, e.uids[0]
			}
			e.selectOn(s, mailbox)
			e.applier.failPut = func(cmd *bongov1.RaftCommand) error {
				if strings.Contains(string(cmd.Key), "/msg/") {
					return errors.New("write refused")
				}
				return nil
			}
			err := s.Store(nil, imap.UIDSetNum(imap.UID(uid)), storeFlags(imap.StoreFlagsAdd, "$work"), nil)
			if shared && err == nil {
				t.Fatal("STORE answered OK although the tags write failed")
			}
			if !shared && err != nil {
				t.Fatalf("own folder: %s, want warn-and-continue", errString(err))
			}
		})
	}
}

// An FSM refusal reaches the client as one fixed NO, never the store's text.
func TestSharedTransferRefusalAnswersFixedText(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lri")
	e.deliverOwn("Archive", "m1", 0)
	s := e.session()
	e.selectOn(s, "Archive")
	e.applier.failPut = func(cmd *bongov1.RaftCommand) error {
		if strings.HasPrefix(string(cmd.Key), "/__mailbox_xfer__/") {
			return errors.New("guid collision on 0123abcd in /user/owner/mail")
		}
		return nil
	}
	_, err := s.Copy(imap.UIDSetNum(1), shWork)
	wantErr(t, err, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Copy or move to the shared folder failed"})
}

// A keyword that would be the owner's 101st tag is refused and nothing is
// written: not the MailMeta, not a tag definition.
func TestSharedKeywordAtTheOwnersTagLimit(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrsw")
	ts := store.NewTagStore(e.ps, e.applier)
	for i := range store.MaxTagsPerUser {
		if _, err := ts.CreateTag(shOwner, fmt.Sprintf("t%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	s := e.sessionWithTags()
	e.selectOn(s, shWork)

	wantErr(t, s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsAdd, "$fresh"), nil), errSharedKeywords)
	if got := e.ownerTags(0); len(got) != 0 {
		t.Fatalf("owner tags = %v after a refused STORE", got)
	}
	if e.tagDefined(shOwner, "fresh") {
		t.Fatal("a refused STORE created the owner's tag")
	}
	// FLAGS replace is held to the same limit.
	wantErr(t, s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsSet, "$fresh"), nil), errSharedKeywords)
	if got := e.ownerTags(0); len(got) != 0 {
		t.Fatalf("owner tags = %v after a refused FLAGS", got)
	}
	// A keyword the owner already defines is not a new tag.
	wantErr(t, s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsAdd, "$t5"), nil), nil)
	if got := e.ownerTags(0); !slices.Equal(got, []string{"t5"}) {
		t.Fatalf("owner tags = %v, want [t5]", got)
	}
}

func TestSharedKeywordRefusalsWriteNothing(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrw")
		s := e.sessionWithTags()
		e.selectOn(s, shWork)
		wantErr(t, s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsAdd, "$Bad_Name"), nil), errSharedKeywords)
		if got := e.ownerTags(0); len(got) != 0 {
			t.Fatalf("owner tags = %v", got)
		}
	})
	t.Run("per message limit on the second message", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrw")
		var full []string
		for i := range store.MaxTagsPerMessage {
			full = append(full, fmt.Sprintf("p%d", i))
		}
		e.presetOwnerTags(1, full...)
		s := e.sessionWithTags()
		e.selectOn(s, shWork)
		set := imap.UIDSet{}
		set.AddRange(imap.UID(e.uids[0]), imap.UID(e.uids[1]))
		wantErr(t, s.Store(nil, set, storeFlags(imap.StoreFlagsAdd, "$extra"), nil), errSharedKeywords)
		if got := e.ownerTags(0); len(got) != 0 {
			t.Fatalf("the first message gained tags %v although the second was refused", got)
		}
		if e.tagDefined(shOwner, "extra") {
			t.Fatal("a refused STORE created the owner's tag")
		}
	})
	t.Run("flags are not written either", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrsw")
		s := e.sessionWithTags()
		e.selectOn(s, shWork)
		wantErr(t, s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsAdd, imap.FlagSeen, "$Bad_Name"), nil), errSharedKeywords)
		if e.flags(e.uids[0]) != 0 {
			t.Fatal("the flags were written although the keyword was refused")
		}
	})
}

// PERMANENTFLAGS offers arbitrary keywords only to a user who may set them.
func TestSharedPermanentFlagsOfferKeywordsWithW(t *testing.T) {
	for _, tt := range []struct {
		rights   string
		readOnly bool
		want     bool
	}{{"lrw", false, true}, {"lrs", false, false}, {"lrw", true, false}} {
		e := newSharedEnv(t)
		e.grant("Work", tt.rights)
		data, err := e.session().Select(shWork, &imap.SelectOptions{ReadOnly: tt.readOnly})
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(data.PermanentFlags, imap.Flag(`\*`)); got != tt.want {
			t.Fatalf("rights %s readOnly %v: \\* offered = %v, want %v", tt.rights, tt.readOnly, got, tt.want)
		}
	}
}

// ---- COPY across owners ------------------------------------------------------

func TestSharedCopyAcrossOwners(t *testing.T) {
	t.Run("shared to own keeps flags and drops the owner's tags", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lr")
		e.presetOwnerTags(0, "private")
		if err := e.ms.AddFlags(shOwner, "Work", e.uids[0], store.FlagSeen|store.FlagFlagged); err != nil {
			t.Fatal(err)
		}
		s := e.session()
		e.selectOn(s, shWork)

		data, err := s.Copy(imap.UIDSetNum(imap.UID(e.uids[0])), "Archive")
		if err != nil {
			t.Fatal(errString(err))
		}
		msgs, _ := e.ms.ListMessages(pollTestUser, "Archive")
		if len(msgs) != 1 {
			t.Fatalf("Archive holds %d messages, want 1", len(msgs))
		}
		m := msgs[0]
		if m.Flags != store.FlagSeen|store.FlagFlagged || len(m.Tags) != 0 || m.MailGuid == "g0" {
			t.Fatalf("copy = flags %d tags %v guid %q", m.Flags, m.Tags, m.MailGuid)
		}
		own := e.mailMeta(pollTestUser, m.MailGuid)
		src := e.mailMeta(shOwner, "g0")
		if own.RawMessage.GetHash() != src.RawMessage.GetHash() || len(own.Tags) != 0 {
			t.Fatalf("copy meta hash %q tags %v", own.RawMessage.GetHash(), own.Tags)
		}
		if got := e.ownerTags(0); !slices.Equal(got, []string{"private"}) {
			t.Fatalf("the owner's tags changed to %v", got)
		}
		mb, _ := e.ms.GetMailbox(pollTestUser, "Archive")
		if data.UIDValidity != mb.UidValidity || data.DestUIDs.String() != "1" || data.SourceUIDs.String() != fmt.Sprint(e.uids[0]) {
			t.Fatalf("COPYUID = %d %s %s, want %d 1 %d", data.UIDValidity, data.SourceUIDs, data.DestUIDs, mb.UidValidity, e.uids[0])
		}
		if e.count(shOwner, "Work") != 2 {
			t.Fatal("the source lost a message")
		}
	})

	t.Run("own to shared masks flags by the destination's rights", func(t *testing.T) {
		for _, tt := range []struct {
			rights string
			want   uint32
		}{{"lri", 0}, {"lris", store.FlagSeen}, {"lrisw", store.FlagSeen | store.FlagFlagged}} {
			e := newSharedEnv(t)
			e.grant("Work", tt.rights)
			own := e.deliverOwn("Archive", "mine1", store.FlagSeen|store.FlagFlagged)
			s := e.session()
			e.selectOn(s, "Archive")

			data, err := s.Copy(imap.UIDSetNum(imap.UID(own)), shWork)
			if err != nil {
				t.Fatalf("%s: %s", tt.rights, errString(err))
			}
			msgs, _ := e.ms.ListMessages(shOwner, "Work")
			if len(msgs) != 3 {
				t.Fatalf("%s: Work holds %d messages, want 3", tt.rights, len(msgs))
			}
			got := msgs[2]
			if got.Flags != tt.want {
				t.Fatalf("%s: flags = %d, want %d", tt.rights, got.Flags, tt.want)
			}
			// The copy is a record in the OWNER's namespace.
			if !e.hasMailMeta(shOwner, got.MailGuid) || got.MailGuid == "mine1" {
				t.Fatalf("%s: no new MailMeta in the owner's account for %q", tt.rights, got.MailGuid)
			}
			mb, _ := e.ms.GetMailbox(shOwner, "Work")
			if data.UIDValidity != mb.UidValidity || data.DestUIDs.String() != fmt.Sprint(got.Uid) {
				t.Fatalf("%s: COPYUID = %d %s, want %d %d", tt.rights, data.UIDValidity, data.DestUIDs, mb.UidValidity, got.Uid)
			}
		}
	})

	t.Run("one owner to another", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lr")
		box := e.otherBox("li")
		s := e.session()
		e.selectOn(s, shWork)
		if _, err := s.Copy(imap.UIDSetNum(imap.UID(e.uids[0])), box); err != nil {
			t.Fatal(errString(err))
		}
		msgs, _ := e.ms.ListMessages(shOther, "Box")
		if len(msgs) != 1 || !e.hasMailMeta(shOther, msgs[0].MailGuid) {
			t.Fatalf("own2 Box = %+v", msgs)
		}
	})
}

func TestSharedCopyAcrossOwnersRefusals(t *testing.T) {
	old := time.Now().Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	tests := []struct {
		name  string
		setup func(e *sharedEnv) (src, dst string)
		want  *imap.Error
		check func(e *sharedEnv) int // messages that must not have appeared
	}{
		{"destination without i", func(e *sharedEnv) (string, string) {
			e.grant("Work", "lr")
			return shWork, e.otherBox("lr")
		}, errNoPermission, func(e *sharedEnv) int { return e.count(shOther, "Box") }},
		{"pending grant on the destination", func(e *sharedEnv) (string, string) {
			e.grantTo(pollTestUser, "Work", "lri", store.GrantStatePending, time.Now())
			e.deliverOwn("Archive", "m1", 0)
			return "Archive", shWork
		}, errTryCreate, func(e *sharedEnv) int { return e.count(shOwner, "Work") - 2 }},
		{"expired pending grant on the destination", func(e *sharedEnv) (string, string) {
			e.grantTo(pollTestUser, "Work", "lri", store.GrantStatePending, old)
			e.deliverOwn("Archive", "m1", 0)
			return "Archive", shWork
		}, errTryCreate, func(e *sharedEnv) int { return e.count(shOwner, "Work") - 2 }},
		{"destination without l", func(e *sharedEnv) (string, string) {
			e.grant("Work", "ri")
			e.deliverOwn("Archive", "m1", 0)
			return "Archive", shWork
		}, errTryCreate, func(e *sharedEnv) int { return e.count(shOwner, "Work") - 2 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSharedEnv(t)
			src, dst := tt.setup(e)
			s := e.session()
			if src == "Archive" {
				s.quota = &stubQuota{over: map[string]bool{shOwner: true}}
			}
			e.selectOn(s, src)
			_, err := s.Copy(imap.UIDSetNum(1), dst)
			wantErr(t, err, tt.want)
			if n := tt.check(e); n != 0 {
				t.Fatalf("%d messages were written", n)
			}
		})
	}
}

func TestSharedMoveAcrossOwnersNeedsTAndEOnTheSource(t *testing.T) {
	for _, rights := range []string{"lrwe", "lrwt", "lrw"} {
		t.Run(rights, func(t *testing.T) {
			e := newSharedEnv(t)
			e.grant("Work", rights)
			s := e.session()
			e.selectOn(s, shWork)
			wantErr(t, s.Move(nil, imap.UIDSetNum(imap.UID(e.uids[0])), "Archive"), errNoPermission)
			if e.count(shOwner, "Work") != 2 || e.count(pollTestUser, "Archive") != 0 {
				t.Fatal("a refused MOVE wrote")
			}
		})
	}
	t.Run("destination without i", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrswte")
		box := e.otherBox("lr")
		s := e.session()
		e.selectOn(s, shWork)
		wantErr(t, s.Move(nil, imap.UIDSetNum(imap.UID(e.uids[0])), box), errNoPermission)
		if e.count(shOwner, "Work") != 2 || e.count(shOther, "Box") != 0 {
			t.Fatal("a refused MOVE wrote")
		}
	})
}

func TestSharedTransferQuota(t *testing.T) {
	t.Run("over quota on the destination writes nothing", func(t *testing.T) {
		for _, dir := range []string{"shared to own", "own to shared"} {
			e := newSharedEnv(t)
			e.grant("Work", "lrswi")
			e.deliverOwn("Archive", "m1", 0)
			q := &stubQuota{over: map[string]bool{pollTestUser: true, shOwner: true}}
			s := e.session()
			s.quota = q
			var src, dst string
			var srcOwner, dstOwner, dstBox string
			if dir == "shared to own" {
				src, dst, srcOwner, dstOwner, dstBox = shWork, "Archive", shOwner, pollTestUser, "Archive"
			} else {
				src, dst, srcOwner, dstOwner, dstBox = "Archive", shWork, pollTestUser, shOwner, "Work"
			}
			e.selectOn(s, src)
			before, _ := e.ms.GetMailbox(dstOwner, dstBox)
			beforeN := e.count(dstOwner, dstBox)
			_, err := s.Copy(imap.UIDSetNum(1), dst)
			wantErr(t, err, errOverQuota)
			after, _ := e.ms.GetMailbox(dstOwner, dstBox)
			if before.UidNext != after.UidNext || e.count(dstOwner, dstBox) != beforeN {
				t.Fatalf("%s: a refused COPY wrote", dir)
			}
			if got := q.asked[dstOwner]; got != int64(len(shRaw)) {
				t.Fatalf("%s: asked %d bytes of %s, want %d", dir, got, dstOwner, len(shRaw))
			}
			if q.asked[srcOwner] != 0 {
				t.Fatalf("%s: the source owner's quota was consulted", dir)
			}
		}
	})
	t.Run("MOVE over quota keeps the source", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrswte")
		s := e.session()
		s.quota = &stubQuota{over: map[string]bool{pollTestUser: true}}
		e.selectOn(s, shWork)
		wantErr(t, s.Move(nil, imap.UIDSetNum(imap.UID(e.uids[0])), "Archive"), errOverQuota)
		if e.count(shOwner, "Work") != 2 || e.count(pollTestUser, "Archive") != 0 {
			t.Fatal("a refused MOVE wrote")
		}
	})
	t.Run("rights are judged before quota", func(t *testing.T) {
		// A user without i must not learn the destination is full.
		e := newSharedEnv(t)
		e.grant("Work", "lr")
		s := e.session()
		s.quota = &stubQuota{over: map[string]bool{shOwner: true}}
		e.deliverOwn("Archive", "m1", 0)
		e.selectOn(s, "Archive")
		_, err := s.Copy(imap.UIDSetNum(1), shWork)
		wantErr(t, err, errNoPermission)
	})
	t.Run("same owner is not gated", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lr")
		e.grant("Work/Sub", "li")
		q := &stubQuota{over: map[string]bool{shOwner: true, pollTestUser: true}}
		s := e.session()
		s.quota = q
		e.selectOn(s, shWork)
		if _, err := s.Copy(imap.UIDSetNum(imap.UID(e.uids[0])), shSub); err != nil {
			t.Fatalf("same-owner COPY over quota: %s", errString(err))
		}
		if len(q.asked) != 0 {
			t.Fatalf("the quota was consulted for a same-owner COPY: %v", q.asked)
		}
		// Own folder to own folder too.
		e.deliverOwn("INBOX", "m2", 0)
		e.selectOn(s, "INBOX")
		if _, err := s.Copy(imap.UIDSetNum(1), "Archive"); err != nil {
			t.Fatalf("own COPY over quota: %s", errString(err))
		}
	})
	t.Run("a checker error fails open", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lr")
		s := e.session()
		s.quota = &stubQuota{err: errors.New("down")}
		e.selectOn(s, shWork)
		if _, err := s.Copy(imap.UIDSetNum(imap.UID(e.uids[0])), "Archive"); err != nil {
			t.Fatalf("COPY with a failing checker: %s", errString(err))
		}
	})
}

// ---- MOVE across owners over the wire ------------------------------------------

func TestSharedMoveAcrossOwnersOverTheWire(t *testing.T) {
	t.Run("shared to own", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrswtea")
		c := dialShared(t, e.startServer())
		if _, err := c.Select(shWork, nil).Wait(); err != nil {
			t.Fatal(err)
		}
		data, err := c.Move(imap.UIDSetNum(imap.UID(e.uids[0])), "Archive").Wait()
		if err != nil {
			t.Fatalf("MOVE: %v", err)
		}
		mb, _ := e.ms.GetMailbox(pollTestUser, "Archive")
		if data.UIDValidity != mb.UidValidity {
			t.Fatalf("COPYUID validity = %d, want the destination's %d", data.UIDValidity, mb.UidValidity)
		}
		if e.count(shOwner, "Work") != 1 || e.count(pollTestUser, "Archive") != 1 {
			t.Fatalf("Work %d, Archive %d", e.count(shOwner, "Work"), e.count(pollTestUser, "Archive"))
		}
		// The source MailMeta stays for the orphan sweep, as after EXPUNGE.
		if !e.hasMailMeta(shOwner, "g0") {
			t.Fatal("MOVE removed the source MailMeta")
		}
		msgs, _ := e.ms.ListMessages(pollTestUser, "Archive")
		if !e.hasMailMeta(pollTestUser, msgs[0].MailGuid) {
			t.Fatal("no MailMeta in the destination account")
		}
	})
	t.Run("own to shared", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lri")
		own := e.deliverOwn("Archive", "mine1", 0)
		c := dialShared(t, e.startServer())
		if _, err := c.Select("Archive", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Move(imap.UIDSetNum(imap.UID(own)), shWork).Wait(); err != nil {
			t.Fatalf("MOVE: %v", err)
		}
		if e.count(shOwner, "Work") != 3 || e.count(pollTestUser, "Archive") != 0 {
			t.Fatalf("Work %d, Archive %d", e.count(shOwner, "Work"), e.count(pollTestUser, "Archive"))
		}
	})
	t.Run("one owner to another", func(t *testing.T) {
		e := newSharedEnv(t)
		e.grant("Work", "lrswte")
		box := e.otherBox("li")
		c := dialShared(t, e.startServer())
		if _, err := c.Select(shWork, nil).Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Move(imap.UIDSetNum(imap.UID(e.uids[1])), box).Wait(); err != nil {
			t.Fatalf("MOVE: %v", err)
		}
		if e.count(shOwner, "Work") != 1 || e.count(shOther, "Box") != 1 {
			t.Fatalf("Work %d, Box %d", e.count(shOwner, "Work"), e.count(shOther, "Box"))
		}
	})
}

// ---- notifications for every session on a shared folder ---------------------------

func waitSharedCond(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hasSeenUpdate(u *mailboxUpdates, uid imap.UID) bool {
	for _, f := range u.flags() {
		if f.uid == uid && slices.Contains(f.flags, imap.FlagSeen) {
			return true
		}
	}
	return false
}

// The owner and a grantee select one folder; the notifier is keyed by the
// owner, so each hears what the other (or a third grantee connection) does.
func TestSharedFolderNotifiesEverySession(t *testing.T) {
	auth := multiAuth{pollTestUsername: pollTestUser, "own1": shOwner}

	setup := func(t *testing.T) (e *sharedEnv, addr string, ownerUp, granteeUp *mailboxUpdates) {
		e = newSharedEnv(t)
		e.grant("Work", "lrswtea")
		addr = e.startServerAuth(auth)
		ownerUp, granteeUp = &mailboxUpdates{}, &mailboxUpdates{}
		owner := dialSharedAs(t, addr, "own1", ownerUp)
		if _, err := owner.Select("Work", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		oi, err := owner.Idle()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = oi.Close() })
		grantee := dialSharedAs(t, addr, pollTestUsername, granteeUp)
		if _, err := grantee.Select(shWork, nil).Wait(); err != nil {
			t.Fatal(err)
		}
		gi, err := grantee.Idle()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = gi.Close() })
		return e, addr, ownerUp, granteeUp
	}

	t.Run("a grantee's seen flag reaches the owner", func(t *testing.T) {
		e, addr, ownerUp, granteeUp := setup(t)
		op := dialSharedAs(t, addr, pollTestUsername, nil)
		if _, err := op.Select(shWork, nil).Wait(); err != nil {
			t.Fatal(err)
		}
		uid := imap.UID(e.uids[0])
		if err := op.Store(imap.UIDSetNum(uid), storeFlags(imap.StoreFlagsAdd, imap.FlagSeen), nil).Close(); err != nil {
			t.Fatal(err)
		}
		waitSharedCond(t, "FETCH \\Seen on the owner's session", func() bool { return hasSeenUpdate(ownerUp, uid) })
		waitSharedCond(t, "FETCH \\Seen on the other grantee session", func() bool { return hasSeenUpdate(granteeUp, uid) })

		// And a later FETCH FLAGS by the owner shows it.
		ownerOp := dialSharedAs(t, addr, "own1", nil)
		if _, err := ownerOp.Select("Work", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		msgs, err := ownerOp.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, Flags: true}).Collect()
		if err != nil || len(msgs) != 1 || !slices.Contains(msgs[0].Flags, imap.FlagSeen) {
			t.Fatalf("owner FETCH FLAGS = %+v, %v", msgs, err)
		}
	})

	t.Run("the owner's expunge reaches the grantee", func(t *testing.T) {
		e, addr, _, granteeUp := setup(t)
		ownerOp := dialSharedAs(t, addr, "own1", nil)
		if _, err := ownerOp.Select("Work", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		if err := ownerOp.Store(imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsAdd, imap.FlagDeleted), nil).Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := ownerOp.Expunge().Collect(); err != nil {
			t.Fatal(err)
		}
		waitSharedCond(t, "EXPUNGE on the grantee's session", func() bool { return slices.Contains(granteeUp.expunges(), uint32(1)) })
	})

	t.Run("a cross-owner copy into the folder gives EXISTS to both", func(t *testing.T) {
		e, addr, ownerUp, granteeUp := setup(t)
		own := e.deliverOwn("Archive", "mine1", 0)
		_ = own
		op := dialSharedAs(t, addr, pollTestUsername, nil)
		if _, err := op.Select("Archive", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		e.grant("Work", "lrswteai") // adds i
		if _, err := op.Copy(imap.UIDSetNum(1), shWork).Wait(); err != nil {
			t.Fatalf("COPY: %v", err)
		}
		waitSharedCond(t, "EXISTS 3 on the owner's session", func() bool { return slices.Contains(ownerUp.exists(), uint32(3)) })
		waitSharedCond(t, "EXISTS 3 on the grantee's session", func() bool { return slices.Contains(granteeUp.exists(), uint32(3)) })
		for _, up := range []*mailboxUpdates{ownerUp, granteeUp} {
			n := 0
			for _, x := range up.exists() {
				if x == 3 {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("a session saw EXISTS 3 %d times, want once", n)
			}
		}
	})
}

// sharedRecorder records the keys events are published under.
type sharedRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *sharedRecorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, s)
}
func (r *sharedRecorder) NotifyNewMail(u, m string, uid, n uint32) {
	r.add(fmt.Sprintf("new %s/%s %d", u, m, uid))
}
func (r *sharedRecorder) NotifyFlagChange(u, m string, uid, f uint32) {
	r.add(fmt.Sprintf("flags %s/%s %d", u, m, uid))
}
func (r *sharedRecorder) NotifyExpunge(u, m string, uid uint32) {
	r.add(fmt.Sprintf("expunge %s/%s %d", u, m, uid))
}

// The web SSE stream and IMAP IDLE subscribe under the owner's id, so a grantee's
// change must be published under it and not under the grantee's.
func TestSharedChangesAreAnnouncedUnderTheOwner(t *testing.T) {
	e := newSharedEnv(t)
	e.grant("Work", "lrswi")
	rec := &sharedRecorder{}
	e.ms.SetNotifier(rec)
	s := e.session()
	e.selectOn(s, shWork)

	if err := s.Store(nil, imap.UIDSetNum(imap.UID(e.uids[0])), storeFlags(imap.StoreFlagsAdd, imap.FlagSeen), nil); err != nil {
		t.Fatal(err)
	}
	e.deliverOwn("Archive", "mine1", 0)
	e.selectOn(s, "Archive")
	if _, err := s.Copy(imap.UIDSetNum(1), shWork); err != nil {
		t.Fatal(errString(err))
	}
	want := []string{
		fmt.Sprintf("flags own1/Work %d", e.uids[0]),
		"new own1/Work 3",
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, w := range want {
		if !slices.Contains(rec.events, w) {
			t.Fatalf("events %v lack %q", rec.events, w)
		}
	}
	for _, ev := range rec.events {
		if len(ev) > 4 && (slices.Contains([]string{"u1"}, ev[len(ev)-1:])) {
			t.Fatalf("unexpected event %q", ev)
		}
	}
}
