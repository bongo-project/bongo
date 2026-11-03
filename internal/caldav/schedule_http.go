package caldav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The HTTP side of RFC 6638. go-webdav v0.7.0 knows nothing of scheduling: it
// builds the principal answer itself, sets the DAV header from its own list
// and has no hook for POST. This wrapper therefore
//
//   - adds calendar-auto-schedule to the DAV header of OPTIONS answers,
//   - moves the scheduling properties of the principal out of the 404 part of
//     go-webdav's answer into the 200 part, with values,
//   - serves the schedule inbox and outbox collections itself, before
//     go-webdav sees the request.
//
// None of it is active until the backend has a Scheduler, so a server without
// one advertises nothing and clients keep sending iMIP themselves.

const (
	caldavNS       = "urn:ietf:params:xml:ns:caldav"
	calServerNS    = "http://calendarserver.org/ns/"
	scheduleToken  = "calendar-auto-schedule"
	inboxSegment   = "inbox"
	outboxSegment  = "outbox"
	maxFreeBusyReq = imip.MaxPartBytes
	// maxFreeBusyRecipients caps the ATTENDEEs of one outbox free/busy POST.
	maxFreeBusyRecipients = icalevent.MaxAttendees
	// maxFreeBusyRange caps the period one POST may ask about.
	maxFreeBusyRange = 366 * 24 * time.Hour
)

func inboxPath(userID string) string  { return "/caldav/" + userID + "/" + inboxSegment + "/" }
func outboxPath(userID string) string { return "/caldav/" + userID + "/" + outboxSegment + "/" }

type scheduleKind int

const (
	scheduleNone scheduleKind = iota
	scheduleInbox
	scheduleInboxItem
	scheduleOutbox
)

type schedulePath struct {
	kind scheduleKind
	user string
	item string // inbox item name without ".ics"
}

// parseSchedulePath recognises /caldav/{user}/inbox/, /caldav/{user}/inbox/{name}.ics
// and /caldav/{user}/outbox/ (trailing slash optional).
func parseSchedulePath(path string) schedulePath {
	rest, ok := strings.CutPrefix(path, "/caldav/")
	if !ok {
		return schedulePath{}
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
		return schedulePath{}
	}
	p := schedulePath{user: parts[0]}
	switch {
	case parts[1] == inboxSegment && len(parts) == 2:
		p.kind = scheduleInbox
	case parts[1] == inboxSegment && len(parts) == 3:
		name, isICS := strings.CutSuffix(parts[2], ".ics")
		if !isICS {
			return schedulePath{}
		}
		p.kind, p.item = scheduleInboxItem, name
	case parts[1] == outboxSegment && len(parts) == 2:
		p.kind = scheduleOutbox
	default:
		return schedulePath{}
	}
	return p
}

// ScheduleHandler wraps next, the go-webdav handler, with CalDAV scheduling.
// The backend is consulted on every request, so a scheduler wired after the
// handler was built still takes effect.
func ScheduleHandler(next http.Handler, b *Backend, logger *slog.Logger) http.Handler {
	s := &scheduleServer{b: b, next: next, logger: logger}
	return http.HandlerFunc(s.serve)
}

type scheduleServer struct {
	b      *Backend
	next   http.Handler
	logger *slog.Logger
}

func (s *scheduleServer) serve(w http.ResponseWriter, r *http.Request) {
	user := getUserID(r.Context())
	if s.b.scheduler == nil || user == "" {
		s.next.ServeHTTP(w, r)
		return
	}
	sp := parseSchedulePath(r.URL.Path)
	if sp.kind != scheduleNone {
		// Another user's inbox is never served, with or without a grant: the
		// path user must be the authenticated user, as for calendars.
		if sp.user != user {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.collection(w, r, sp)
		return
	}
	switch r.Method {
	case http.MethodOptions:
		c := &capture{w: w, limit: maxPrivilegeRewriteBytes}
		s.next.ServeHTTP(c, r)
		if c.status == 0 || c.status/100 == 2 {
			c.Header().Add("DAV", scheduleToken)
		}
		c.finish(func(b []byte) []byte { return b })
	case "PROPFIND":
		if p := parseDAVPath(r.URL.Path); p.kind == kindPrincipal && p.user == user {
			c := &capture{w: w, limit: maxPrivilegeRewriteBytes}
			s.next.ServeHTTP(c, r)
			c.finish(func(body []byte) []byte {
				out, err := s.rewritePrincipal(body, user)
				if err != nil {
					s.logger.Warn("caldav: scheduling properties not added to the principal", "error", err)
					return body
				}
				return out
			})
			return
		}
		s.next.ServeHTTP(w, r)
	default:
		s.next.ServeHTTP(w, r)
	}
}

// ---- principal -------------------------------------------------------------

var (
	nameScheduleInboxURL  = xml.Name{Space: caldavNS, Local: "schedule-inbox-URL"}
	nameScheduleOutboxURL = xml.Name{Space: caldavNS, Local: "schedule-outbox-URL"}
	nameUserAddressSet    = xml.Name{Space: caldavNS, Local: "calendar-user-address-set"}
	namePropstat          = xml.Name{Space: davNS, Local: "propstat"}
	nameProp              = xml.Name{Space: davNS, Local: "prop"}
	nameStatus            = xml.Name{Space: davNS, Local: "status"}
)

// xnode is a parsed XML element; items keep character data in document order.
type xnode struct {
	name  xml.Name
	attrs []xml.Attr
	items []xitem
}

type xitem struct {
	node *xnode
	text string
}

func (n *xnode) text() string {
	var sb strings.Builder
	for _, it := range n.items {
		sb.WriteString(it.text)
	}
	return strings.TrimSpace(sb.String())
}

func (n *xnode) child(name xml.Name) *xnode {
	for _, it := range n.items {
		if it.node != nil && it.node.name == name {
			return it.node
		}
	}
	return nil
}

func (n *xnode) remove(target *xnode) {
	n.items = slices.DeleteFunc(n.items, func(it xitem) bool { return it.node == target })
}

func leaf(name xml.Name, text string) *xnode {
	n := &xnode{name: name}
	if text != "" {
		n.items = []xitem{{text: text}}
	}
	return n
}

func hrefNode(href string) *xnode { return leaf(nameHref, href) }

func parseXML(in []byte) (*xnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(in))
	var stack []*xnode
	var root *xnode
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
			n := &xnode{name: t.Name, attrs: dropNamespaceDecls(t.Attr)}
			if len(stack) == 0 {
				root = n
			} else {
				top := stack[len(stack)-1]
				top.items = append(top.items, xitem{node: n})
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.items = append(top.items, xitem{text: string(t)})
			}
		}
	}
	if root == nil {
		return nil, errors.New("empty XML document")
	}
	return root, nil
}

func (n *xnode) encode(enc *xml.Encoder) error {
	if err := enc.EncodeToken(xml.StartElement{Name: n.name, Attr: n.attrs}); err != nil {
		return err
	}
	for _, it := range n.items {
		if it.node != nil {
			if err := it.node.encode(enc); err != nil {
				return err
			}
			continue
		}
		if err := enc.EncodeToken(xml.CharData(it.text)); err != nil {
			return err
		}
	}
	return enc.EncodeToken(xml.EndElement{Name: n.name})
}

// rewritePrincipal moves the scheduling properties a client asked for from
// the 404 part of go-webdav's principal answer to a 200 part with values.
// An answer that did not ask for them is returned unchanged.
func (s *scheduleServer) rewritePrincipal(in []byte, user string) ([]byte, error) {
	root, err := parseXML(in)
	if err != nil {
		return nil, err
	}
	values := map[xml.Name]*xnode{}
	value := func(name xml.Name) *xnode {
		if v, ok := values[name]; ok {
			return v
		}
		var v *xnode
		switch name {
		case nameScheduleInboxURL:
			v = leaf(name, "")
			v.items = []xitem{{node: hrefNode(inboxPath(user))}}
		case nameScheduleOutboxURL:
			v = leaf(name, "")
			v.items = []xitem{{node: hrefNode(outboxPath(user))}}
		case nameUserAddressSet:
			v = leaf(name, "")
			for _, a := range s.b.scheduler.Addresses(user) {
				v.items = append(v.items, xitem{node: hrefNode("mailto:" + a)})
			}
		}
		values[name] = v
		return v
	}

	principal := userPath(user)
	changed := false
	for _, it := range root.items {
		resp := it.node
		if resp == nil || resp.name != nameResponse {
			continue
		}
		href := ""
		if h := resp.child(nameHref); h != nil {
			href = h.text()
			if u, err := url.PathUnescape(href); err == nil {
				href = u
			}
		}
		if strings.TrimSuffix(href, "/") != strings.TrimSuffix(principal, "/") {
			continue
		}
		var moved []*xnode
		var ok200 *xnode
		for _, pi := range resp.items {
			ps := pi.node
			if ps == nil || ps.name != namePropstat {
				continue
			}
			status := ""
			if st := ps.child(nameStatus); st != nil {
				status = st.text()
			}
			prop := ps.child(nameProp)
			switch {
			case prop == nil:
			case strings.Contains(status, " 200 "):
				ok200 = ps
			case strings.Contains(status, " 404 "):
				for _, pp := range prop.items {
					if pp.node != nil && value(pp.node.name) != nil {
						moved = append(moved, pp.node)
					}
				}
				for _, m := range moved {
					prop.remove(m)
				}
			}
		}
		if len(moved) == 0 {
			continue
		}
		changed = true
		// Drop a 404 propstat that has nothing left in it.
		for _, pi := range slices.Clone(resp.items) {
			ps := pi.node
			if ps == nil || ps.name != namePropstat {
				continue
			}
			if prop := ps.child(nameProp); prop != nil && !hasElements(prop) {
				resp.remove(ps)
			}
		}
		if ok200 == nil {
			ok200 = &xnode{name: namePropstat}
			ok200.items = []xitem{
				{node: &xnode{name: nameProp}},
				{node: leaf(nameStatus, "HTTP/1.1 200 OK")},
			}
			resp.items = append(resp.items, xitem{node: ok200})
		}
		prop := ok200.child(nameProp)
		for _, m := range moved {
			prop.items = append(prop.items, xitem{node: value(m.name)})
		}
	}
	if !changed {
		return in, nil
	}
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	if err := root.encode(enc); err != nil {
		return nil, err
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out.Bytes()...), nil
}

func hasElements(n *xnode) bool {
	for _, it := range n.items {
		if it.node != nil {
			return true
		}
	}
	return false
}

// ---- inbox and outbox ------------------------------------------------------

func (s *scheduleServer) collection(w http.ResponseWriter, r *http.Request, sp schedulePath) {
	allow := "OPTIONS, PROPFIND"
	switch sp.kind {
	case scheduleInbox:
		allow = "OPTIONS, PROPFIND, REPORT"
	case scheduleInboxItem:
		allow = "OPTIONS, PROPFIND, GET, HEAD, DELETE"
	case scheduleOutbox:
		allow = "OPTIONS, PROPFIND, POST"
	}
	switch {
	case r.Method == http.MethodOptions:
		w.Header().Set("DAV", "1, 3, calendar-access, "+scheduleToken)
		w.Header().Set("Allow", allow)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "PROPFIND":
		s.propfind(w, r, sp)
	case r.Method == "REPORT" && sp.kind == scheduleInbox:
		s.report(w, r, sp)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && sp.kind == scheduleInboxItem:
		s.getItem(w, r, sp)
	case r.Method == http.MethodDelete && sp.kind == scheduleInboxItem:
		s.deleteItem(w, sp)
	case r.Method == http.MethodPost && sp.kind == scheduleOutbox:
		s.postOutbox(w, r, sp.user)
	default:
		w.Header().Set("Allow", allow)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// propRequest is what a PROPFIND or REPORT asks for.
type propRequest struct {
	names    []xml.Name
	all      bool
	hrefs    []string // multiget
	multiget bool
}

func parseRequest(body []byte) (propRequest, error) {
	var req propRequest
	if len(bytes.TrimSpace(body)) == 0 {
		req.all = true
		return req, nil
	}
	root, err := parseXML(body)
	if err != nil {
		return req, err
	}
	req.multiget = root.name == xml.Name{Space: caldavNS, Local: "calendar-multiget"}
	for _, it := range root.items {
		n := it.node
		if n == nil {
			continue
		}
		switch n.name {
		case nameProp:
			for _, pi := range n.items {
				if pi.node != nil {
					req.names = append(req.names, pi.node.name)
				}
			}
		case xml.Name{Space: davNS, Local: "allprop"}, xml.Name{Space: davNS, Local: "propname"}:
			req.all = true
		case nameHref:
			req.hrefs = append(req.hrefs, n.text())
		}
	}
	return req, nil
}

var (
	nameResourceType = xml.Name{Space: davNS, Local: "resourcetype"}
	nameDisplayName  = xml.Name{Space: davNS, Local: "displayname"}
	nameGetETag      = xml.Name{Space: davNS, Local: "getetag"}
	nameContentType  = xml.Name{Space: davNS, Local: "getcontenttype"}
	nameContentLen   = xml.Name{Space: davNS, Local: "getcontentlength"}
	nameLastModified = xml.Name{Space: davNS, Local: "getlastmodified"}
	nameCurrentUser  = xml.Name{Space: davNS, Local: "current-user-principal"}
	nameOwner        = xml.Name{Space: davNS, Local: "owner"}
	namePrivileges   = xml.Name{Space: davNS, Local: "current-user-privilege-set"}
	nameReportSet    = xml.Name{Space: davNS, Local: "supported-report-set"}
	nameCalendarData = xml.Name{Space: caldavNS, Local: "calendar-data"}
	nameGetCTag      = xml.Name{Space: calServerNS, Local: "getctag"}
)

func davEl(local string) *xnode { return &xnode{name: xml.Name{Space: davNS, Local: local}} }
func calEl(local string) *xnode { return &xnode{name: xml.Name{Space: caldavNS, Local: local}} }

func wrap(name xml.Name, kids ...*xnode) *xnode {
	n := &xnode{name: name}
	for _, k := range kids {
		n.items = append(n.items, xitem{node: k})
	}
	return n
}

func privilege(inner *xnode) *xnode { return wrap(xml.Name{Space: davNS, Local: "privilege"}, inner) }

// collectionProps are the properties of the inbox or outbox collection.
func collectionProps(sp schedulePath, items []store.ScheduleItem) map[xml.Name]*xnode {
	principal := userPath(sp.user)
	props := map[xml.Name]*xnode{
		nameCurrentUser: wrap(nameCurrentUser, hrefNode(principal)),
		nameOwner:       wrap(nameOwner, hrefNode(principal)),
	}
	if sp.kind == scheduleInbox {
		props[nameResourceType] = wrap(nameResourceType, davEl("collection"), calEl("schedule-inbox"))
		props[nameDisplayName] = leaf(nameDisplayName, inboxSegment)
		props[namePrivileges] = wrap(namePrivileges, privilege(davEl("read")), privilege(davEl("write")))
		props[nameReportSet] = wrap(nameReportSet,
			wrap(xml.Name{Space: davNS, Local: "supported-report"}, wrap(xml.Name{Space: davNS, Local: "report"}, calEl("calendar-multiget"))),
			wrap(xml.Name{Space: davNS, Local: "supported-report"}, wrap(xml.Name{Space: davNS, Local: "report"}, calEl("calendar-query"))))
		ctag := "0"
		if n := len(items); n > 0 {
			ctag = items[n-1].Name
		}
		props[nameGetCTag] = leaf(nameGetCTag, ctag)
	} else {
		props[nameResourceType] = wrap(nameResourceType, davEl("collection"), calEl("schedule-outbox"))
		props[nameDisplayName] = leaf(nameDisplayName, outboxSegment)
		props[namePrivileges] = wrap(namePrivileges, privilege(davEl("read")), privilege(calEl("schedule-send")))
		props[nameReportSet] = wrap(nameReportSet)
	}
	return props
}

func itemETag(it store.ScheduleItem) string { return store.ContentETag(it.Data, it.Received.Unix()) }

func itemHref(user string, it store.ScheduleItem) string {
	return inboxPath(user) + it.Name + ".ics"
}

// itemProps are the properties of one inbox item.
func itemProps(it store.ScheduleItem) map[xml.Name]*xnode {
	return map[xml.Name]*xnode{
		nameResourceType: wrap(nameResourceType),
		nameGetETag:      leaf(nameGetETag, itemETag(it)),
		nameContentType:  leaf(nameContentType, "text/calendar; charset=utf-8"),
		nameContentLen:   leaf(nameContentLen, strconv.Itoa(len(it.Data))),
		nameLastModified: leaf(nameLastModified, it.Received.UTC().Format(http.TimeFormat)),
		nameCalendarData: leaf(nameCalendarData, string(it.Data)),
	}
}

// defaultProps is what an allprop or empty request lists.
var defaultProps = []xml.Name{nameResourceType, nameDisplayName, nameGetETag, nameContentType, nameContentLen, nameLastModified, nameCurrentUser, nameOwner, namePrivileges, nameReportSet, nameGetCTag}

// response renders one multistatus response: the known properties asked for in
// a 200 propstat, the others in a 404 one.
func response(href string, have map[xml.Name]*xnode, req propRequest) *xnode {
	names := req.names
	if req.all || len(names) == 0 {
		names = nil
		for _, n := range defaultProps {
			if _, ok := have[n]; ok {
				names = append(names, n)
			}
		}
	}
	found, missing := wrap(nameProp), wrap(nameProp)
	for _, n := range names {
		// calendar-data is large; it is sent only when named.
		if v, ok := have[n]; ok && (n != nameCalendarData || slices.Contains(req.names, n)) {
			found.items = append(found.items, xitem{node: v})
		} else {
			missing.items = append(missing.items, xitem{node: &xnode{name: n}})
		}
	}
	resp := wrap(nameResponse, hrefNode(href))
	if hasElements(found) {
		resp.items = append(resp.items, xitem{node: wrap(namePropstat, found, leaf(nameStatus, "HTTP/1.1 200 OK"))})
	}
	if hasElements(missing) {
		resp.items = append(resp.items, xitem{node: wrap(namePropstat, missing, leaf(nameStatus, "HTTP/1.1 404 Not Found"))})
	}
	return resp
}

func writeMultistatus(w http.ResponseWriter, responses []*xnode) {
	root := wrap(xml.Name{Space: davNS, Local: "multistatus"})
	for _, r := range responses {
		root.items = append(root.items, xitem{node: r})
	}
	var out bytes.Buffer
	out.WriteString(xml.Header)
	enc := xml.NewEncoder(&out)
	if err := root.encode(enc); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := enc.Flush(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write(out.Bytes())
}

func (s *scheduleServer) items(w http.ResponseWriter, user string) ([]store.ScheduleItem, bool) {
	if s.b.inbox == nil {
		return nil, true
	}
	items, err := s.b.inbox.List(user)
	if err != nil {
		s.logger.Warn("caldav: reading the schedule inbox failed", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	return items, true
}

func readBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("request body too large")
	}
	return body, nil
}

func (s *scheduleServer) propfind(w http.ResponseWriter, r *http.Request, sp schedulePath) {
	body, err := readBody(r, 64<<10)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req, err := parseRequest(body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	depth := r.Header.Get("Depth")
	switch sp.kind {
	case scheduleOutbox:
		writeMultistatus(w, []*xnode{response(outboxPath(sp.user), collectionProps(sp, nil), req)})
	case scheduleInbox:
		items, ok := s.items(w, sp.user)
		if !ok {
			return
		}
		out := []*xnode{response(inboxPath(sp.user), collectionProps(sp, items), req)}
		if depth != "0" {
			for _, it := range items {
				out = append(out, response(itemHref(sp.user, it), itemProps(it), req))
			}
		}
		writeMultistatus(w, out)
	case scheduleInboxItem:
		it, ok := s.item(w, sp)
		if !ok {
			return
		}
		writeMultistatus(w, []*xnode{response(itemHref(sp.user, *it), itemProps(*it), req)})
	}
}

// item reads one inbox item, answering 404 itself when there is none.
func (s *scheduleServer) item(w http.ResponseWriter, sp schedulePath) (*store.ScheduleItem, bool) {
	if s.b.inbox == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	}
	it, err := s.b.inbox.Get(sp.user, sp.item)
	switch {
	case errors.Is(err, store.ErrScheduleItemNotFound):
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	case err != nil:
		s.logger.Warn("caldav: reading a schedule inbox item failed", "user", sp.user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	return it, true
}

func (s *scheduleServer) getItem(w http.ResponseWriter, r *http.Request, sp schedulePath) {
	it, ok := s.item(w, sp)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", `"`+itemETag(*it)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(it.Data)))
	w.Header().Set("Last-Modified", it.Received.UTC().Format(http.TimeFormat))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(it.Data)
}

func (s *scheduleServer) deleteItem(w http.ResponseWriter, sp schedulePath) {
	if s.b.inbox == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// A missing item is a 404, as for any resource; the store alone would
	// treat a second DELETE as a success.
	if _, err := s.b.inbox.Get(sp.user, sp.item); err != nil {
		if errors.Is(err, store.ErrScheduleItemNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.logger.Warn("caldav: reading a schedule inbox item failed", "user", sp.user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.b.inbox.Delete(sp.user, sp.item); err != nil {
		if errors.Is(err, store.ErrScheduleItemNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		s.logger.Warn("caldav: deleting a schedule inbox item failed", "user", sp.user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// report answers calendar-multiget and calendar-query on the inbox. A query
// returns every item: the inbox is small and a client filters what it needs.
func (s *scheduleServer) report(w http.ResponseWriter, r *http.Request, sp schedulePath) {
	body, err := readBody(r, 64<<10)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req, err := parseRequest(body)
	if err != nil || len(req.names) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	root, _ := parseXML(body)
	switch root.name {
	case xml.Name{Space: caldavNS, Local: "calendar-multiget"}, xml.Name{Space: caldavNS, Local: "calendar-query"}:
	default:
		writePrecondition(w, http.StatusForbidden, xml.Name{Space: davNS, Local: "supported-report"})
		return
	}
	items, ok := s.items(w, sp.user)
	if !ok {
		return
	}
	var out []*xnode
	if req.multiget {
		byHref := map[string]store.ScheduleItem{}
		for _, it := range items {
			byHref[itemHref(sp.user, it)] = it
		}
		for _, h := range req.hrefs {
			if u, err := url.PathUnescape(h); err == nil {
				h = u
			}
			it, found := byHref[h]
			if !found {
				out = append(out, wrap(nameResponse, hrefNode(h), leaf(nameStatus, "HTTP/1.1 404 Not Found")))
				continue
			}
			out = append(out, response(h, itemProps(it), req))
		}
	} else {
		for _, it := range items {
			out = append(out, response(itemHref(sp.user, it), itemProps(it), req))
		}
	}
	writeMultistatus(w, out)
}

// writePrecondition answers with a DAV:error naming the failed precondition.
func writePrecondition(w http.ResponseWriter, status int, name xml.Name) {
	var out bytes.Buffer
	out.WriteString(xml.Header)
	enc := xml.NewEncoder(&out)
	_ = wrap(xml.Name{Space: davNS, Local: "error"}, &xnode{name: name}).encode(enc)
	_ = enc.Flush()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out.Bytes())
}

// ---- outbox free/busy ------------------------------------------------------

var (
	nameValidMessage  = xml.Name{Space: caldavNS, Local: "valid-scheduling-message"}
	nameValidOrganzr  = xml.Name{Space: caldavNS, Local: "valid-organizer"}
	nameRecipientCap  = xml.Name{Space: caldavNS, Local: "recipient-limit"}
	nameValidCalendar = xml.Name{Space: caldavNS, Local: "valid-calendar-data"}
)

// postOutbox answers a free/busy request (RFC 6638 section 5). It is the only
// iTIP the outbox takes: invitations, updates and cancellations are sent by
// the implicit scheduling of a PUT or DELETE, and a second way in would send
// the same mail twice.
func (s *scheduleServer) postOutbox(w http.ResponseWriter, r *http.Request, user string) {
	body, err := readBody(r, maxFreeBusyReq)
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	cal, err := ical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		writePrecondition(w, http.StatusBadRequest, nameValidCalendar)
		return
	}
	fb, ok := freeBusyRequest(cal)
	if !ok {
		writePrecondition(w, http.StatusForbidden, nameValidMessage)
		return
	}
	org := ""
	if p := fb.Props.Get(ical.PropOrganizer); p != nil {
		if a, ok := icalevent.AddressFromCalAddress(p.Value); ok {
			org = store.NormaliseAddress(a)
		}
	}
	if org == "" || !slices.Contains(s.b.scheduler.Addresses(user), org) {
		writePrecondition(w, http.StatusForbidden, nameValidOrganzr)
		return
	}
	// An absent or unreadable DTSTART or DTEND is refused.
	start, okS := freeBusyInstant(fb.Props.Get(ical.PropDateTimeStart))
	end, okE := freeBusyInstant(fb.Props.Get(ical.PropDateTimeEnd))
	if !okS || !okE || !end.After(start) || end.Sub(start) > maxFreeBusyRange {
		writePrecondition(w, http.StatusForbidden, nameValidMessage)
		return
	}
	attendees := fb.Props.Values(ical.PropAttendee)
	if len(attendees) == 0 {
		writePrecondition(w, http.StatusForbidden, nameValidMessage)
		return
	}
	if len(attendees) > maxFreeBusyRecipients {
		writePrecondition(w, http.StatusForbidden, nameRecipientCap)
		return
	}

	uid := uidOf(fb)
	root := wrap(xml.Name{Space: caldavNS, Local: "schedule-response"})
	for _, a := range attendees {
		resp := calEl("response")
		addr, valid := icalevent.AddressFromCalAddress(a.Value)
		recipient := wrap(xml.Name{Space: caldavNS, Local: "recipient"}, hrefNode(strings.TrimSpace(a.Value)))
		resp.items = append(resp.items, xitem{node: recipient})
		var busy [][2]int64
		local := false
		if valid {
			busy, local = s.b.scheduler.FreeBusy(user, addr, start.Unix(), end.Unix())
		}
		if !local {
			resp.items = append(resp.items, xitem{node: leaf(xml.Name{Space: caldavNS, Local: "request-status"}, "3.7;Invalid calendar user")})
		} else {
			data, err := freeBusyReply(uid, org, addr, start, end, busy)
			if err != nil {
				s.logger.Warn("caldav: building a free/busy answer failed", "user", user, "error", err)
				resp.items = append(resp.items, xitem{node: leaf(xml.Name{Space: caldavNS, Local: "request-status"}, "5.1;Service unavailable")})
			} else {
				resp.items = append(resp.items,
					xitem{node: leaf(xml.Name{Space: caldavNS, Local: "request-status"}, "2.0;Success")},
					xitem{node: leaf(nameCalendarData, data)})
			}
		}
		root.items = append(root.items, xitem{node: resp})
	}
	var out bytes.Buffer
	out.WriteString(xml.Header)
	enc := xml.NewEncoder(&out)
	if err := root.encode(enc); err != nil || enc.Flush() != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}

// freeBusyRequest returns the VFREEBUSY of a METHOD:REQUEST calendar that holds
// nothing else; anything different is not a free/busy request.
func freeBusyRequest(cal *ical.Calendar) (*ical.Component, bool) {
	m := cal.Props.Get(ical.PropMethod)
	if m == nil || !strings.EqualFold(strings.TrimSpace(m.Value), imip.MethodRequest) {
		return nil, false
	}
	var fb *ical.Component
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompFreeBusy:
			if fb != nil {
				return nil, false
			}
			fb = c
		case ical.CompTimezone:
		default:
			return nil, false
		}
	}
	return fb, fb != nil
}

// freeBusyReply builds the METHOD:REPLY calendar for one attendee. Overlapping
// periods are merged and clipped to the requested range.
func freeBusyReply(uid, organiser, attendee string, start, end time.Time, busy [][2]int64) (string, error) {
	periods := mergeBusy(busy, start.Unix(), end.Unix())
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Bongo//Calendar//EN")
	cal.Props.SetText(ical.PropMethod, imip.MethodReply)
	fb := ical.NewComponent(ical.CompFreeBusy)
	if uid != "" {
		fb.Props.SetText(ical.PropUID, uid)
	}
	fb.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	fb.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
	fb.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())
	fb.Props.Set(icalevent.OrganizerProp(organiser))
	fb.Props.Set(icalevent.AttendeeProp(&bongov1.Attendee{Address: attendee}))
	for _, p := range periods {
		prop := ical.NewProp("FREEBUSY")
		prop.Params.Set("FBTYPE", "BUSY")
		prop.Value = time.Unix(p[0], 0).UTC().Format("20060102T150405Z") + "/" + time.Unix(p[1], 0).UTC().Format("20060102T150405Z")
		fb.Props.Add(prop)
	}
	cal.Children = append(cal.Children, fb)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", fmt.Errorf("encoding free/busy answer: %w", err)
	}
	return buf.String(), nil
}

// mergeBusy sorts periods, clips them to [from, to] and joins overlaps.
func mergeBusy(busy [][2]int64, from, to int64) [][2]int64 {
	var clipped [][2]int64
	for _, p := range busy {
		s, e := max(p[0], from), min(p[1], to)
		if e > s {
			clipped = append(clipped, [2]int64{s, e})
		}
	}
	slices.SortFunc(clipped, func(a, b [2]int64) int {
		if a[0] != b[0] {
			return int(a[0] - b[0])
		}
		return int(a[1] - b[1])
	})
	var out [][2]int64
	for _, p := range clipped {
		if n := len(out); n > 0 && p[0] <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], p[1])
			continue
		}
		out = append(out, p)
	}
	return out
}

// freeBusyInstant reads a free-busy request's DTSTART or DTEND through the
// zone resolver. A nil property is not ok.
func freeBusyInstant(p *ical.Prop) (time.Time, bool) {
	if p == nil {
		return time.Time{}, false
	}
	t, _, ok := icalevent.PropInstant(p, time.UTC)
	return t, ok
}
