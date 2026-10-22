package middleware

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
)

// formCSRFToken extracts the _csrf field from a native form submission without
// reading more of the body than csrfPeekLimit (multipart) or csrfFormMaxBytes
// (url-encoded). Any other content type yields "".
func formCSRFToken(w http.ResponseWriter, r *http.Request) string {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	switch mediaType {
	case "application/x-www-form-urlencoded":
		r.Body = http.MaxBytesReader(w, r.Body, csrfFormMaxBytes)
		return r.PostFormValue(csrfFieldName)
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return ""
		}
		return peekMultipartToken(r, boundary)
	}
	return ""
}

// replayBody serves the bytes already consumed from the original body, then the
// rest of it, and closes the original.
type replayBody struct {
	io.Reader
	io.Closer
}

// peekMultipartToken reads the first multipart part and returns its value if it
// is the _csrf field. The bytes consumed are put back in front of the rest of
// the body so downstream handlers read the unmodified stream.
func peekMultipartToken(r *http.Request, boundary string) string {
	orig := r.Body
	var recorded bytes.Buffer
	tee := io.TeeReader(orig, &recorded)

	token := ""
	mr := multipart.NewReader(io.LimitReader(tee, csrfPeekLimit), boundary)
	if part, err := mr.NextPart(); err == nil && part.FormName() == csrfFieldName && part.FileName() == "" {
		if v, err := io.ReadAll(io.LimitReader(part, csrfFieldMaxBytes+1)); err == nil && len(v) <= csrfFieldMaxBytes {
			token = string(v)
		}
	}

	r.Body = replayBody{Reader: io.MultiReader(&recorded, orig), Closer: orig}
	return token
}
