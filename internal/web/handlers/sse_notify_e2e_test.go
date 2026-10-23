package handlers

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/go-chi/chi/v5"

	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// This file holds the one test that has to live on this side of the tree: it
// proves a change made through the WEB handler reaches an IMAP client. Emitting
// notifications from the IMAP session instead of from the store would leave
// every web-initiated change invisible, which is half the reported bug, and a
// test that lived entirely inside internal/imap could not tell the difference.

const (
	crossUser     = "u-cross"
	crossUsername = "u-cross@localhost"
	crossPassword = "correct-horse"
)

// crossAuth accepts exactly one credential pair, so reaching a selected
// mailbox proves the connection really authenticated.
type crossAuth struct{}

func (crossAuth) Authenticate(username, password string) (string, error) {
	if username == crossUsername && password == crossPassword {
		return crossUser, nil
	}
	return "", errors.New("invalid credentials")
}

// crossEnv is a single storage stack fronted by both a web router and a real
// IMAP server, sharing one notification hub -- the arrangement
// internal/agent/agent.go builds at startup.
type crossEnv struct {
	mailboxes *store.MailboxStore
	router    *chi.Mux
	imapAddr  string
}

func newCrossEnv(t *testing.T) *crossEnv {
	t.Helper()

	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	blobs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	// The applier needs the store back, because that is where it finds the
	// notifier installed below: notifications are emitted by the apply, not by
	// the mutation method that issued the command.
	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	applier.mailboxes = mailboxes
	if err := mailboxes.CreateMailbox(crossUser, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox INBOX: %v", err)
	}

	notifier := bongoimap.NewNotifier()
	mailboxes.SetNotifier(bongoimap.NewStoreNotifier(notifier))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := bongoimap.NewServer(crossAuth{}, ps, blobs, mailboxes, nil, nil, nil, notifier, nil, true, logger)
	if err := srv.Listen("127.0.0.1:0", "", nil, false, nil); err != nil {
		t.Fatalf("IMAP Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })

	h := NewMailHandler(ps, blobs, mailboxes, logger)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithUserContext(req.Context(), crossUser, crossUsername)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/mail/{mailbox}/{uid}/flags", h.ToggleFlags)

	return &crossEnv{mailboxes: mailboxes, router: r, imapAddr: srv.PlainAddr()}
}

// deliverCrossMessage puts a message in INBOX the way SMTP delivery does.
func (e *crossEnv) deliverCrossMessage(t *testing.T, guid string) uint32 {
	t.Helper()

	uid, err := e.mailboxes.DeliverMessage(crossUser, "INBOX", &bongov1.MailMeta{
		Guid:       guid,
		Subject:    guid,
		ReceivedAt: time.Now().Unix(),
	}, 0)
	if err != nil {
		t.Fatalf("DeliverMessage(%q): %v", guid, err)
	}
	return uid
}

// crossFlagUpdate is an untagged FETCH the IMAP client received without having
// asked for one.
type crossFlagUpdate struct {
	seqNum uint32
	uid    imap.UID
	flags  []imap.Flag
}

// TestWebFlagChangeReachesIdlingIMAPClient is the "unread counts do not
// decrease" complaint from the other direction: a message marked read in the
// web UI has to reach the desktop client that is idling on the same folder.
//
// It drives the real HTTP handler and reads the result off a real IMAP socket,
// so the only thing joining the two is the store-level producer. Move that
// producer into the IMAP session and this test goes red while every
// IMAP-to-IMAP test stays green -- which is exactly the distinction that
// justifies putting it in the store.
func TestWebFlagChangeReachesIdlingIMAPClient(t *testing.T) {
	env := newCrossEnv(t)
	env.deliverCrossMessage(t, "guid-1")
	uid := env.deliverCrossMessage(t, "guid-2")

	var (
		received = make(chan crossFlagUpdate, 8)
		expunged = make(chan uint32, 8)
	)
	c, err := imapclient.DialInsecure(env.imapAddr, &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Fetch: func(msg *imapclient.FetchMessageData) {
				buf, err := msg.Collect()
				if err != nil {
					return
				}
				received <- crossFlagUpdate{seqNum: buf.SeqNum, uid: buf.UID, flags: buf.Flags}
			},
			Expunge: func(seqNum uint32) { expunged <- seqNum },
		},
	})
	if err != nil {
		t.Fatalf("DialInsecure(%s): %v", env.imapAddr, err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Login(crossUsername, crossPassword).Wait(); err != nil {
		t.Fatalf("IMAP Login: %v", err)
	}
	selectData, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("IMAP SELECT INBOX: %v", err)
	}
	if selectData.NumMessages != 2 {
		t.Fatalf("SELECT reported %d messages, want 2", selectData.NumMessages)
	}

	idle, err := c.Idle()
	if err != nil {
		t.Fatalf("IMAP IDLE: %v", err)
	}

	// The web UI marks the second message read. This is the exact request the
	// browser sends; nothing here touches the IMAP session.
	req := httptest.NewRequest(http.MethodPost, "/mail/INBOX/2/flags",
		strings.NewReader("action=add&flag=seen"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flags returned %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got crossFlagUpdate
	select {
	case got = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("the idling IMAP client was never told the web UI marked the message read")
	}

	if err := idle.Close(); err != nil {
		t.Fatalf("ending IDLE: %v", err)
	}

	if got.uid != imap.UID(uid) {
		t.Errorf("flag update carried UID %d, want %d", got.uid, uid)
	}
	if got.seqNum != 2 {
		t.Errorf("flag update addressed sequence number %d, want 2", got.seqNum)
	}
	if !crossHasFlag(got.flags, imap.FlagSeen) {
		t.Errorf("flag update carried flags %v, want \\Seen among them", got.flags)
	}
	select {
	case seq := <-expunged:
		t.Errorf("a flag change produced EXPUNGE %d", seq)
	default:
	}
}

func crossHasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func (a crossAuth) AuthenticateProfile(username, password string) (string, *bongov1.UserProfile, error) {
	id, err := a.Authenticate(username, password)
	if err != nil {
		return "", nil, err
	}
	return id, &bongov1.UserProfile{}, nil
}
