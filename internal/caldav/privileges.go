package caldav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

// go-webdav v0.7.0 reports every calendar as read-write in
// DAV:current-user-privilege-set and has no backend hook to change that
// (its source carries a TODO for it). PROPFIND answers are therefore
// rewritten on the way out: the DAV:write privilege is dropped for the
// collections that are shared read-only. This is cosmetic. It tells a client
// not to offer edits; the Backend refuses PUT and DELETE on a read share with
// 403 on its own, whatever a client believes.

// maxPrivilegeRewriteBytes bounds the PROPFIND body held for rewriting. A
// larger answer is passed through unmodified (the privilege then reads
// read-write, and writes are still refused by the backend).
const maxPrivilegeRewriteBytes = 8 << 20

const davNS = "DAV:"

// ReadOnlyFunc reports whether href names a collection the authenticated
// user holds at read level only.
type ReadOnlyFunc func(ctx context.Context, href string) bool

// PrivilegeFilter wraps a CalDAV handler so PROPFIND answers report read
// instead of read-write for read-only shares.
func PrivilegeFilter(next http.Handler, readOnly ReadOnlyFunc, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			next.ServeHTTP(w, r)
			return
		}
		c := &capture{w: w, limit: maxPrivilegeRewriteBytes}
		next.ServeHTTP(c, r)
		c.finish(func(body []byte) []byte {
			out, err := rewritePrivileges(body, func(href string) bool { return readOnly(r.Context(), href) })
			if err != nil {
				// Never lose the answer over a cosmetic rewrite.
				logger.Warn("caldav: privilege rewrite failed, sending original", "error", err)
				return body
			}
			return out
		})
	})
}

// capture holds a response until it is complete, or passes it through once it
// outgrows limit.
type capture struct {
	w           http.ResponseWriter
	limit       int
	status      int
	buf         bytes.Buffer
	passthrough bool
	wrote       bool
}

func (c *capture) Header() http.Header { return c.w.Header() }

func (c *capture) WriteHeader(code int) {
	if c.passthrough {
		c.writeHeader(code)
		return
	}
	if c.status == 0 {
		c.status = code
	}
}

func (c *capture) writeHeader(code int) {
	if !c.wrote {
		c.wrote = true
		c.w.WriteHeader(code)
	}
}

func (c *capture) Write(p []byte) (int, error) {
	if c.passthrough {
		c.writeHeader(http.StatusOK)
		return c.w.Write(p)
	}
	if c.buf.Len()+len(p) > c.limit {
		c.passthrough = true
		status := c.status
		if status == 0 {
			status = http.StatusOK
		}
		c.writeHeader(status)
		if _, err := c.w.Write(c.buf.Bytes()); err != nil {
			return 0, err
		}
		c.buf = bytes.Buffer{}
		return c.w.Write(p)
	}
	return c.buf.Write(p)
}

// finish sends the captured response, with a 207 body rewritten by rewrite.
func (c *capture) finish(rewrite func([]byte) []byte) {
	if c.passthrough {
		return
	}
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	body := c.buf.Bytes()
	if status == http.StatusMultiStatus {
		body = rewrite(body)
	}
	c.w.Header().Del("Content-Length")
	c.writeHeader(status)
	_, _ = c.w.Write(body)
}

var (
	nameResponse     = xml.Name{Space: davNS, Local: "response"}
	nameHref         = xml.Name{Space: davNS, Local: "href"}
	namePrivilegeSet = xml.Name{Space: davNS, Local: "current-user-privilege-set"}
	nameWrite        = xml.Name{Space: davNS, Local: "write"}
)

// rewritePrivileges drops DAV:write from every current-user-privilege-set in
// a multistatus body whose response href is read-only. Namespaces are carried
// on each element's name, so the xmlns declarations are dropped and the
// encoder writes the namespace of every element itself: the result is
// equivalent XML, not the same bytes.
func rewritePrivileges(in []byte, readOnly func(href string) bool) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(in))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)

	var (
		stack      []xml.Name
		href       string
		haveHref   bool
		inHref     bool
		inPrivSet  int
		skipDepth  int // >0 while inside a dropped element
		changedAny bool
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			if t.Name == nameWrite && inPrivSet > 0 && haveHref && readOnly(href) {
				skipDepth = 1
				changedAny = true
				continue
			}
			parent := xml.Name{}
			if n := len(stack); n > 0 {
				parent = stack[n-1]
			}
			stack = append(stack, t.Name)
			switch {
			case t.Name == nameResponse:
				href, haveHref = "", false
			case t.Name == nameHref && parent == nameResponse && !haveHref:
				inHref = true
			case t.Name == namePrivilegeSet:
				inPrivSet++
			}
			t.Attr = dropNamespaceDecls(t.Attr)
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if n := len(stack); n > 0 {
				stack = stack[:n-1]
			}
			switch t.Name {
			case nameHref:
				if inHref {
					inHref, haveHref = false, true
					if u, err := url.PathUnescape(href); err == nil {
						href = u
					}
				}
			case namePrivilegeSet:
				inPrivSet--
			}
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}
		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			if inHref {
				href += string(t)
			}
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			if skipDepth > 0 {
				continue
			}
			if err := enc.EncodeToken(tok); err != nil {
				return nil, err
			}
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	if !changedAny {
		// Nothing to change: keep the original bytes exactly.
		return in, nil
	}
	return out.Bytes(), nil
}

func dropNamespaceDecls(attrs []xml.Attr) []xml.Attr {
	kept := attrs[:0:0]
	for _, a := range attrs {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		kept = append(kept, a)
	}
	return kept
}
