package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
)

// davSlots is how many hrefs a client rotates its writes over, so a long run
// updates a few objects instead of growing the collection.
const davSlots = 4

// davRT wraps the transport and records every HTTP request: its method, the
// status class and the time to the end of the body. One library call can make
// several requests, so this is the only place true per-request latency is
// visible. It never reads or keeps a URL, header or body.
type davRT struct {
	base http.RoundTripper
	st   *clientStats
	hard context.Context
	// errs counts requests that failed, so a caller can tell a library error
	// that was already counted from one that was not.
	errs atomic.Int64
	// auths counts 401 answers.
	auths atomic.Int64
}

func davOpFor(method string) string {
	switch method {
	case "PROPFIND":
		return opDAVPropfind
	case "REPORT":
		return opDAVReport
	case http.MethodPut:
		return opDAVPut
	}
	return ""
}

func (t *davRT) RoundTrip(req *http.Request) (*http.Response, error) {
	op := davOpFor(req.Method)
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.fail(op, classifyTransport(err))
		return nil, err
	}
	done := func() {
		if op == "" {
			return
		}
		switch sc := resp.StatusCode; {
		case sc == http.StatusUnauthorized:
			t.auths.Add(1)
			t.fail(op, classAuth)
		case sc >= 500:
			t.fail(op, classHTTP5xx)
		case sc >= 400:
			t.fail(op, classHTTP4xx)
		case sc >= 300 || sc < 200:
			// A redirect is never followed, so it is an error here.
			t.fail(op, classOther)
		default:
			t.st.ok(op, time.Since(start))
		}
	}
	resp.Body = &timedBody{ReadCloser: resp.Body, done: done}
	return resp, nil
}

func (t *davRT) fail(op string, c errClass) {
	t.errs.Add(1)
	// Errors caused by our own shutdown are not the server's.
	if op != "" && t.hard.Err() == nil {
		t.st.fail(op, c)
	}
}

// timedBody runs done once, when the body is first read to the end or closed.
type timedBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *timedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.done)
	}
	return n, err
}

func (b *timedBody) Close() error {
	b.once.Do(b.done)
	return b.ReadCloser.Close()
}

// davClient is one simulated CalDAV and CardDAV client.
type davClient struct {
	r    *clientsRunner
	idx  int
	a    account
	rng  *rand.Rand
	rt   *davRT
	tr   *http.Transport
	cal  *caldav.Client
	card *carddav.Client

	ready             bool
	calPath, bookPath string
	calHome, bookHome string
	writes            int
}

// newHTTPClient builds the client every DAV request goes through. Dialling
// uses the guarded dialer, the environment proxy is ignored and redirects are
// not followed, so no request can reach a host that checkTarget did not see.
func newHTTPClient(dial dialFunc, insecure bool, timeout time.Duration, rt func(http.RoundTripper) http.RoundTripper) (*http.Client, *http.Transport) {
	tr := &http.Transport{
		DialContext: dial,
		Proxy:       nil, // never honour HTTP_PROXY: it would route around checkTarget
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// #nosec G402 -- opt-in for self-signed certificates on an allow-listed test target
			InsecureSkipVerify: insecure,
		},
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: timeout,
	}
	var rtr http.RoundTripper = tr
	if rt != nil {
		rtr = rt(tr)
	}
	return &http.Client{
		Transport: rtr,
		Timeout:   timeout,
		// A redirect could point at a host that was never checked, and
		// /.well-known discovery is never used.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, tr
}

func (r *clientsRunner) davWorker(idx int) {
	a := r.account(kindDAV, idx)
	d := &davClient{r: r, idx: idx, a: a, rng: r.rngFor(kindDAV, idx)}
	if !sleep(r.soft, r.startDelay()) {
		return
	}
	rt := &davRT{st: r.st, hard: r.hard}
	hc, tr := newHTTPClient(r.dial, r.cfg.InsecureTLS, r.cfg.Timeout, func(base http.RoundTripper) http.RoundTripper {
		rt.base = base
		return rt
	})
	defer tr.CloseIdleConnections()
	d.rt, d.tr = rt, tr
	auth := webdav.HTTPClientWithBasicAuth(hc, a.email, a.pw.Reveal())
	base := "https://" + r.cfg.DAVAddr
	var err error
	if d.cal, err = caldav.NewClient(auth, base+"/caldav/"); err != nil {
		return
	}
	if d.card, err = carddav.NewClient(auth, base+"/carddav/"); err != nil {
		return
	}
	authFails := int64(0)
	for n := 0; r.soft.Err() == nil; n++ {
		start := time.Now()
		r.st.davCycles.Add(1)
		before := rt.auths.Load()
		d.cycle()
		if rt.auths.Load() > before {
			authFails++
			if authFails >= maxAuthFailures {
				r.st.davStoppedAuth.Add(1)
				return
			}
		} else {
			authFails = 0
		}
		if !sleep(r.soft, r.cfg.DAVInterval-time.Since(start)) {
			return
		}
	}
}

// call runs one library call under the operation timeout. A failure the
// transport already counted is not counted again; one it did not see (a
// response the library could not parse) is counted as other under op.
func (d *davClient) call(op string, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(d.r.hard, d.r.cfg.Timeout)
	defer cancel()
	before := d.rt.errs.Load()
	err := fn(ctx)
	if err != nil && d.rt.errs.Load() == before && d.r.hard.Err() == nil {
		d.r.st.fail(op, classOther)
	}
	return err
}

// setup discovers the home sets and picks a VEVENT calendar and the first
// address book. It repeats on the next cycle if it fails. New users have a
// "tasks" calendar that holds VTODO only, and a VEVENT PUT there is refused,
// so only a calendar that allows VEVENT (or names no components) is used. A
// subscription calendar cannot be told apart from the protocol's listing;
// seeded users have none.
func (d *davClient) setup() {
	var principal string
	if d.call(opDAVPropfind, func(ctx context.Context) (err error) {
		principal, err = d.cal.FindCurrentUserPrincipal(ctx)
		return
	}) != nil {
		return
	}
	if d.call(opDAVPropfind, func(ctx context.Context) (err error) {
		d.calHome, err = d.cal.FindCalendarHomeSet(ctx, principal)
		return
	}) != nil {
		return
	}
	cals, ok := d.findCalendars()
	if !ok {
		return
	}
	for _, c := range cals {
		if len(c.SupportedComponentSet) == 0 || slices.Contains(c.SupportedComponentSet, ical.CompEvent) {
			d.calPath = c.Path
			break
		}
	}
	var cardPrincipal string
	if d.call(opDAVPropfind, func(ctx context.Context) (err error) {
		cardPrincipal, err = d.card.FindCurrentUserPrincipal(ctx)
		return
	}) != nil {
		return
	}
	if d.call(opDAVPropfind, func(ctx context.Context) (err error) {
		d.bookHome, err = d.card.FindAddressBookHomeSet(ctx, cardPrincipal)
		return
	}) != nil {
		return
	}
	books, ok := d.findBooks()
	if !ok {
		return
	}
	if len(books) > 0 {
		d.bookPath = books[0].Path
	}
	d.ready = d.calPath != "" && d.bookPath != ""
}

func (d *davClient) findCalendars() (cals []caldav.Calendar, ok bool) {
	err := d.call(opDAVPropfind, func(ctx context.Context) (e error) {
		cals, e = d.cal.FindCalendars(ctx, d.calHome)
		return
	})
	return cals, err == nil
}

func (d *davClient) findBooks() (books []carddav.AddressBook, ok bool) {
	err := d.call(opDAVPropfind, func(ctx context.Context) (e error) {
		books, e = d.card.FindAddressBooks(ctx, d.bookHome)
		return
	})
	return books, err == nil
}

// cycle is one poll: PROPFIND the collections, REPORT their contents, and with
// probability --clients-dav-write-share PUT one object, alternating between
// an event and a contact.
func (d *davClient) cycle() {
	if !d.ready {
		d.setup()
		if !d.ready {
			return
		}
	}
	_, _ = d.findCalendars()
	_, _ = d.findBooks()
	now := time.Now()
	_ = d.call(opDAVReport, func(ctx context.Context) error {
		_, err := d.cal.QueryCalendar(ctx, d.calPath, &caldav.CalendarQuery{
			CompRequest: caldav.CalendarCompRequest{
				Name: ical.CompCalendar,
				Comps: []caldav.CalendarCompRequest{{
					Name: ical.CompEvent, Props: []string{ical.PropUID, ical.PropSummary, ical.PropDateTimeStart},
				}},
			},
			CompFilter: caldav.CompFilter{
				Name: ical.CompCalendar,
				Comps: []caldav.CompFilter{{
					Name: ical.CompEvent, Start: now.Add(-24 * time.Hour), End: now.Add(30 * 24 * time.Hour),
				}},
			},
		})
		return err
	})
	_ = d.call(opDAVReport, func(ctx context.Context) error {
		_, err := d.card.QueryAddressBook(ctx, d.bookPath, &carddav.AddressBookQuery{
			DataRequest: carddav.AddressDataRequest{Props: []string{vcard.FieldUID, vcard.FieldFormattedName}},
			Limit:       20,
		})
		return err
	})
	if d.rng.Float64() < d.r.cfg.DAVWriteShare {
		d.write(now)
	}
}

// write PUTs one event or contact under a deterministic UID, rotating over
// davSlots hrefs.
func (d *davClient) write(now time.Time) {
	n := d.writes
	d.writes++
	slot := (n / 2) % davSlots
	if n%2 == 0 {
		uid := fmt.Sprintf("loadgen-%s-c%d-%d", d.r.runID, d.idx, slot)
		_ = d.call(opDAVPut, func(ctx context.Context) error {
			_, err := d.cal.PutCalendarObject(ctx, davObjectPath(d.calPath, uid+".ics"), buildEvent(uid, now, n))
			return err
		})
	} else {
		uid := fmt.Sprintf("loadgen-%s-a%d-%d", d.r.runID, d.idx, slot)
		_ = d.call(opDAVPut, func(ctx context.Context) error {
			_, err := d.card.PutAddressObject(ctx, davObjectPath(d.bookPath, uid+".vcf"), buildContact(uid, n))
			return err
		})
	}
	d.r.st.davWrites.Add(1)
}

func buildEvent(uid string, now time.Time, n int) *ical.Calendar {
	ev := ical.NewEvent()
	start := now.Add(time.Hour).Truncate(time.Hour).UTC()
	ev.Props.SetText(ical.PropUID, uid)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	ev.Props.SetDateTime(ical.PropDateTimeStart, start)
	ev.Props.SetDateTime(ical.PropDateTimeEnd, start.Add(time.Hour))
	ev.Props.SetText(ical.PropSummary, fmt.Sprintf("loadgen event %d", n))
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//loadgen//clients//EN")
	cal.Children = append(cal.Children, ev.Component)
	return cal
}

func buildContact(uid string, n int) vcard.Card {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "3.0")
	card.SetValue(vcard.FieldUID, uid)
	card.SetValue(vcard.FieldFormattedName, fmt.Sprintf("Loadgen Contact %d", n))
	card.SetValue(vcard.FieldName, "Contact;Loadgen;;;")
	return card
}

// davObjectPath places an object inside a collection. A discovered collection
// path may come back with or without its trailing slash, and plain
// concatenation would then name a sibling of the collection, not a member.
func davObjectPath(collection, name string) string {
	return strings.TrimSuffix(collection, "/") + "/" + name
}
