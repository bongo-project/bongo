package caldav

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-message"
	"github.com/emersion/go-webdav/caldav"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// iTIP messages the real scheduler handed to delivery, in order.
type mailedITIP struct {
	method, from string
	to           []string
	seq          string
	calendar     string // the text/calendar part
}

type recordingSender struct {
	mu   sync.Mutex
	sent []mailedITIP
}

func (r *recordingSender) DeliverGenerated(_, _, from string, rcpts []string, raw []byte) ([]string, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		return rcpts, err
	}
	m := mailedITIP{from: from, to: append([]string(nil), rcpts...)}
	mr := entity.MultipartReader()
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		if mt, _, _ := part.Header.ContentType(); mt != "multipart/alternative" {
			continue
		}
		ar := part.MultipartReader()
		for {
			sub, err := ar.NextPart()
			if err != nil {
				break
			}
			if mt, params, _ := sub.Header.ContentType(); mt == "text/calendar" {
				body, _ := io.ReadAll(sub.Body)
				m.calendar, m.method = string(body), strings.ToUpper(params["method"])
				if cal, err := ical.NewDecoder(bytes.NewReader(body)).Decode(); err == nil {
					if ve := masterOf(cal); ve != nil {
						if p := ve.Props.Get(ical.PropSequence); p != nil {
							m.seq = p.Value
						}
					}
				}
			}
		}
	}
	r.mu.Lock()
	r.sent = append(r.sent, m)
	r.mu.Unlock()
	return nil, nil
}

// tally counts messages as "METHOD recipient".
func (r *recordingSender) tally() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for _, m := range r.sent {
		for _, to := range m.to {
			out[m.method+" "+to]++
		}
	}
	return out
}

type fixedEmail struct{}

func (fixedEmail) UserEmail(string) string { return schEmail }

// realSchedEnv is the CalDAV endpoint wired to the real CalDAV scheduler of
// the web handlers, with only delivery replaced by a recorder.
func realSchedEnv(t *testing.T) (*schedEnv, *recordingSender) {
	t.Helper()
	return realSchedEnvWith(t, nil)
}

// realSchedEnvWith is realSchedEnv with a hook that wires more of the handler
// before the CalDAV endpoint is built.
func realSchedEnvWith(t *testing.T, wire func(h *handlers.CalendarHandler, ps *store.PebbleStore, app *grantApplier)) (*schedEnv, *recordingSender) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &grantApplier{ps: ps}
	events := store.NewEventStore(ps, app)
	cals := store.NewCalendarStore(ps, app)
	if _, err := cals.EnsureDefault(schUser); err != nil {
		t.Fatal(err)
	}
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mm.Close() })
	addrs := store.NewAddressStore(ps, app, mm.System)
	// sales@d.test is a plain alias of the user: owned, not sendable.
	if err := addrs.Put("sales@d.test", &bongov1.AddressRecord{UserId: schUser, Domain: "d.test", Kind: "alias"}); err != nil {
		t.Fatal(err)
	}
	rec := &recordingSender{}
	h := handlers.NewCalendarHandler(events, cals, nil)
	h.SetInviteSender(rec)
	h.SetAddressStore(addrs)
	if wire != nil {
		wire(h, ps, app)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBackend(events, cals, logger)
	b.SetScheduler(handlers.NewCalDAVScheduler(h, nil, fixedEmail{}))
	inbox := store.NewScheduleInboxStore(ps, app)
	b.SetScheduleInbox(inbox)
	handler := ScheduleHandler(PrivilegeFilter(&caldav.Handler{Backend: b, Prefix: "/caldav"}, b.ReadOnlyShare, logger), b, logger)
	e := &schedEnv{t: t, b: b, events: events, inbox: inbox, ps: ps, app: app}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), r.Header.Get("X-Test-User"))))
	}))
	t.Cleanup(e.srv.Close)
	return e, rec
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(strings.ReplaceAll(string(b), "\r", ""), "\n", "\r\n")
}

func (r *recordingSender) check(t *testing.T, step string, want map[string]int) {
	t.Helper()
	got := r.tally()
	if len(got) == 0 {
		got = map[string]int{}
	}
	var a, b []string
	for k, v := range got {
		a = append(a, fmt.Sprintf("%s=%d", k, v))
	}
	for k, v := range want {
		b = append(b, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, ", ") != strings.Join(b, ", ") {
		t.Fatalf("after %s the server has mailed\n  %v\nwant\n  %v", step, a, b)
	}
}

// A client that sees calendar-auto-schedule stops sending iMIP itself, so the
// server's own messages must be exactly one REQUEST per attendee: not one more
// for the re-upload every client makes once the server has answered, and not
// one for a change that tells the attendees nothing.
func TestClientRecordingsSendOneRequestPerAttendee(t *testing.T) {
	const bob, carol = "bob@example.org", "carol@other.example"
	tests := []struct {
		name, propfind, uid string
		create, reput       string
		edit                string // a change that must be mailed, or ""
	}{
		{"Apple Calendar", "apple_propfind_principal.xml", "APPLE-0F7A-4B21", "apple_put_create.ics", "apple_put_reput.ics", "apple_put_moved.ics"},
		{"Thunderbird", "thunderbird_propfind_principal.xml", "5c1f4b3e-7b1a-4a39-b0b3-1d3e0e9a77aa", "thunderbird_put_create.ics", "thunderbird_put_reput.ics", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, rec := realSchedEnv(t)

			// Discovery.
			code, body, _ := e.do(schUser, "PROPFIND", "/caldav/"+schUser+"/", fixture(t, tt.propfind),
				map[string]string{"Depth": "0", "Content-Type": "application/xml"})
			if code != http.StatusMultiStatus {
				t.Fatalf("PROPFIND = %d: %s", code, body)
			}
			ok := strings.Join(propstats(t, body)["HTTP/1.1 200 OK"], " ")
			for _, want := range []string{"schedule-inbox-URL=/caldav/" + schUser + "/inbox/", "schedule-outbox-URL=/caldav/" + schUser + "/outbox/", "calendar-user-address-set=mailto:" + schEmail} {
				if !strings.Contains(ok, want) {
					t.Errorf("principal answer lacks %q: %v", want, ok)
				}
			}

			path := objPath("obj")
			// First upload: one REQUEST to each attendee, none to the organiser.
			if code, body, _ := e.do(schUser, "PUT", path, fixture(t, tt.create), nil); code != http.StatusCreated {
				t.Fatalf("PUT = %d: %s", code, body)
			}
			rec.check(t, "the first upload", map[string]int{"REQUEST " + bob: 1, "REQUEST " + carol: 1})
			for _, m := range rec.sent {
				if m.from != schEmail {
					t.Errorf("sent as %q, want the organiser", m.from)
				}
				if strings.Contains(strings.ToUpper(m.calendar), "SCHEDULE-") {
					t.Errorf("outgoing iTIP carries a scheduling parameter:\n%s", m.calendar)
				}
			}
			ev := firstEvent(t, e)
			if got := attendeeParam(t, ev.IcalRaw, bob, "SCHEDULE-STATUS"); got != "1.1" {
				t.Errorf("SCHEDULE-STATUS = %q after the send", got)
			}

			// The re-upload, with the status the server wrote and other
			// churn (DTSTAMP, an alarm, X- properties): nothing more is sent.
			if code, body, _ := e.do(schUser, "PUT", path, fixture(t, tt.reput), nil); code != http.StatusCreated {
				t.Fatalf("re-PUT = %d: %s", code, body)
			}
			rec.check(t, "the re-upload", map[string]int{"REQUEST " + bob: 1, "REQUEST " + carol: 1})
			// ... and a third time, byte for byte.
			e.do(schUser, "PUT", path, fixture(t, tt.reput), nil)
			rec.check(t, "the same upload again", map[string]int{"REQUEST " + bob: 1, "REQUEST " + carol: 1})

			want := map[string]int{"REQUEST " + bob: 1, "REQUEST " + carol: 1}
			if tt.edit != "" {
				if code, body, _ := e.do(schUser, "PUT", path, fixture(t, tt.edit), nil); code != http.StatusCreated {
					t.Fatalf("edit PUT = %d: %s", code, body)
				}
				want["REQUEST "+bob], want["REQUEST "+carol] = 2, 2
				rec.check(t, "a time change", want)
				if last := rec.sent[len(rec.sent)-1]; last.seq != "1" {
					t.Errorf("the update carries SEQUENCE %q, want 1", last.seq)
				}
			}

			if code, _, _ := e.do(schUser, "DELETE", path, "", nil); code != http.StatusNoContent {
				t.Fatalf("DELETE = %d", code)
			}
			want["CANCEL "+bob], want["CANCEL "+carol] = 1, 1
			rec.check(t, "the delete", want)
		})
	}
}

func firstEvent(t *testing.T, e *schedEnv) *bongov1.Event {
	t.Helper()
	ev, err := e.events.Get(schUser, "obj")
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// A room's free/busy is answered over the outbox to users of its own domain,
// and refused like an unknown address to everyone else.
func TestOutboxFreeBusyOfAResource(t *testing.T) {
	const room = "room@d.test"
	put := func(t *testing.T, app *grantApplier, id string, p *bongov1.UserProfile) {
		t.Helper()
		data, err := proto.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + id + "/profile"), Value: data}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := realSchedEnvWith(t, func(h *handlers.CalendarHandler, ps *store.PebbleStore, app *grantApplier) {
		as := store.NewAddressStore(ps, app, nil)
		if err := as.Put(room, &bongov1.AddressRecord{UserId: "room-id", Domain: "d.test", Kind: "primary", Verified: true}); err != nil {
			t.Fatal(err)
		}
		put(t, app, "room-id", &bongov1.UserProfile{Email: room, Kind: "resource", ResourceType: "room"})
		put(t, app, schUser, &bongov1.UserProfile{Email: schEmail})
		put(t, app, "carl-id", &bongov1.UserProfile{Email: "carl@other.test"})
		h.SetAddressStore(as)
		h.SetProfiles(ps)
	})
	if _, err := e.events.Create("room-id", &bongov1.Event{Guid: "b1", Title: "Booked", StartTime: 1904288400, EndTime: 1904292000}); err != nil {
		t.Fatal(err)
	}
	post := func(user string) string {
		code, body, _ := e.do(user, "POST", "/caldav/"+user+"/outbox/", freeBusyPost("REQUEST", "VFREEBUSY", schEmail, room), map[string]string{"Content-Type": "text/calendar"})
		if code != http.StatusOK {
			t.Fatalf("POST = %d: %s", code, body)
		}
		return body
	}
	if body := post(schUser); !strings.Contains(body, "FBTYPE=BUSY:20300506T090000Z/20300506T100000Z") || !strings.Contains(body, "2.0;Success") {
		t.Errorf("a user of the room's domain got no busy block:\n%s", body)
	}
	if body := post("carl-id"); !strings.Contains(body, "3.7;Invalid calendar user") || strings.Contains(body, "FREEBUSY;") {
		t.Errorf("a user of another domain was answered:\n%s", body)
	}
}
