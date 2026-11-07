package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// davFake answers the CalDAV and CardDAV requests the clients make: principal
// and home-set discovery, collection listings, empty REPORTs and PUTs. It
// counts each method and keeps the PUT paths.
type davFake struct {
	t   *testing.T
	srv *httptest.Server

	mu    sync.Mutex
	users map[string]string
	puts  []string

	propfind, report, put atomic.Int64
	// fail answers every authenticated request with this status and a body
	// that echoes the user name, the way a careless server might.
	fail atomic.Int64
	// redirectTo answers every request with a 302 to this URL.
	redirectTo atomic.Value
	// unauthorised counts requests refused for bad credentials.
	unauthorised atomic.Int64
}

func newDAVFake(t *testing.T) *davFake {
	t.Helper()
	f := &davFake{t: t, users: map[string]string{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *davFake) addr() string { return strings.TrimPrefix(f.srv.URL, "https://") }

func (f *davFake) addUser(email, pw string) {
	f.mu.Lock()
	f.users[email] = pw
	f.mu.Unlock()
}

func (f *davFake) putPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.puts...)
}

const (
	nsDAV  = `xmlns:d="DAV:"`
	nsCal  = `xmlns:c="urn:ietf:params:xml:ns:caldav"`
	nsCard = `xmlns:card="urn:ietf:params:xml:ns:carddav"`
)

func multistatus(responses ...string) string {
	return `<?xml version="1.0" encoding="utf-8"?><d:multistatus ` + nsDAV + " " + nsCal + " " + nsCard + ">" +
		strings.Join(responses, "") + "</d:multistatus>"
}

func propResponse(href, props string) string {
	return "<d:response><d:href>" + href + "</d:href><d:propstat><d:prop>" + props +
		"</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>"
}

func (f *davFake) handle(w http.ResponseWriter, r *http.Request) {
	if to, _ := f.redirectTo.Load().(string); to != "" {
		http.Redirect(w, r, to, http.StatusFound)
		return
	}
	user, pw, ok := r.BasicAuth()
	f.mu.Lock()
	want, known := f.users[user]
	f.mu.Unlock()
	if !ok || !known || want != pw {
		f.unauthorised.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad credentials for "+user)
		return
	}
	if code := int(f.fail.Load()); code != 0 {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, "failure for "+user+" at "+r.URL.Path)
		return
	}
	// The real server serves the collection roots with a trailing slash only;
	// "/caldav" is a 404 there, and go-webdav's discovery asks for exactly that.
	if r.URL.Path == "/caldav" || r.URL.Path == "/carddav" {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	cal := strings.HasPrefix(r.URL.Path, "/caldav")
	root := "/carddav/"
	if cal {
		root = "/caldav/"
	}
	switch r.Method {
	case "PROPFIND":
		f.propfind.Add(1)
		f.propfindResponse(w, string(body), root, cal)
	case "REPORT":
		f.report.Add(1)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, multistatus())
	case http.MethodPut:
		f.put.Add(1)
		f.mu.Lock()
		f.puts = append(f.puts, r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("ETag", `"1"`)
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

func (f *davFake) propfindResponse(w http.ResponseWriter, body, root string, cal bool) {
	var xml string
	switch {
	case strings.Contains(body, "current-user-principal"):
		xml = multistatus(propResponse(root, "<d:current-user-principal><d:href>"+root+"principal/</d:href></d:current-user-principal>"))
	case strings.Contains(body, "calendar-home-set"):
		xml = multistatus(propResponse(root+"principal/", "<c:calendar-home-set><d:href>"+root+"home/</d:href></c:calendar-home-set>"))
	case strings.Contains(body, "addressbook-home-set"):
		xml = multistatus(propResponse(root+"principal/", "<card:addressbook-home-set><d:href>"+root+"home/</d:href></card:addressbook-home-set>"))
	case cal:
		// New users have an events calendar and a tasks calendar; the
		// tasks one refuses a VEVENT PUT, so it must not be chosen.
		xml = multistatus(
			propResponse(root+"home/", "<d:resourcetype><d:collection/></d:resourcetype>"),
			propResponse(root+"home/tasks/", "<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>tasks</d:displayname>"+
				`<c:supported-calendar-component-set><c:comp name="VTODO"/></c:supported-calendar-component-set>`),
			propResponse(root+"home/default/", "<d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>default</d:displayname>"+
				`<c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set>`),
		)
	default:
		xml = multistatus(
			propResponse(root+"home/", "<d:resourcetype><d:collection/></d:resourcetype>"),
			propResponse(root+"home/book/", "<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype><d:displayname>book</d:displayname>"),
		)
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, xml)
}
