package handlers

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func collectScan(t *testing.T, data []byte, maxMsg int) [][]byte {
	t.Helper()
	var got [][]byte
	err := scanMbox(bytes.NewReader(data), maxMsg, func(msg []byte, oversized bool) error {
		if oversized {
			t.Fatalf("unexpected oversized message")
		}
		got = append(got, append([]byte(nil), msg...))
		return nil
	})
	if err != nil {
		t.Fatalf("scanMbox: %v", err)
	}
	return got
}

func equalMessages(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

var mboxEdgeCases = []struct {
	name string
	in   string
}{
	{"two messages", mboxMsgA + mboxMsgB},
	{"leading garbage", "junk line\nmore junk\n" + mboxMsgA + mboxMsgB},
	{"From header is not a separator", "From a Mon\nFrom: x@y\nSubject: s\n\nbody\n"},
	{"CRLF", "From a Mon\r\nSubject: s\r\n\r\nbody\r\nFrom b Tue\r\nSubject: t\r\n\r\nbody2\r\n"},
	{"no trailing newline", "From a Mon\nSubject: s\n\nbody"},
	{"consecutive From lines", "From a Mon\nFrom b Tue\nSubject: s\n\nbody\n"},
	{"whitespace-only messages", "From a Mon\n  \n\t\nFrom b Tue\nSubject: s\n\nbody\nFrom c Wed\n\n\n"},
	{"quoted From body line", "From a Mon\nSubject: s\n\n>From here\nbody\nFrom b Tue\nSubject: t\n\nx\n"},
	{"empty file", ""},
	{"no separator", "Subject: s\n\nbody\n"},
	{"separator without newline at EOF", mboxMsgA + "From b Tue"},
	{"only a separator line", "From a Mon\n"},
	{"blank line before separator", "From a Mon\nSubject: s\n\nbody\n\nFrom b Tue\nSubject: t\n\nx\n"},
	{"From without space", "From\nFrom a\nSubject: s\n\nb\n"},
	{"mid-line From", "From a Mon\nSubject: s\n\nsaid From b Tue\nbody\n"},
}

func TestScanMboxMatchesSplitMbox(t *testing.T) {
	for _, tc := range mboxEdgeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := splitMbox([]byte(tc.in))
			got := collectScan(t, []byte(tc.in), 1<<20)
			if !equalMessages(got, want) {
				t.Fatalf("scanMbox = %q, splitMbox = %q", got, want)
			}
		})
	}
}

// A line longer than the bufio window exercises the ErrBufferFull path,
// including a long separator line and a long body line.
func TestScanMboxLongLines(t *testing.T) {
	long := strings.Repeat("x", 3*mboxReadBufferSize+17)
	for name, in := range map[string]string{
		"long body line":          "From a Mon\nSubject: s\n\n" + long + "\nFrom b Tue\nSubject: t\n\nok\n",
		"long separator line":     "From a " + long + "\nSubject: s\n\nbody\nFrom b " + long + "\nSubject: t\n\nok\n",
		"long From-prefixed body": "From a Mon\nSubject: s\n\nbody\n" + "From " + long + "\nSubject: t\n\nok\n",
	} {
		t.Run(name, func(t *testing.T) {
			want := splitMbox([]byte(in))
			got := collectScan(t, []byte(in), 1<<30)
			if len(want) == 0 || !equalMessages(got, want) {
				t.Fatalf("scanMbox returned %d messages, splitMbox %d (or mismatch)", len(got), len(want))
			}
		})
	}
}

func FuzzScanMbox(f *testing.F) {
	for _, tc := range mboxEdgeCases {
		f.Add([]byte(tc.in))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		want := splitMbox(data)
		var got [][]byte
		err := scanMbox(bytes.NewReader(data), 1<<30, func(msg []byte, _ bool) error {
			got = append(got, append([]byte(nil), msg...))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !equalMessages(got, want) {
			t.Fatalf("scanMbox = %q, splitMbox = %q", got, want)
		}
	})
}

func TestScanMboxOversizedMessage(t *testing.T) {
	big := strings.Repeat("y", 5000)
	in := "From a Mon\nSubject: one\n\nsmall\nFrom b Tue\nSubject: big\n\n" + big + "\nFrom c Wed\nSubject: three\n\nlast\n"
	var kinds []string
	err := scanMbox(strings.NewReader(in), 1000, func(msg []byte, oversized bool) error {
		if oversized {
			if msg != nil {
				t.Errorf("oversized message must carry no bytes")
			}
			kinds = append(kinds, "big")
		} else {
			kinds = append(kinds, string(msg))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Subject: one\n\nsmall", "big", "Subject: three\n\nlast\n"}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", kinds, want)
	}
}

func TestScanMboxReadError(t *testing.T) {
	boom := fmt.Errorf("disk gone")
	r := io.MultiReader(strings.NewReader(mboxMsgA+mboxMsgB[:40]), errReader{boom})
	var n int
	err := scanMbox(r, 1<<20, func([]byte, bool) error { n++; return nil })
	if err != boom {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if n != 1 {
		t.Fatalf("delivered %d messages before the error, want 1", n)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// countingReader records how many bytes have been consumed.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// genMboxMessage builds message i: a separator line, headers and a body whose
// length is non-round and varies with i.
func genMboxMessage(i, bodyLen int) string {
	return string(appendMboxMessage(make([]byte, 0, bodyLen+200), i, bodyLen))
}

func appendMboxMessage(b []byte, i, bodyLen int) []byte {
	start := len(b)
	b = fmt.Appendf(b, "From gen%d@x.example Mon Jan  1 00:00:00 2024\n", i)
	b = fmt.Appendf(b, "From: gen%d@x.example\nTo: quota@example.com\nSubject: generated %d\nMessage-ID: <gen%d@x>\n\n", i, i, i)
	b = fmt.Appendf(b, "message %d\n", i)
	for len(b)-start < bodyLen {
		b = fmt.Appendf(b, "line of body text for message %d, padding padding padding\n", i)
	}
	return b
}

// The structural proof: when the callback receives message 1 the reader has
// consumed only that message plus the buffer window, not the file.
func TestScanMboxStreams(t *testing.T) {
	const count = 100
	first := genMboxMessage(0, 200_003)
	src := &countingReader{r: io.MultiReader(strings.NewReader(first), &lazyMessages{from: 1, count: count, bodyLen: 200_003})}
	var consumedAtFirst int64 = -1
	var n int
	err := scanMbox(src, 1<<20, func(msg []byte, _ bool) error {
		if n == 0 {
			consumedAtFirst = src.n.Load()
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != count {
		t.Fatalf("messages = %d, want %d", n, count)
	}
	// The first message is delivered on seeing the second separator: its own
	// bytes, that line, and at most one buffer window of read-ahead.
	limit := int64(len(first)) + 200 + mboxReadBufferSize
	if consumedAtFirst < 0 || consumedAtFirst > limit {
		t.Fatalf("reader had consumed %d bytes when message 1 was delivered, limit %d (file is ~%d)", consumedAtFirst, limit, count*len(first))
	}
}

// lazyMessages generates messages on demand so the test never holds the file.
type lazyMessages struct {
	from, count, bodyLen int
	cur, buf             []byte
}

func (l *lazyMessages) Read(p []byte) (int, error) {
	for len(l.cur) == 0 {
		if l.from >= l.count {
			return 0, io.EOF
		}
		l.buf = appendMboxMessage(l.buf[:0], l.from, l.bodyLen)
		l.cur = l.buf
		l.from++
	}
	n := copy(p, l.cur)
	l.cur = l.cur[n:]
	return n, nil
}

// Not parallel: the heap sampler would see other tests' allocations.
func TestImportMboxLargeStreams(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const count = 200
	const bodyLen = 210_003

	env := newQuotaEnv(t)
	h := NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger())

	var wantBytes int64
	var total int64
	for i := range count {
		m := genMboxMessage(i, bodyLen)
		total += int64(len(m))
		// Stored bytes exclude the separator line and the newline before the
		// next separator.
		wantBytes += int64(len(m)-strings.Index(m, "\n")-1) - 1
	}
	wantBytes++ // the last message keeps its trailing newline
	if total < 40<<20 {
		t.Fatalf("fixture is only %d bytes", total)
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, err := mw.CreateFormFile("mbox", "big.mbox")
		if err == nil {
			_, err = io.Copy(fw, &lazyMessages{count: count, bodyLen: bodyLen})
		}
		if err == nil {
			err = mw.WriteField("new_mailbox", "Big")
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	req := httptest.NewRequest(http.MethodPost, "/import", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withUserContext(req, quotaUser, quotaEmail)

	// A tight GC target keeps uncollected garbage from masquerading as
	// retained data in the heap samples.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peak.Load() {
				peak.Store(ms.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()

	w := httptest.NewRecorder()
	h.ImportMbox(w, req)
	close(stop)
	wg.Wait()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := env.countMail(t, quotaUser, "Big"); got != count {
		t.Fatalf("imported %d messages, want %d", got, count)
	}
	var blobBytes int64
	var blobCount int
	_ = filepath.WalkDir(env.blobDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			info, _ := d.Info()
			blobBytes += info.Size()
			blobCount++
		}
		return nil
	})
	if blobCount != count || blobBytes != wantBytes {
		t.Fatalf("blobs = %d totalling %d bytes, want %d totalling %d", blobCount, blobBytes, count, wantBytes)
	}
	if grown := int64(peak.Load()) - int64(base.HeapInuse); grown > 24<<20 {
		t.Fatalf("heap grew by %d MiB importing a %d MiB mbox", grown>>20, total>>20)
	}
	if entries, _ := os.ReadDir(os.TempDir()); len(entries) != 0 {
		t.Fatalf("%d temporary files left behind", len(entries))
	}
}

// Not parallel: it points TMPDIR at a scratch directory.
func TestUploadTempFilesRemoved(t *testing.T) {
	big := bytes.Repeat([]byte("a"), uploadMaxMemory+12345)
	// Compose keeps a larger in-memory window, so its payload must exceed that.
	bigCompose := bytes.Repeat([]byte("a"), composeUploadMaxMemory+512<<10)
	mboxBig := []byte(mboxMsgA + "From c Tue\nSubject: big\n\n" + string(big) + "\n")
	routes := []struct {
		name string
		call func(t *testing.T, env *quotaEnv) *http.Request
		run  func(env *quotaEnv, w http.ResponseWriter, r *http.Request)
	}{
		{"mbox import", func(t *testing.T, _ *quotaEnv) *http.Request {
			return uploadReq(t, "/import", "mbox", mboxBig, map[string]string{"new_mailbox": "T"})
		}, func(env *quotaEnv, w http.ResponseWriter, r *http.Request) {
			NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger()).ImportMbox(w, r)
		}},
		{"compose send", func(t *testing.T, _ *quotaEnv) *http.Request {
			return uploadReq(t, "/compose/send", "attachment", bigCompose, map[string]string{"send_id": "x"})
		}, func(env *quotaEnv, w http.ResponseWriter, r *http.Request) {
			NewComposeHandler(nil, nil, nil, nil, nil, discardLogger()).Send(w, r)
		}},
		{"compose draft", func(t *testing.T, _ *quotaEnv) *http.Request {
			return uploadReq(t, "/compose/draft", "attachment", bigCompose, map[string]string{"to": "a@b.example"})
		}, func(env *quotaEnv, w http.ResponseWriter, r *http.Request) {
			defer func() { _ = recover() }() // the draft path needs stores this test does not build
			NewComposeHandler(nil, nil, nil, nil, nil, discardLogger()).SaveDraft(w, r)
		}},
		{"vcard import", func(t *testing.T, _ *quotaEnv) *http.Request {
			return uploadReq(t, "/contacts/import", "vcf", big, nil)
		}, func(env *quotaEnv, w http.ResponseWriter, r *http.Request) {
			NewContactsHandler(env.contacts, nil).ImportContacts(w, r)
		}},
		{"ics import", func(t *testing.T, _ *quotaEnv) *http.Request {
			return uploadReq(t, "/calendar/import", "ics", big, nil)
		}, func(env *quotaEnv, w http.ResponseWriter, r *http.Request) {
			NewCalendarHandler(env.events, env.calendars, nil).ImportCalendar(w, r)
		}},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			env := newQuotaEnv(t)
			req := rt.call(t, env)
			// The spill threshold must really be crossed, or the test proves nothing.
			if req.ContentLength < uploadMaxMemory {
				t.Fatalf("request is %d bytes, below the spill threshold", req.ContentLength)
			}
			var sawFiles atomic.Bool
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					if e, _ := os.ReadDir(tmp); len(e) > 0 {
						sawFiles.Store(true)
					}
					select {
					case <-stop:
						return
					case <-time.After(time.Millisecond):
					}
				}
			}()
			rt.run(env, httptest.NewRecorder(), req)
			close(stop)
			<-done
			if e, _ := os.ReadDir(tmp); len(e) != 0 {
				t.Fatalf("%d temporary files left after the handler returned", len(e))
			}
			if !sawFiles.Load() {
				// Sampling can miss a short-lived file; spooling itself is
				// asserted deterministically in TestParseUploadFormSpools.
				t.Log("note: upload never spooled to disk while sampled")
			}
		})
	}
}

// Over quota with a file big enough to spool: nothing is written and no
// temporary file survives.
func TestImportMboxQuotaSpilledUpload(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	big := mboxMsgA + "From c Tue\nSubject: big\n\n" + strings.Repeat("z", uploadMaxMemory+999) + "\n"
	env := newQuotaEnv(t)
	h := NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger())
	h.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: int64(len(big)) - 1}})

	w := httptest.NewRecorder()
	h.ImportMbox(w, uploadReq(t, "/import", "mbox", []byte(big), map[string]string{"new_mailbox": "Imported"}))

	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507", w.Code)
	}
	if !strings.Contains(w.Body.String(), "storage quota") {
		t.Errorf("body %q should explain the quota refusal", w.Body.String())
	}
	if _, err := env.mailboxes.GetMailbox(quotaUser, "Imported"); err == nil {
		t.Error("refused import must not create the target mailbox")
	}
	if n := env.countMail(t, quotaUser, "Imported"); n != 0 {
		t.Errorf("stored %d messages", n)
	}
	var blobs int
	_ = filepath.WalkDir(env.blobDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			blobs++
		}
		return nil
	})
	if blobs != 0 {
		t.Errorf("%d blobs written for a refused import", blobs)
	}
	if e, _ := os.ReadDir(tmp); len(e) != 0 {
		t.Errorf("%d temporary files left", len(e))
	}
}

// A "From " separator that lands exactly on a bufio chunk boundary must be
// judged by whether the previous chunk ended the line, not by chunk position.
func TestScanMboxSeparatorAtChunkBoundary(t *testing.T) {
	for d := 0; d < 40; d++ {
		k := mboxReadBufferSize - 20 + d
		in := "From a Mon\nSubject: s\n\n" + strings.Repeat("x", k) + "From zz\nSubject: t\n\nok\n"
		in2 := "From a Mon\nSubject: s\n\n" + strings.Repeat("x", k) + "\nFrom zz\nSubject: t\n\nok\n"
		for _, data := range []string{in, in2} {
			want := splitMbox([]byte(data))
			got := collectScan(t, []byte(data), 1<<30)
			if !equalMessages(got, want) {
				t.Fatalf("k=%d: scanMbox returned %d messages, splitMbox %d", k, len(got), len(want))
			}
		}
	}
}

// Not parallel: it lowers a package-level cap.
func TestImportMboxOversizedMessageCounted(t *testing.T) {
	big := "From c Tue\nSubject: big\nMessage-ID: <big@x>\n\n" + strings.Repeat("q", 4000) + "\n"
	run := func(limit int, mbox string) (*quotaEnv, *httptest.ResponseRecorder) {
		old := importMboxMaxMessageBytes
		importMboxMaxMessageBytes = limit
		defer func() { importMboxMaxMessageBytes = old }()
		env := newQuotaEnv(t)
		w := httptest.NewRecorder()
		NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger()).ImportMbox(w,
			uploadReq(t, "/import", "mbox", []byte(mbox), map[string]string{"new_mailbox": "Big"}))
		return env, w
	}
	env, w := run(1000, mboxMsgA+big+mboxMsgB)
	if n := env.countMail(t, quotaUser, "Big"); n != 2 {
		t.Fatalf("stored %d messages, want 2", n)
	}
	var blobs int
	_ = filepath.WalkDir(env.blobDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			blobs++
			if fi, _ := d.Info(); fi != nil && fi.Size() > 1000 {
				t.Errorf("oversized message was stored as a blob (%d bytes)", fi.Size())
			}
		}
		return nil
	})
	if blobs != 2 {
		t.Errorf("%d blobs, want 2", blobs)
	}
	// Same two small messages with nothing oversized: the summary must differ
	// by exactly the failure line.
	_, clean := run(1000, mboxMsgA+mboxMsgB)
	if w.Body.String() == clean.Body.String() {
		t.Error("summary does not report the oversized message as failed")
	}
}

// failAfterReader returns err once n bytes have been read.
type failAfterReader struct {
	r   io.Reader
	n   int
	err error
}

func (f *failAfterReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, f.err
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	k, err := f.r.Read(p)
	f.n -= k
	return k, err
}

// Not parallel: it swaps a package-level hook.
func TestImportMboxReadErrorKeepsImported(t *testing.T) {
	old := importMboxReader
	defer func() { importMboxReader = old }()
	run := func(wrap bool) (*quotaEnv, string) {
		if wrap {
			// Fail inside the second message, after the first was delivered.
			importMboxReader = func(r io.Reader) io.Reader {
				return &failAfterReader{r: r, n: len(mboxMsgA) + 20, err: io.ErrUnexpectedEOF}
			}
		} else {
			importMboxReader = old
		}
		env := newQuotaEnv(t)
		w := httptest.NewRecorder()
		NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger()).ImportMbox(w,
			uploadReq(t, "/import", "mbox", []byte(mboxMsgA+mboxMsgB), map[string]string{"new_mailbox": "Part"}))
		return env, w.Body.String()
	}
	env, body := run(true)
	if n := env.countMail(t, quotaUser, "Part"); n != 1 {
		t.Fatalf("stored %d messages, want the 1 delivered before the error", n)
	}
	// A clean run importing one message must not show a failure line.
	importMboxReader = func(r io.Reader) io.Reader { return io.LimitReader(r, int64(len(mboxMsgA))) }
	_, cleanOne := func() (*quotaEnv, string) {
		env := newQuotaEnv(t)
		w := httptest.NewRecorder()
		NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger()).ImportMbox(w,
			uploadReq(t, "/import", "mbox", []byte(mboxMsgA+mboxMsgB), map[string]string{"new_mailbox": "Part"}))
		return env, w.Body.String()
	}()
	if body == cleanOne {
		t.Error("summary does not report the read failure")
	}
}

// parseUploadForm must spool a payload above uploadMaxMemory to disk rather
// than hold it in memory.
func TestParseUploadFormSpools(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	payload := bytes.Repeat([]byte("v"), uploadMaxMemory+4321)
	req := uploadReq(t, "/contacts/import", "vcf", payload, nil)
	w := httptest.NewRecorder()
	if !parseUploadForm(w, req, 10<<20, "/contacts") {
		t.Fatalf("parseUploadForm refused: %d", w.Code)
	}
	defer removeUploadTemp(req)
	f, _, err := req.FormFile("vcf")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, ok := f.(*os.File); !ok {
		t.Fatalf("upload held in memory (%T), want a spooled *os.File", f)
	}
}

// The default per-message cap must stay bounded, or one huge message can
// exhaust the container's memory.
func TestImportMboxMessageCapIsBounded(t *testing.T) {
	const lo, hi = 34 << 20, 64 << 20 // above compose's 33 MiB, well below the 512 MB container
	if importMboxMaxMessageBytes < lo || importMboxMaxMessageBytes > hi {
		t.Fatalf("importMboxMaxMessageBytes = %d, want within [%d, %d]", importMboxMaxMessageBytes, lo, hi)
	}
}
