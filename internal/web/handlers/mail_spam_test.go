package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/antispam"
	internalmail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// bayesFailApplier fails Bayes training commands and passes everything else
// to a real applier, so the mailbox move succeeds and only training breaks.
type bayesFailApplier struct {
	next store.RaftApplier
	fail bool
}

func (a *bayesFailApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if a.fail && store.IsBayesSyntheticCommand(cmd.Key) {
		return errors.New("raft unavailable")
	}
	return a.next.ApplyCommand(cmd, d)
}

type spamEnv struct {
	h       *MailHandler
	ps      *store.PebbleStore
	bayes   *antispam.BayesFilter
	applier *bayesFailApplier
	uid     uint32
	guid    string
}

func newSpamEnv(t *testing.T) *spamEnv {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	direct := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, direct)
	for _, mb := range []string{"INBOX", "Junk"} {
		if err := mailboxes.CreateMailbox("user1", mb); err != nil {
			t.Fatalf("CreateMailbox %s: %v", mb, err)
		}
	}
	raw := "From: a@example.test\r\nTo: user1@example.test\r\nSubject: cheap pills offer\r\n\r\nbuy discount pills now\r\n"
	uid := deliverRawMessage(t, mailboxes, bs, "user1", "cheap pills offer", raw)

	applier := &bayesFailApplier{next: direct}
	h := NewMailHandler(ps, bs, mailboxes, discardLogger())
	h.SetApplier(applier)
	bayes := antispam.NewBayesFilter(ps, applier)
	h.SetBayes(bayes)
	return &spamEnv{h: h, ps: ps, bayes: bayes, applier: applier, uid: uid, guid: fmt.Sprintf("m-user1-%d", len(raw))}
}

func (e *spamEnv) post(t *testing.T, mailbox, action string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/%s/%d/%s", mailbox, e.uid, action), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	rctx.URLParams.Add("uid", fmt.Sprintf("%d", e.uid))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), "user1", "user1@example.test"))
	w := httptest.NewRecorder()
	if action == "spam" {
		e.h.MarkSpam(w, r)
	} else {
		e.h.MarkNotSpam(w, r)
	}
	return w
}

func (e *spamEnv) count(category string) uint64 {
	data, err := e.ps.Get(store.BayesTokenKey("user1", category, "pills"))
	if err != nil || len(data) == 0 {
		return 0
	}
	return uint64(data[0])
}

func TestMarkSpam_TrainingFailureFailsRequest(t *testing.T) {
	e := newSpamEnv(t)
	e.applier.fail = true

	w := e.post(t, "INBOX", "spam")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if w.Header().Get("HX-Redirect") != "" {
		t.Errorf("failed training still sent a success redirect: %q", w.Header().Get("HX-Redirect"))
	}

	// Retry once the fault clears: the move is skipped, training applies once.
	e.applier.fail = false
	w = e.post(t, "Junk", "spam")
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	if got := e.count("spam"); got != 1 {
		t.Errorf("spam count after retry = %d, want 1", got)
	}
}

func TestMarkSpam_RepeatAndReclassify(t *testing.T) {
	e := newSpamEnv(t)

	for range 3 {
		if w := e.post(t, "INBOX", "spam"); w.Code != http.StatusOK {
			// After the first click the message lives in Junk.
			if w = e.post(t, "Junk", "spam"); w.Code != http.StatusOK {
				t.Fatalf("spam status = %d; body %s", w.Code, w.Body.String())
			}
		}
	}
	if s, hm := e.count("spam"), e.count("ham"); s != 1 || hm != 0 {
		t.Fatalf("after repeated spam: spam=%d ham=%d, want 1/0", s, hm)
	}
	if n := e.bayes.TrainedCount("user1"); n != 1 {
		t.Errorf("TrainedCount = %d, want 1", n)
	}

	if w := e.post(t, "Junk", "not-spam"); w.Code != http.StatusOK {
		t.Fatalf("not-spam status = %d; body %s", w.Code, w.Body.String())
	}
	if s, hm := e.count("spam"), e.count("ham"); s != 0 || hm != 1 {
		t.Errorf("after reclassify: spam=%d ham=%d, want 0/1", s, hm)
	}
	if n := e.bayes.TrainedCount("user1"); n != 1 {
		t.Errorf("TrainedCount after reclassify = %d, want 1", n)
	}
}

func TestMarkSpam_TrainingRateLimited(t *testing.T) {
	e := newSpamEnv(t)
	e.h.SetTrainLimiter(internalmail.NewRateLimiter(1))

	if w := e.post(t, "INBOX", "spam"); w.Code != http.StatusOK {
		t.Fatalf("first status = %d", w.Code)
	}
	// Second action exceeds the limit: the message still moves, training is
	// skipped and the user is told.
	w := e.post(t, "Junk", "not-spam")
	if w.Code != http.StatusOK {
		t.Fatalf("limited status = %d, want 200", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.Contains(loc, "training+skipped") {
		t.Errorf("HX-Redirect = %q, want a training-skipped flash", loc)
	}
	if s, hm := e.count("spam"), e.count("ham"); s != 1 || hm != 0 {
		t.Errorf("limited action changed counts: spam=%d ham=%d, want 1/0", s, hm)
	}
}

// Every way trainBayes can fail before reaching Train must fail the request,
// not report success for training that never happened.
func TestMarkSpam_UnreadableMessageFailsRequest(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, e *spamEnv)
	}{
		{"meta missing", func(t *testing.T, e *spamEnv) {
			if err := e.ps.Delete([]byte("/user/user1/mail/" + e.guid)); err != nil {
				t.Fatal(err)
			}
		}},
		{"raw message nil", func(t *testing.T, e *spamEnv) {
			data, err := proto.Marshal(&bongov1.MailMeta{Guid: e.guid, Subject: "x"})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.ps.Set([]byte("/user/user1/mail/"+e.guid), data); err != nil {
				t.Fatal(err)
			}
		}},
		{"blob missing", func(t *testing.T, e *spamEnv) {
			data, err := proto.Marshal(&bongov1.MailMeta{
				Guid:       e.guid,
				RawMessage: &bongov1.BlobRef{Hash: strings.Repeat("ab", 32)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.ps.Set([]byte("/user/user1/mail/"+e.guid), data); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSpamEnv(t)
			tc.break_(t, e)
			w := e.post(t, "INBOX", "spam")
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
			if w.Header().Get("HX-Redirect") != "" {
				t.Errorf("success redirect sent: %q", w.Header().Get("HX-Redirect"))
			}
			if n := e.bayes.TrainedCount("user1"); n != 0 {
				t.Errorf("TrainedCount = %d, want 0", n)
			}
		})
	}
}
