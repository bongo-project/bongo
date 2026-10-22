package imap

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-imap/v2"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// literal adapts a byte slice to imap.LiteralReader.
type literal struct {
	*bytes.Reader
}

func newLiteral(b []byte) imap.LiteralReader { return literal{bytes.NewReader(b)} }

func (l literal) Size() int64 { return int64(l.Len()) }

type fakeQuota struct {
	over  bool
	err   error
	calls int
}

func (f *fakeQuota) RecipientOverQuota(string) (bool, error) {
	f.calls++
	return f.over, f.err
}

type quotaEnv struct {
	sess     *session
	ps       *store.PebbleStore
	blobDir  string
	mailboxs *store.MailboxStore
}

func newQuotaEnv(t *testing.T, qc quotaChecker) *quotaEnv {
	t.Helper()
	dataDir := t.TempDir()
	ps, err := store.NewPebbleStore(filepath.Join(dataDir, "pebble"))
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	blobs, err := store.NewBlobStore(dataDir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ms := newDirectMailboxStore(ps)
	if err := ms.CreateMailbox("u1", "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	return &quotaEnv{
		sess: &session{
			blobs:     blobs,
			mailboxes: ms,
			pebble:    ps,
			logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			userID:    "u1",
			quota:     qc,
		},
		ps:       ps,
		blobDir:  filepath.Join(dataDir, "blobs"),
		mailboxs: ms,
	}
}

// blobFiles counts stored blob files, ignoring the tmp staging directory.
func (e *quotaEnv) blobFiles(t *testing.T) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(e.blobDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("walk blobs: %v", err)
	}
	return n
}

func (e *quotaEnv) mailKeys(t *testing.T) int {
	t.Helper()
	iter, err := e.ps.PrefixScan([]byte("/user/u1/mail/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	defer iter.Close()
	n := 0
	for iter.Next() {
		n++
	}
	return n
}

func (e *quotaEnv) messageCount(t *testing.T) int {
	t.Helper()
	msgs, err := e.mailboxs.ListMessages("u1", "INBOX")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	return len(msgs)
}

var quotaTestMsg = []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: quota\r\n\r\nbody\r\n")

func TestAppendOverQuotaRefused(t *testing.T) {
	fq := &fakeQuota{over: true}
	e := newQuotaEnv(t, fq)

	_, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil)
	var ie *imap.Error
	if !errors.As(err, &ie) {
		t.Fatalf("Append over quota: want *imap.Error, got %v", err)
	}
	if ie.Type != imap.StatusResponseTypeNo || ie.Code != imap.ResponseCodeOverQuota {
		t.Errorf("Append over quota: got %s [%s], want NO [OVERQUOTA]", ie.Type, ie.Code)
	}
	if n := e.blobFiles(t); n != 0 {
		t.Errorf("blob files after refused APPEND = %d, want 0", n)
	}
	if n := e.mailKeys(t); n != 0 {
		t.Errorf("mail keys after refused APPEND = %d, want 0", n)
	}
	if n := e.messageCount(t); n != 0 {
		t.Errorf("mailbox messages after refused APPEND = %d, want 0", n)
	}

	// Positive control: the same session and user, now under quota.
	fq.over = false
	data, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil)
	if err != nil {
		t.Fatalf("Append under quota: %v", err)
	}
	if data == nil || data.UID == 0 {
		t.Errorf("Append under quota: want a UID, got %+v", data)
	}
	if n := e.messageCount(t); n != 1 {
		t.Errorf("mailbox messages after accepted APPEND = %d, want 1", n)
	}
	if fq.calls != 2 {
		t.Errorf("quota checker calls = %d, want 2", fq.calls)
	}
}

// A checker error fails open, as SMTP RCPT does.
func TestAppendQuotaCheckerErrorFailsOpen(t *testing.T) {
	e := newQuotaEnv(t, &fakeQuota{over: true, err: errors.New("pebble unavailable")})
	if _, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil); err != nil {
		t.Fatalf("Append with failing checker: want success (fail open), got %v", err)
	}
	if n := e.messageCount(t); n != 1 {
		t.Errorf("mailbox messages = %d, want 1", n)
	}
}

func TestAppendNilQuotaCheckerUnlimited(t *testing.T) {
	e := newQuotaEnv(t, nil)
	if _, err := e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil); err != nil {
		t.Fatalf("Append without checker: %v", err)
	}
}

// Drives the real DomainQuotaStore through Append at an awkward limit.
// Usage one byte under the limit is accepted; usage equal to it is refused.
func TestAppendDomainQuotaBoundary(t *testing.T) {
	const limit = int64(1_048_583)
	tests := []struct {
		name   string
		usage  int64
		wantOK bool
	}{
		{"one byte under limit", limit - 1, true},
		{"at limit", limit, false},
		{"over limit", limit + 4097, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newQuotaEnv(t, nil)
			applier := &imapDirectApplier{store: e.ps, mailboxes: e.mailboxs}
			dq := store.NewDomainQuotaStore(e.ps, applier)
			e.sess.quota = dq

			prof, err := proto.Marshal(&bongov1.UserProfile{Email: "alice@Quota.test"})
			if err != nil {
				t.Fatalf("marshal profile: %v", err)
			}
			if err := e.ps.Set([]byte("/user/u1/profile"), prof); err != nil {
				t.Fatalf("write profile: %v", err)
			}
			if err := dq.SetQuota("quota.test", &bongov1.DomainQuota{MaxStorageBytes: limit}); err != nil {
				t.Fatalf("SetQuota: %v", err)
			}
			if err := dq.SetUsage("quota.test", tt.usage); err != nil {
				t.Fatalf("SetUsage: %v", err)
			}

			_, err = e.sess.Append("INBOX", newLiteral(quotaTestMsg), nil)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("Append at usage %d: %v", tt.usage, err)
				}
				return
			}
			var ie *imap.Error
			if !errors.As(err, &ie) || ie.Code != imap.ResponseCodeOverQuota {
				t.Fatalf("Append at usage %d: want OVERQUOTA, got %v", tt.usage, err)
			}
			if n := e.blobFiles(t); n != 0 {
				t.Errorf("blob files = %d, want 0", n)
			}
		})
	}
}

// SetQuotaChecker must reach sessions created after the call; the checker is
// read from the Server, not captured when NewServer built its closure.
func TestServerSetQuotaCheckerReachesNewSessions(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	fq := &fakeQuota{over: true}
	srv.SetQuotaChecker(fq)

	sess, _, err := srv.newSession(nil)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if got := sess.(*session).quota; got != quotaChecker(fq) {
		t.Errorf("session quota = %v, want the installed checker", got)
	}

	srv.SetQuotaChecker(nil)
	sess, _, _ = srv.newSession(nil)
	if sess.(*session).quota != nil {
		t.Errorf("session quota after clearing = %v, want nil", sess.(*session).quota)
	}
}
