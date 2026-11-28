package attachpolicy

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const withExe = "From: a@example.org\r\nTo: bob@corp.test\r\nSubject: hi\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
	"--B\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
	"--B\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"invoice.pdf.exe\"\r\n\r\nQUJD\r\n--B--\r\n"

type fakePolicies struct {
	m   map[string]*bongov1.AttachmentPolicy
	err error
}

func (f fakePolicies) Get(domain string) (*bongov1.AttachmentPolicy, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.m[domain], nil
}

type env struct {
	proc  *Processor
	blobs *store.BlobStore
}

func newEnv(t *testing.T, pols map[string]*bongov1.AttachmentPolicy) *env {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := i18n.NewI18n("en")
	users := map[string]string{"bob@corp.test": "u1", "sales@alias.test": "u1"}
	hosted := map[string]string{"corp.test": "corp.test", "alias.test": "corp.test", "other.test": "other.test"}
	d := Deps{
		Policies: fakePolicies{m: pols},
		Blobs:    bs,
		Resolve: func(email string) (string, error) {
			if id, ok := users[email]; ok {
				return id, nil
			}
			return "", errors.New("unknown")
		},
		UserDomain: func(string) (string, error) { return "corp.test", nil },
		PolicyDomain: func(domain string) (string, bool, error) {
			t, ok := hosted[domain]
			return t, ok, nil
		},
		Translate: func(locale, id string, data map[string]interface{}) string { return tr.T(locale, id, data) },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return &env{proc: New(d), blobs: bs}
}

func (e *env) put(t *testing.T, raw string) string {
	t.Helper()
	h, err := e.blobs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (e *env) read(t *testing.T, hash string) string {
	t.Helper()
	rc, err := e.blobs.Get(hash)
	if err != nil {
		t.Fatalf("blob %s: %v", hash, err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func (e *env) run(t *testing.T, rcpt, raw string) (*bongov1.QueueEnvelope, *bongov1.StageResult) {
	t.Helper()
	en := &bongov1.QueueEnvelope{Id: "m1", Sender: "a@example.org", Recipients: []string{rcpt}, BlobHash: e.put(t, raw)}
	res, err := e.proc.Process(context.Background(), en, strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return en, res
}

func policy(action string) map[string]*bongov1.AttachmentPolicy {
	return map[string]*bongov1.AttachmentPolicy{"corp.test": {
		Enabled: true, BlockedExtensions: []string{"exe"}, Action: action,
	}}
}

func TestNoPolicyLeavesHash(t *testing.T) {
	e := newEnv(t, nil)
	en, res := e.run(t, "bob@corp.test", withExe)
	if res.Action != "pass" || en.BlobHash != e.put(t, withExe) {
		t.Fatalf("res=%+v hash=%s", res, en.BlobHash)
	}
}

func TestDisabledPolicyPasses(t *testing.T) {
	p := policy("strip")
	p["corp.test"].Enabled = false
	e := newEnv(t, p)
	if _, res := e.run(t, "bob@corp.test", withExe); res.Action != "pass" {
		t.Fatalf("res=%+v", res)
	}
}

func TestStripCreatesNewBlobKeepsOriginal(t *testing.T) {
	p := policy("strip")
	p["corp.test"].Language = "de"
	e := newEnv(t, p)
	orig := e.put(t, withExe)
	en, res := e.run(t, "bob@corp.test", withExe)
	if res.Action != "strip" || en.BlobHash == orig {
		t.Fatalf("res=%+v hash=%s", res, en.BlobHash)
	}
	if got := e.read(t, orig); got != withExe {
		t.Fatal("original blob changed")
	}
	stripped := e.read(t, en.BlobHash)
	if strings.Contains(stripped, "invoice.pdf.exe\"") || !strings.Contains(stripped, "hello") {
		t.Fatalf("stripped = %s", stripped)
	}
	if res.DkimResult != "none" {
		t.Errorf("DkimResult = %q", res.DkimResult)
	}
	if len(res.Reasons) != 1 || res.Reasons[0] != ".exe" {
		t.Errorf("reasons = %v", res.Reasons)
	}
	// The notice is in the policy language.
	notice := noticeText(t, stripped)
	if !strings.Contains(notice, "Anhang") || !strings.Contains(notice, "invoice.pdf.exe") || !strings.Contains(notice, "corp.test") {
		t.Errorf("notice = %q", notice)
	}
}

func TestStripNoticeLanguages(t *testing.T) {
	for lang, want := range map[string]string{"": "attachment", "en": "attachment", "fr": "pièce jointe", "es": "archivo adjunto", "de": "Anhang"} {
		p := policy("strip")
		p["corp.test"].Language = lang
		e := newEnv(t, p)
		en, _ := e.run(t, "bob@corp.test", withExe)
		if n := noticeText(t, e.read(t, en.BlobHash)); !strings.Contains(n, want) {
			t.Errorf("lang %q: notice = %q, want %q", lang, n, want)
		}
	}
}

// noticeText decodes the base64 text/plain part that follows "hello".
func noticeText(t *testing.T, msg string) string {
	t.Helper()
	i := strings.Index(msg, "Content-Transfer-Encoding: base64\r\n\r\n")
	if i < 0 {
		t.Fatalf("no notice part in:\n%s", msg)
	}
	body := msg[i+len("Content-Transfer-Encoding: base64\r\n\r\n"):]
	body, _, _ = strings.Cut(body, "\r\n--B")
	dec, err := decodeBase64(body)
	if err != nil {
		t.Fatalf("notice decode: %v", err)
	}
	return dec
}

func decodeBase64(s string) (string, error) {
	s = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

func TestQuarantineActionKeepsOriginal(t *testing.T) {
	e := newEnv(t, policy("quarantine"))
	orig := e.put(t, withExe)
	en, res := e.run(t, "bob@corp.test", withExe)
	if res.Action != "quarantine" || en.BlobHash != orig {
		t.Fatalf("res=%+v", res)
	}
	if len(res.Reasons) < 2 || res.Reasons[0] != "blocked_attachment" || res.Reasons[1] != ".exe" {
		t.Errorf("reasons = %v", res.Reasons)
	}
}

func TestPolicyResolution(t *testing.T) {
	tests := []struct {
		name   string
		rcpt   string
		pols   map[string]*bongov1.AttachmentPolicy
		action string
	}{
		{"direct user", "bob@corp.test", policy("strip"), "strip"},
		{"alias address resolves to user of the policy domain", "sales@alias.test", policy("strip"), "strip"},
		{"alias domain, unresolved address, maps to target", "nobody@alias.test", policy("strip"), "strip"},
		{"unresolved address on policy domain", "nobody@corp.test", policy("strip"), "strip"},
		{"unhosted domain has no policy", "x@elsewhere.test", map[string]*bongov1.AttachmentPolicy{"elsewhere.test": {Enabled: true, BlockedExtensions: []string{"exe"}, Action: "strip"}}, "pass"},
		{"other hosted domain without policy", "x@other.test", policy("strip"), "pass"},
		{"angle brackets and case", "<Bob@Corp.Test>", policy("strip"), "strip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.pols)
			if _, res := e.run(t, tt.rcpt, withExe); res.Action != tt.action {
				t.Fatalf("action = %q, want %q", res.Action, tt.action)
			}
		})
	}
}

func TestMultiRecipientMergesPolicies(t *testing.T) {
	pols := map[string]*bongov1.AttachmentPolicy{
		"corp.test":  {Enabled: true, BlockedExtensions: []string{"js"}, Action: "strip"},
		"other.test": {Enabled: true, BlockedExtensions: []string{"exe"}, Action: "quarantine"},
	}
	e := newEnv(t, pols)
	en := &bongov1.QueueEnvelope{Id: "m", Recipients: []string{"bob@corp.test", "x@other.test"}, BlobHash: e.put(t, withExe)}
	res, err := e.proc.Process(context.Background(), en, strings.NewReader(withExe))
	if err != nil || res.Action != "quarantine" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestPolicyReadErrorFailsTheStage(t *testing.T) {
	e := newEnv(t, nil)
	e.proc.d.Policies = fakePolicies{err: errors.New("pebble down")}
	en := &bongov1.QueueEnvelope{Id: "m", Recipients: []string{"bob@corp.test"}}
	if _, err := e.proc.Process(context.Background(), en, strings.NewReader(withExe)); err == nil {
		t.Fatal("a failed policy read must not pass mail unchecked")
	}
}

func TestTooLargeQuarantines(t *testing.T) {
	e := newEnv(t, policy("strip"))
	e.proc.d.MaxSize = 100
	_, res := e.run(t, "bob@corp.test", withExe)
	if res.Action != "quarantine" || res.Reasons[0] != reasonTooLarge {
		t.Fatalf("res=%+v", res)
	}
}

func TestMissingStoresPass(t *testing.T) {
	p := New(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	res, err := p.Process(context.Background(), &bongov1.QueueEnvelope{Recipients: []string{"a@b.test"}}, strings.NewReader(withExe))
	if err != nil || res.Action != "pass" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

type blobReader struct{ bs *store.BlobStore }

func (b blobReader) ReadBlob(h string) (io.ReadCloser, error) { return b.bs.Get(h) }

type counting struct {
	pipeline.Processor
	calls atomic.Int32
}

func (c *counting) Process(ctx context.Context, env *bongov1.QueueEnvelope, r io.Reader) (*bongov1.StageResult, error) {
	c.calls.Add(1)
	return c.Processor.Process(ctx, env, r)
}

// driveRunner runs one envelope through a real Runner and returns where it
// ended up.
func driveRunner(t *testing.T, spool *pipeline.SpoolStore, e *env, procs []pipeline.Processor, id string) {
	t.Helper()
	r := pipeline.NewRunner(spool, blobReader{e.blobs}, procs, slog.New(slog.NewTextHandler(io.Discard, nil)),
		pipeline.WithWorkers(1), pipeline.WithPollInterval(10*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, a := spool.ReadEnvelope("incoming", id)
		_, b := spool.ReadEnvelope("processing", id)
		if a != nil && b != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

type passAll struct{}

func (passAll) Name() string { return "deliver" }
func (passAll) Process(context.Context, *bongov1.QueueEnvelope, io.Reader) (*bongov1.StageResult, error) {
	return &bongov1.StageResult{Stage: "deliver", Action: "pass"}, nil
}

func TestRunnerQuarantineReleaseAndRescan(t *testing.T) {
	e := newEnv(t, policy("quarantine"))
	spool, err := pipeline.NewSpoolStore(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	stage := &counting{Processor: e.proc}
	procs := []pipeline.Processor{stage, passAll{}}

	orig := e.put(t, withExe)
	env := &bongov1.QueueEnvelope{Id: "q1", Sender: "a@example.org", Recipients: []string{"bob@corp.test"}, BlobHash: orig}
	if err := spool.Enqueue(env); err != nil {
		t.Fatal(err)
	}
	driveRunner(t, spool, e, procs, "q1")
	q, err := spool.ReadEnvelope("quarantine", "q1")
	if err != nil {
		t.Fatalf("not in quarantine/: %v", err)
	}
	if !pipeline.HasFlag(q.Flags, pipeline.MsgFlagQuarantined) || !pipeline.HasFlag(q.Flags, pipeline.MsgFlagAttachmentChecked) || q.BlobHash != orig {
		t.Fatalf("flags=%b hash=%s", q.Flags, q.BlobHash)
	}
	if stage.calls.Load() != 1 {
		t.Fatalf("calls = %d", stage.calls.Load())
	}

	// Release: the stage does not run again and the message is delivered.
	pipeline.PrepareForRelease(q)
	if err := spool.Move("q1", "quarantine", "incoming"); err != nil {
		t.Fatal(err)
	}
	if err := spool.WriteEnvelope("incoming", q); err != nil {
		t.Fatal(err)
	}
	driveRunner(t, spool, e, procs, "q1")
	if _, err := spool.ReadEnvelope("quarantine", "q1"); err == nil {
		t.Fatal("released message was quarantined again")
	}
	if stage.calls.Load() != 1 {
		t.Fatalf("release re-ran the stage: calls = %d", stage.calls.Load())
	}

	// Rescan: a new quarantined copy is checked again.
	env2 := &bongov1.QueueEnvelope{
		Id: "q2", Recipients: []string{"bob@corp.test"}, BlobHash: orig,
		Flags:  pipeline.MsgFlagAttachmentChecked | pipeline.MsgFlagQuarantined,
		Stages: []*bongov1.StageResult{{Stage: "attachment_policy", Action: "quarantine"}},
	}
	pipeline.PrepareForRescan(env2)
	if err := spool.Enqueue(env2); err != nil {
		t.Fatal(err)
	}
	driveRunner(t, spool, e, procs, "q2")
	if stage.calls.Load() != 2 {
		t.Fatalf("rescan did not re-run the stage: calls = %d", stage.calls.Load())
	}
	if _, err := spool.ReadEnvelope("quarantine", "q2"); err != nil {
		t.Fatalf("rescanned message should be held again: %v", err)
	}
}

func TestStripIsDeterministic(t *testing.T) {
	e := newEnv(t, policy("strip"))
	a, _ := e.run(t, "bob@corp.test", withExe)
	b, _ := e.run(t, "bob@corp.test", withExe)
	if a.BlobHash != b.BlobHash || bytes.Equal([]byte(a.BlobHash), []byte(e.put(t, withExe))) {
		t.Fatalf("hashes %s %s", a.BlobHash, b.BlobHash)
	}
}
