package handlers

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

type countingBody struct {
	r io.Reader
	n int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// oversizedUpload streams a multipart body whose file part is size bytes.
func oversizedUpload(field string, size int64) (*countingBody, string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		fw, err := mw.CreateFormFile(field, "big.bin")
		if err != nil {
			return
		}
		_, _ = io.Copy(fw, io.LimitReader(zeroReader{}, size))
		_ = mw.Close()
	}()
	return &countingBody{r: pr}, mw.FormDataContentType()
}

// The caps are asserted against literals so that raising a constant fails here
// rather than silently moving the expectation with it.
const (
	// importUploadBodySize is far past any sane cap (absolute, not derived).
	importUploadBodySize = 256 << 20
	// importUploadReadCeiling is the most a handler may read before refusing.
	importUploadReadCeiling = 11 << 20
)

func TestImportUploadCapValues(t *testing.T) {
	tests := []struct {
		name string
		got  int64
		want int64
	}{
		{"contacts", contactsImportMaxBytes, 10 << 20},
		{"calendar", calendarImportMaxBytes, 10 << 20},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s import cap = %d, want %d", tt.name, tt.got, tt.want)
		}
	}
}

func TestImportUploadsRejectOversizedBody(t *testing.T) {
	contacts := NewContactsHandler(nil, nil)
	calendar := NewCalendarHandler(nil, nil, nil)
	tests := []struct {
		name   string
		field  string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{"contacts", "vcf", contacts.ImportContacts},
		{"calendar", "ics", calendar.ImportCalendar},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, ctype := oversizedUpload(tt.field, importUploadBodySize)
			req := httptest.NewRequest(http.MethodPost, "/import", body)
			req.Header.Set("Content-Type", ctype)
			req = withUserContext(req, "user-oversized-import", "user@example.com")
			rec := httptest.NewRecorder()

			tt.handle(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
			}
			if body.n > importUploadReadCeiling {
				t.Errorf("read %d bytes, want <= %d", body.n, importUploadReadCeiling)
			}
		})
	}
}
