package middleware

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// maxPeek is an absolute ceiling on what the middleware may read from a
// hostile body before rejecting it. It is deliberately a literal, not derived
// from csrfPeekLimit, so raising that constant turns these tests red. Every
// byte read is also tee'd into the in-memory replay buffer, so this bounds the
// buffer too.
const maxPeek = 128 << 10

// hostileBody is far larger than maxPeek, so an unbounded read cannot hide.
const hostileBody = 256 << 20

// countingReader counts bytes handed to its consumer.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// zeroReader yields left zero bytes without allocating them.
type zeroReader struct{ left int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.left {
		p = p[:z.left]
	}
	clear(p)
	z.left -= int64(len(p))
	return len(p), nil
}

// repeatReader endlessly repeats its string.
type repeatReader string

func (s repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = s[i%len(s)]
	}
	return len(p), nil
}

// multipartStream builds a streaming multipart body: the lead name/value
// pairs, then a file part from fileSrc, then the trail name/value pairs.
func multipartStream(lead []string, fileSrc io.Reader, trail []string) (io.Reader, string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		for i := 0; i+1 < len(lead); i += 2 {
			_ = mw.WriteField(lead[i], lead[i+1])
		}
		if fw, err := mw.CreateFormFile("file", "f.bin"); err == nil {
			_, _ = io.Copy(fw, fileSrc)
		}
		for i := 0; i+1 < len(trail); i += 2 {
			_ = mw.WriteField(trail[i], trail[i+1])
		}
		_ = mw.Close()
	}()
	return pr, mw.FormDataContentType()
}

func multipartReq(body io.Reader, ctype string) *http.Request {
	req := httptest.NewRequest("POST", "/import", body).WithContext(csrfCtx("u1"))
	req.Header.Set("Content-Type", ctype)
	return req
}

func TestRequireCSRF_MultipartOversizedNotBuffered(t *testing.T) {
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	body, ctype := multipartStream(nil, &zeroReader{left: hostileBody + 12345}, nil)
	cr := &countingReader{r: body}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, multipartReq(cr, ctype))
	if called {
		t.Error("handler ran without a CSRF token")
	}
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 403 or 413", rr.Code)
	}
	if cr.n > maxPeek {
		t.Errorf("consumed %d bytes before rejecting, want <= %d", cr.n, maxPeek)
	}
}

// A hostile body can pad the stream before the first boundary; the peek must
// give up at csrfPeekLimit rather than scan for it.
func TestRequireCSRF_MultipartHugePreambleBounded(t *testing.T) {
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	// Many short preamble lines: the multipart reader consumes these one by one
	// while looking for the boundary.
	cr := &countingReader{r: io.LimitReader(repeatReader("a\r\n"), hostileBody)}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, multipartReq(cr, "multipart/form-data; boundary=xyz"))
	if called || rr.Code != http.StatusForbidden {
		t.Errorf("called=%v status=%d, want rejected 403", called, rr.Code)
	}
	if cr.n > maxPeek {
		t.Errorf("consumed %d bytes, want <= %d", cr.n, maxPeek)
	}
}

func TestCSRFPeekLimitIsSmall(t *testing.T) {
	if csrfPeekLimit > maxPeek {
		t.Errorf("csrfPeekLimit = %d, want <= %d", csrfPeekLimit, maxPeek)
	}
}

func TestRequireCSRF_MultipartLeadingFieldToken(t *testing.T) {
	data := make([]byte, 1<<20+777)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	var parsedOnEntry, tailOK bool
	var got [32]byte
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parsedOnEntry = r.MultipartForm != nil
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("handler ParseMultipartForm: %v", err)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		got = sha256.Sum256(b)
		tailOK = r.FormValue("after") == "tail"
	}))
	body, ctype := multipartStream([]string{"_csrf", csrfToken(testSecret, "u1")}, bytes.NewReader(data), []string{"after", "tail"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, multipartReq(body, ctype))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if parsedOnEntry {
		t.Error("middleware parsed the multipart form")
	}
	if got != want {
		t.Error("file bytes changed by the middleware replay")
	}
	if !tailOK {
		t.Error("trailing field lost")
	}
}

func TestRequireCSRF_MultipartHeaderTokenNoBodyRead(t *testing.T) {
	body, ctype := multipartStream(nil, &zeroReader{left: 1 << 20}, nil)
	cr := &countingReader{r: body}
	var readOnEntry int64 = -1
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readOnEntry = cr.n
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	req := multipartReq(cr, ctype)
	req.Header.Set(csrfHeaderName, csrfToken(testSecret, "u1"))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if readOnEntry != 0 {
		t.Errorf("read %d body bytes before downstream, want 0", readOnEntry)
	}
}

// A token appended as the last part (the old bongo.js layout) is rejected on
// purpose: finding it would mean reading the whole upload first.
func TestRequireCSRF_MultipartStrictness(t *testing.T) {
	good := csrfToken(testSecret, "u1")
	tests := []struct {
		name  string
		lead  []string
		trail []string
		want  int
	}{
		{"no token", nil, []string{"x", "y"}, http.StatusForbidden},
		{"wrong leading token", []string{"_csrf", good + "x"}, nil, http.StatusForbidden},
		{"token only as last part", nil, []string{"_csrf", good}, http.StatusForbidden},
		{"other field first", []string{"a", "b", "_csrf", good}, nil, http.StatusForbidden},
		{"valid leading token", []string{"_csrf", good}, nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			}))
			body, ctype := multipartStream(tt.lead, strings.NewReader("data"), tt.trail)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, multipartReq(body, ctype))
			if rr.Code != tt.want {
				t.Errorf("status = %d, want %d", rr.Code, tt.want)
			}
			if called != (tt.want == http.StatusOK) {
				t.Errorf("called = %v", called)
			}
		})
	}
}

func TestRequireCSRF_UnknownContentTypeRejected(t *testing.T) {
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"_csrf":"`+csrfToken(testSecret, "u1")+`"}`)).WithContext(csrfCtx("u1"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if called || rr.Code != http.StatusForbidden {
		t.Errorf("called=%v status=%d, want rejected 403", called, rr.Code)
	}
}
