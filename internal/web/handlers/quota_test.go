package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeQuota mirrors DomainQuotaStore semantics per user: headroom is
// max-usage; a write is refused when headroom <= 0 or addBytes > headroom.
// A user with no entry is unlimited.
type fakeQuota struct {
	headroom map[string]int64
	err      error
	calls    int
}

func (f *fakeQuota) RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	h, ok := f.headroom[userID]
	if !ok {
		return false, nil
	}
	return h <= 0 || addBytes > h, nil
}

const quotaUser = "u-quota"
const quotaEmail = "quota@example.com"

type quotaEnv struct {
	ps        *store.PebbleStore
	blobs     *store.BlobStore
	blobDir   string
	mailboxes *store.MailboxStore
	events    *store.EventStore
	contacts  *store.ContactStore
	calendars *store.CalendarStore
}

func newQuotaEnv(t *testing.T) *quotaEnv {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	blobs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	a := &directApplierTags{ps: ps}
	return &quotaEnv{
		ps: ps, blobs: blobs, blobDir: dir + "/blobs",
		mailboxes: store.NewMailboxStore(ps, a),
		events:    store.NewEventStore(ps, a),
		contacts:  store.NewContactStore(ps, a),
		calendars: store.NewCalendarStore(ps, a),
	}
}

func (e *quotaEnv) countEvents(t *testing.T) int {
	t.Helper()
	_, n, err := e.events.ListAll(quotaUser, 0, 0)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	return n
}

func (e *quotaEnv) countContacts(t *testing.T) int {
	t.Helper()
	_, n, err := e.contacts.List(quotaUser, 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return n
}

func (e *quotaEnv) countMail(t *testing.T, userID, mailbox string) int {
	t.Helper()
	msgs, err := e.mailboxes.ListMessages(userID, mailbox)
	if err != nil {
		return 0 // mailbox absent
	}
	return len(msgs)
}

func formReq(method, target string, form url.Values) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return withUserContext(req, quotaUser, quotaEmail)
}

func uploadReq(t *testing.T, target, field string, content []byte, extra map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, "upload.dat")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(content)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return withUserContext(req, quotaUser, quotaEmail)
}

// withGUID routes req through a chi context so chi.URLParam("guid") resolves.
func withGUID(req *http.Request, guid string) *http.Request {
	rc := chi.NewRouteContext()
	rc.URLParams.Add("guid", guid)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
}

// Non-round byte counts: the boundary must not coincide with a round number.
const mboxMsgA = "From a@x.example Mon Jan  1 00:00:00 2024\nFrom: a@x.example\nTo: quota@example.com\nSubject: one\nMessage-ID: <a1@x>\n\nbody of the first message\n"
const mboxMsgB = "From b@x.example Mon Jan  1 00:00:01 2024\nFrom: b@x.example\nTo: quota@example.com\nSubject: two\nMessage-ID: <b1@x>\n\nsecond, slightly longer, body 12345\n"

func TestImportMboxQuota(t *testing.T) {
	mbox := []byte(mboxMsgA + mboxMsgB)
	size := int64(len(mbox))
	if size%1000 == 0 || size%1024 == 0 {
		t.Fatalf("fixture size %d is a round number", size)
	}
	tests := []struct {
		name         string
		headroom     int64
		hasLimit     bool
		wantImported int
	}{
		{"headroom equals payload imports everything", size, true, 2},
		{"one byte short imports nothing", size - 1, true, 0},
		{"user at quota imports nothing", 0, true, 0},
		{"unlimited imports everything", 0, false, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newQuotaEnv(t)
			h := NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger())
			fq := &fakeQuota{headroom: map[string]int64{}}
			if tc.hasLimit {
				fq.headroom[quotaUser] = tc.headroom
			}
			h.SetQuotaChecker(fq)

			req := uploadReq(t, "/import", "mbox", mbox, map[string]string{"new_mailbox": "Imported"})
			w := httptest.NewRecorder()
			h.ImportMbox(w, req)

			if got := env.countMail(t, quotaUser, "Imported"); got != tc.wantImported {
				t.Fatalf("imported messages = %d, want %d (status %d: %s)", got, tc.wantImported, w.Code, w.Body.String())
			}
			if tc.wantImported == 0 {
				if w.Code != http.StatusInsufficientStorage {
					t.Errorf("status = %d, want 507", w.Code)
				}
				if _, err := env.mailboxes.GetMailbox(quotaUser, "Imported"); err == nil {
					t.Error("refused import must not create the target mailbox")
				}
				if !strings.Contains(w.Body.String(), "storage quota") {
					t.Errorf("body %q should explain the quota refusal", w.Body.String())
				}
			} else if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
		})
	}
}

func TestImportMboxQuotaFailsOpen(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger())
	h.SetQuotaChecker(&fakeQuota{err: errors.New("pebble hiccup")})
	w := httptest.NewRecorder()
	h.ImportMbox(w, uploadReq(t, "/import", "mbox", []byte(mboxMsgA), map[string]string{"new_mailbox": "Imported"}))
	if got := env.countMail(t, quotaUser, "Imported"); got != 1 {
		t.Fatalf("checker error must fail open: imported %d, want 1 (status %d)", got, w.Code)
	}
}

func eventForm(title string) url.Values {
	return url.Values{
		"title":      {title},
		"start_date": {"2030-05-06"},
		"start_time": {"09:00"},
		"end_date":   {"2030-05-06"},
		"end_time":   {"10:00"},
	}
}

func TestCalendarQuota(t *testing.T) {
	const ics = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:q1@x\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\nSUMMARY:Imported one\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	over := map[string]int64{quotaUser: 0}
	room := map[string]int64{quotaUser: 1_048_583}

	newHandler := func(t *testing.T, headroom map[string]int64) (*CalendarHandler, *quotaEnv) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		h.SetQuotaChecker(&fakeQuota{headroom: headroom})
		return h, env
	}

	t.Run("create", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			hr   map[string]int64
			want int
			code int
		}{{"over quota refused", over, 0, http.StatusInsufficientStorage}, {"under quota allowed", room, 1, http.StatusSeeOther}} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, tc.hr)
				w := httptest.NewRecorder()
				h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", eventForm("Lunch")))
				if w.Code != tc.code || env.countEvents(t) != tc.want {
					t.Fatalf("status=%d events=%d, want status=%d events=%d", w.Code, env.countEvents(t), tc.code, tc.want)
				}
			})
		}
	})

	t.Run("create boundary uses stored record size", func(t *testing.T) {
		form := eventForm("Lunch")
		form.Set("description", strings.Repeat("d", 1337))
		parsed, err := parseEventForm(formReq(http.MethodPost, "/", form))
		if err != nil {
			t.Fatal(err)
		}
		size := store.EventRecordSize(parsed)
		if size < 1337 {
			t.Fatalf("estimate %d smaller than the description", size)
		}
		for _, tc := range []struct {
			name string
			hr   int64
			want int
		}{{"exact fit allowed", size, 1}, {"one byte short refused", size - 1, 0}} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, map[string]int64{quotaUser: tc.hr})
				w := httptest.NewRecorder()
				h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
				if env.countEvents(t) != tc.want {
					t.Fatalf("events=%d want %d (status %d)", env.countEvents(t), tc.want, w.Code)
				}
				if tc.want == 1 {
					// The estimate must equal what was actually stored.
					evs, _, _ := env.events.ListAll(quotaUser, 0, 0)
					raw, err := env.ps.Get([]byte("/user/" + quotaUser + "/event/" + evs[0].Guid))
					if err != nil || int64(len(raw)) != size {
						t.Fatalf("stored %d bytes (err %v), estimate %d", len(raw), err, size)
					}
				}
			})
		}
	})

	t.Run("update growth delta against headroom", func(t *testing.T) {
		seedForm := eventForm("Standup")
		grownForm := eventForm("Standup")
		grownForm.Set("description", strings.Repeat("g", 613))
		seed, _ := parseEventForm(formReq(http.MethodPut, "/", seedForm))
		grown, _ := parseEventForm(formReq(http.MethodPut, "/", grownForm))
		delta := int64(proto.Size(grown) - proto.Size(seed))
		if delta < 613 {
			t.Fatalf("delta %d", delta)
		}
		for _, tc := range []struct {
			name string
			form url.Values
			hr   int64
			code int
		}{
			{"growth fits exactly", grownForm, delta, http.StatusSeeOther},
			{"growth one byte over refused", grownForm, delta - 1, http.StatusInsufficientStorage},
			{"shrink at zero headroom allowed", seedForm, 0, http.StatusSeeOther},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, map[string]int64{quotaUser: tc.hr})
				start := seed
				if tc.name == "shrink at zero headroom allowed" {
					start = grown
				}
				guid, err := env.events.Create(quotaUser, start)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, tc.form), guid))
				if w.Code != tc.code {
					t.Fatalf("status=%d want %d", w.Code, tc.code)
				}
			})
		}
	})

	t.Run("import", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			hr   map[string]int64
			want int
		}{{"over quota refused", over, 0}, {"under quota allowed", room, 1}} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, tc.hr)
				w := httptest.NewRecorder()
				h.ImportCalendar(w, uploadReq(t, "/calendar/import", "ics", []byte(ics), nil))
				if w.Code != http.StatusSeeOther || env.countEvents(t) != tc.want {
					t.Fatalf("status=%d events=%d, want 303 and %d events", w.Code, env.countEvents(t), tc.want)
				}
				if tc.want == 0 && !strings.Contains(w.Header().Get("Location"), "quota") {
					t.Errorf("redirect %q should carry the quota message", w.Header().Get("Location"))
				}
			})
		}
	})

	t.Run("import refused when payload does not fit", func(t *testing.T) {
		h, env := newHandler(t, map[string]int64{quotaUser: int64(len(ics)) - 1})
		w := httptest.NewRecorder()
		h.ImportCalendar(w, uploadReq(t, "/calendar/import", "ics", []byte(ics), nil))
		if env.countEvents(t) != 0 {
			t.Fatalf("payload larger than headroom must store nothing, got %d events", env.countEvents(t))
		}
	})

	t.Run("update at quota", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			newTitle  string
			wantTitle string
			wantCode  int
		}{
			{"same size allowed", "Standup", "Standup", http.StatusSeeOther},
			{"shrink allowed", "Stand", "Stand", http.StatusSeeOther},
			{"grows by a few bytes refused", "Standupx", "Standup", http.StatusInsufficientStorage},
			{"growth refused", "Standup with a much longer title", "Standup", http.StatusInsufficientStorage},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, over)
				seed, err := parseEventForm(formReq(http.MethodPut, "/", eventForm("Standup")))
				if err != nil {
					t.Fatal(err)
				}
				if tc.newTitle == "Standupx" {
					grown, err := parseEventForm(formReq(http.MethodPut, "/", eventForm(tc.newTitle)))
					if err != nil {
						t.Fatal(err)
					}
					if d := proto.Size(grown) - proto.Size(seed); d != 1 {
						t.Fatalf("fixture must grow the record by exactly 1 byte, grew by %d", d)
					}
				}
				guid, err := env.events.Create(quotaUser, seed)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, eventForm(tc.newTitle)), guid))
				got, err := env.events.Get(quotaUser, guid)
				if err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.wantCode || got.Title != tc.wantTitle {
					t.Fatalf("status=%d title=%q, want status=%d title=%q", w.Code, got.Title, tc.wantCode, tc.wantTitle)
				}
			})
		}
	})

	t.Run("reschedule at quota keeps size and is allowed", func(t *testing.T) {
		h, env := newHandler(t, over)
		guid, _ := env.events.Create(quotaUser, &bongov1.Event{Title: "Move me", StartTime: 1, EndTime: 2, CalendarId: "default"})
		form := url.Values{"start_time": {"1900000000"}, "end_time": {"1900003600"}}
		w := httptest.NewRecorder()
		h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/calendar/event/"+guid+"/reschedule", form), guid))
		got, _ := env.events.Get(quotaUser, guid)
		if w.Code >= 400 || got.StartTime != 1900000000 {
			t.Fatalf("status=%d start=%d, want a successful reschedule", w.Code, got.StartTime)
		}
	})

	t.Run("fails open on checker error", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		h.SetQuotaChecker(&fakeQuota{err: errors.New("pebble hiccup")})
		w := httptest.NewRecorder()
		h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", eventForm("Lunch")))
		if env.countEvents(t) != 1 {
			t.Fatalf("checker error must fail open, status %d", w.Code)
		}
	})
}

func TestContactsQuota(t *testing.T) {
	const vcf = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Imported Person\r\nEMAIL:imp@x.example\r\nEND:VCARD\r\n"
	over := map[string]int64{quotaUser: 0}
	room := map[string]int64{quotaUser: 1_048_583}

	newHandler := func(t *testing.T, headroom map[string]int64) (*ContactsHandler, *quotaEnv) {
		env := newQuotaEnv(t)
		h := NewContactsHandler(env.contacts, nil)
		h.SetQuotaChecker(&fakeQuota{headroom: headroom})
		return h, env
	}

	for _, tc := range []struct {
		name string
		hr   map[string]int64
		want int
	}{{"over quota refused", over, 0}, {"under quota allowed", room, 1}} {
		t.Run("create/"+tc.name, func(t *testing.T) {
			h, env := newHandler(t, tc.hr)
			w := httptest.NewRecorder()
			h.CreateContact(w, formReq(http.MethodPost, "/contacts", url.Values{"display_name": {"Ada"}, "email": {"ada@x.example"}}))
			if env.countContacts(t) != tc.want {
				t.Fatalf("contacts=%d want %d (status %d)", env.countContacts(t), tc.want, w.Code)
			}
			if tc.want == 0 && w.Code != http.StatusInsufficientStorage {
				t.Errorf("status=%d want 507", w.Code)
			}
		})
		t.Run("quickadd/"+tc.name, func(t *testing.T) {
			h, env := newHandler(t, tc.hr)
			w := httptest.NewRecorder()
			h.QuickAddContact(w, formReq(http.MethodPost, "/contacts/quick-add", url.Values{"from_email": {"q@x.example"}, "from_name": {"Q"}}))
			if env.countContacts(t) != tc.want {
				t.Fatalf("contacts=%d want %d (status %d)", env.countContacts(t), tc.want, w.Code)
			}
			if tc.want == 0 && !strings.Contains(w.Header().Get("Location"), "quota") {
				t.Errorf("redirect %q should carry the quota message", w.Header().Get("Location"))
			}
		})
		t.Run("import/"+tc.name, func(t *testing.T) {
			h, env := newHandler(t, tc.hr)
			w := httptest.NewRecorder()
			h.ImportContacts(w, uploadReq(t, "/contacts/import", "vcf", []byte(vcf), nil))
			if env.countContacts(t) != tc.want {
				t.Fatalf("contacts=%d want %d (status %d)", env.countContacts(t), tc.want, w.Code)
			}
		})
	}

	t.Run("create and quickadd boundary use stored record size", func(t *testing.T) {
		createForm := url.Values{"display_name": {"Ada"}, "email": {"ada@x.example"}, "notes": {strings.Repeat("n", 1337)}}
		c := &bongov1.Contact{}
		buildVCardFromForm(c, formReq(http.MethodPost, "/", createForm))
		createSize := store.ContactRecordSize(c)
		q := &bongov1.Contact{DisplayName: "Q", Email: "q@x.example"}
		syncQuickFieldsToVCard(q)
		quickSize := store.ContactRecordSize(q)
		for _, tc := range []struct {
			name string
			hr   int64
			want int
		}{{"exact fit allowed", 0, 1}, {"one byte short refused", -1, 0}} {
			t.Run("create/"+tc.name, func(t *testing.T) {
				h, env := newHandler(t, map[string]int64{quotaUser: createSize + tc.hr})
				h.CreateContact(httptest.NewRecorder(), formReq(http.MethodPost, "/contacts", createForm))
				if env.countContacts(t) != tc.want {
					t.Fatalf("contacts=%d want %d", env.countContacts(t), tc.want)
				}
				if tc.want == 1 {
					cs, _, _ := env.contacts.List(quotaUser, 0, 0)
					raw, err := env.ps.Get([]byte("/user/" + quotaUser + "/contact/" + cs[0].Guid))
					if err != nil || int64(len(raw)) != createSize {
						t.Fatalf("stored %d bytes (err %v), estimate %d", len(raw), err, createSize)
					}
				}
			})
			t.Run("quickadd/"+tc.name, func(t *testing.T) {
				h, env := newHandler(t, map[string]int64{quotaUser: quickSize + tc.hr})
				h.QuickAddContact(httptest.NewRecorder(), formReq(http.MethodPost, "/contacts/quick-add", url.Values{"from_email": {"q@x.example"}, "from_name": {"Q"}}))
				if env.countContacts(t) != tc.want {
					t.Fatalf("contacts=%d want %d", env.countContacts(t), tc.want)
				}
			})
		}
	})

	t.Run("import refused when payload does not fit", func(t *testing.T) {
		h, env := newHandler(t, map[string]int64{quotaUser: int64(len(vcf)) - 1})
		h.ImportContacts(httptest.NewRecorder(), uploadReq(t, "/contacts/import", "vcf", []byte(vcf), nil))
		if env.countContacts(t) != 0 {
			t.Fatal("payload larger than headroom must store nothing")
		}
	})

	t.Run("update growth delta against headroom", func(t *testing.T) {
		seedForm := url.Values{"display_name": {"Ada"}, "email": {"ada@x.example"}, "notes": {"aaaa"}}
		growForm := url.Values{"display_name": {"Ada"}, "email": {"ada@x.example"}, "notes": {strings.Repeat("g", 613)}}
		// Measure the delta the same way the handler does, on the stored record.
		probe := &bongov1.Contact{}
		buildVCardFromForm(probe, formReq(http.MethodPut, "/", seedForm))
		before := proto.Size(probe)
		buildVCardFromForm(probe, formReq(http.MethodPut, "/", growForm))
		delta := int64(proto.Size(probe) - before)
		if delta <= 0 {
			t.Fatalf("delta=%d, want growth", delta)
		}
		for _, tc := range []struct {
			name     string
			hr       int64
			wantCode int
		}{{"headroom equals delta allowed", delta, http.StatusSeeOther}, {"headroom one below delta refused", delta - 1, http.StatusInsufficientStorage}} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, map[string]int64{quotaUser: tc.hr})
				seed := &bongov1.Contact{}
				buildVCardFromForm(seed, formReq(http.MethodPut, "/", seedForm))
				guid, err := env.contacts.Create(quotaUser, seed)
				if err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				h.UpdateContact(w, withGUID(formReq(http.MethodPut, "/contacts/"+guid, growForm), guid))
				if w.Code != tc.wantCode {
					t.Fatalf("status=%d want %d", w.Code, tc.wantCode)
				}
			})
		}
	})

	t.Run("update at quota", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			notes    string
			wantCode int
		}{
			{"same size allowed", "aaaa", http.StatusSeeOther},
			{"shrink allowed", "a", http.StatusSeeOther},
			{"grows by a few bytes refused", "aaaaa", http.StatusInsufficientStorage},
			{"growth refused", "aaaa and then a good deal more text", http.StatusInsufficientStorage},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, env := newHandler(t, over)
				// Seed through the same builder the handler uses so the derived
				// quick fields match and "same size" really is the same size.
				seed := &bongov1.Contact{}
				buildVCardFromForm(seed, formReq(http.MethodPut, "/", url.Values{"display_name": {"Ada"}, "email": {"ada@x.example"}, "notes": {"aaaa"}}))
				guid, err := env.contacts.Create(quotaUser, seed)
				if err != nil {
					t.Fatal(err)
				}
				form := url.Values{"display_name": {"Ada"}, "email": {"ada@x.example"}, "notes": {tc.notes}}
				w := httptest.NewRecorder()
				h.UpdateContact(w, withGUID(formReq(http.MethodPut, "/contacts/"+guid, form), guid))
				if w.Code != tc.wantCode {
					t.Fatalf("status=%d want %d", w.Code, tc.wantCode)
				}
				if tc.wantCode == http.StatusInsufficientStorage {
					got, err := env.contacts.Get(quotaUser, guid)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got.VcardRaw, seed.VcardRaw) {
						t.Fatalf("refused update must not change the stored vCard, got %q", got.VcardRaw)
					}
				}
			})
		}
	})

	t.Run("fails open on checker error", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewContactsHandler(env.contacts, nil)
		h.SetQuotaChecker(&fakeQuota{err: errors.New("pebble hiccup")})
		h.CreateContact(httptest.NewRecorder(), formReq(http.MethodPost, "/contacts", url.Values{"display_name": {"Ada"}}))
		if env.countContacts(t) != 1 {
			t.Fatal("checker error must fail open")
		}
	})
}

func newQuotaCompose(t *testing.T, fq *fakeQuota) (*composeSendAsEnv, string, string) {
	t.Helper()
	env := newComposeSendAsEnv(t)
	env.seedUser(t, quotaUser, quotaEmail)
	env.seedAddress(t, quotaEmail, quotaUser, "example.com", true, true)
	env.seedUser(t, "u-bob", "bob@example.com")
	env.seedAddress(t, "bob@example.com", "u-bob", "example.com", true, true)
	// The resolver only hands out accounts that have a live profile.
	prof, err := proto.Marshal(&bongov1.UserProfile{Email: "bob@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/u-bob/profile"), Value: prof,
	}, 0); err != nil {
		t.Fatal(err)
	}
	env.handler.SetQuotaChecker(fq)
	return env, quotaUser, quotaEmail
}

func TestComposeSendQuota(t *testing.T) {
	pending := func(t *testing.T, env *composeSendAsEnv) int {
		t.Helper()
		p, err := env.qs.ListPending(50)
		if err != nil {
			t.Fatal(err)
		}
		return len(p)
	}
	count := func(env *composeSendAsEnv, user, mb string) int {
		msgs, err := env.mailboxes.ListMessages(user, mb)
		if err != nil {
			return 0
		}
		return len(msgs)
	}

	tests := []struct {
		name     string
		headroom map[string]int64
		to       []string
		wantCode int
		wantSent int
		wantBob  int
		wantQ    int
	}{
		{"sender over quota refuses the whole send", map[string]int64{quotaUser: 0}, []string{"bob@example.com", "ext@external.example"}, http.StatusInsufficientStorage, 0, 0, 0},
		{"local recipient over quota refuses the whole send", map[string]int64{"u-bob": 0}, []string{"bob@example.com", "ext@external.example"}, http.StatusInsufficientStorage, 0, 0, 0},
		{"under quota succeeds", map[string]int64{quotaUser: 1_048_583, "u-bob": 1_048_583}, []string{"bob@example.com", "ext@external.example"}, http.StatusSeeOther, 1, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, uid, email := newQuotaCompose(t, &fakeQuota{headroom: tc.headroom})
			w := sendCompose(t, env, uid, email, tc.to, nil)
			if w.Code != tc.wantCode {
				t.Fatalf("status=%d want %d: %s", w.Code, tc.wantCode, w.Body.String())
			}
			if got := count(env, uid, "Sent"); got != tc.wantSent {
				t.Errorf("Sent=%d want %d", got, tc.wantSent)
			}
			// Local recipients are spooled for the pipeline, not delivered inline.
			if got := len(env.spool.envelopes()); got != tc.wantBob {
				t.Errorf("recipient envelopes=%d want %d", got, tc.wantBob)
			}
			if got := count(env, "u-bob", "INBOX"); got != 0 {
				t.Errorf("recipient INBOX=%d, compose must not deliver directly", got)
			}
			if got := pending(t, env); got != tc.wantQ {
				t.Errorf("queue=%d want %d", got, tc.wantQ)
			}
			if tc.wantCode == http.StatusInsufficientStorage && strings.Contains(tc.name, "recipient") &&
				!strings.Contains(w.Body.String(), "bob@example.com") {
				t.Errorf("recipient refusal should name the address: %q", w.Body.String())
			}
		})
	}

	t.Run("refused send spends no rate-limit budget", func(t *testing.T) {
		env, uid, email := newQuotaCompose(t, &fakeQuota{headroom: map[string]int64{"u-bob": 0}})
		rl := mail.NewRateLimiter(1)
		env.handler.SetRateLimiter(rl)
		_ = sendCompose(t, env, uid, email, []string{"bob@example.com", "ext@external.example"}, nil)
		// Remove the over-quota recipient: the single permitted send must still pass.
		w := sendCompose(t, env, uid, email, []string{"ext@external.example"}, nil)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("limiter budget was spent by the refused send: status %d", w.Code)
		}
	})

	t.Run("fails open on checker error", func(t *testing.T) {
		env, uid, email := newQuotaCompose(t, &fakeQuota{err: errors.New("pebble hiccup")})
		if w := sendCompose(t, env, uid, email, []string{"bob@example.com"}, nil); w.Code != http.StatusSeeOther {
			t.Fatalf("status=%d, checker error must fail open", w.Code)
		}
	})
}

func TestComposeSaveDraftQuota(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hr       map[string]int64
		wantCode int
		want     int
	}{
		{"over quota refused", map[string]int64{quotaUser: 0}, http.StatusInsufficientStorage, 0},
		{"under quota allowed", map[string]int64{quotaUser: 1_048_583}, http.StatusSeeOther, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, uid, email := newQuotaCompose(t, &fakeQuota{headroom: tc.hr})
			form := url.Values{"to": {"bob@example.com"}, "subject": {"draft"}, "body": {"wip"}}
			req := withUserContext(formReq(http.MethodPost, "/compose/draft", form), uid, email)
			w := httptest.NewRecorder()
			env.handler.SaveDraft(w, req)
			msgs, _ := env.mailboxes.ListMessages(uid, "Drafts")
			if w.Code != tc.wantCode || len(msgs) != tc.want {
				t.Fatalf("status=%d drafts=%d, want status=%d drafts=%d", w.Code, len(msgs), tc.wantCode, tc.want)
			}
		})
	}
}

// recordingQuota captures every addBytes it is asked to weigh.
type recordingQuota struct{ sizes []int64 }

func (r *recordingQuota) RecipientWouldExceedQuota(_ string, addBytes int64) (bool, error) {
	r.sizes = append(r.sizes, addBytes)
	return false, nil
}

// newSizedQuotaCompose wires a real DomainQuotaStore: the sender is in
// example.com and bob in hosted.example, each domain with a 10,000,000 byte
// quota and the given recorded usage.
func newSizedQuotaCompose(t *testing.T, senderUsage, bobUsage int64) (*composeSendAsEnv, *store.DomainQuotaStore) {
	t.Helper()
	env := newComposeSendAsEnv(t)
	env.seedUser(t, quotaUser, quotaEmail)
	env.seedAddress(t, quotaEmail, quotaUser, "example.com", true, true)
	env.seedUser(t, "u-bob", "bob@hosted.example")
	env.seedAddress(t, "bob@hosted.example", "u-bob", "hosted.example", true, true)
	for id, addr := range map[string]string{quotaUser: quotaEmail, "u-bob": "bob@hosted.example"} {
		prof, err := proto.Marshal(&bongov1.UserProfile{Email: addr})
		if err != nil {
			t.Fatal(err)
		}
		if err := env.applier.ApplyCommand(&bongov1.RaftCommand{
			Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + id + "/profile"), Value: prof,
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	dq := store.NewDomainQuotaStore(env.ps, env.applier)
	for domain, usage := range map[string]int64{"example.com": senderUsage, "hosted.example": bobUsage} {
		if err := dq.SetQuota(domain, &bongov1.DomainQuota{MaxStorageBytes: 10_000_000}); err != nil {
			t.Fatal(err)
		}
		if err := dq.SetUsage(domain, usage); err != nil {
			t.Fatal(err)
		}
	}
	env.handler.SetQuotaChecker(dq)
	return env, dq
}

func sendSized(t *testing.T, env *composeSendAsEnv, to []string, bodyBytes int) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"send_id": {sendID()},
		"to":      {strings.Join(to, ", ")},
		"subject": {"sized"},
		"body":    {strings.Repeat("abcdefghi\n", bodyBytes/10)},
	}
	req := withUserContext(formReq(http.MethodPost, "/compose/send", form), quotaUser, quotaEmail)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	return w
}

func TestComposeQuotaWeighsSize(t *testing.T) {
	pending := func(env *composeSendAsEnv) int {
		p, err := env.qs.ListPending(50)
		if err != nil {
			t.Fatal(err)
		}
		return len(p)
	}
	count := func(env *composeSendAsEnv, user, mb string) int {
		msgs, _ := env.mailboxes.ListMessages(user, mb)
		return len(msgs)
	}
	blobCount := func(env *composeSendAsEnv) int {
		l, err := env.blobs.List()
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}

	t.Run("sender headroom", func(t *testing.T) {
		env, _ := newSizedQuotaCompose(t, 9_990_000, 0)
		before := blobCount(env)
		to := []string{"bob@hosted.example", "ext@external.example"}
		w := sendSized(t, env, to, 50_000)
		if w.Code != http.StatusInsufficientStorage {
			t.Fatalf("50 KB send: status=%d want 507: %s", w.Code, w.Body.String())
		}
		if count(env, quotaUser, "Sent") != 0 || count(env, "u-bob", "INBOX") != 0 || pending(env) != 0 {
			t.Errorf("refused send left state: sent=%d bob=%d queue=%d", count(env, quotaUser, "Sent"), count(env, "u-bob", "INBOX"), pending(env))
		}
		if got := blobCount(env); got != before {
			t.Errorf("blobs %d -> %d, refused send must write none", before, got)
		}
		if w := sendSized(t, env, to, 5_000); w.Code != http.StatusSeeOther {
			t.Fatalf("5 KB send: status=%d want 303: %s", w.Code, w.Body.String())
		}
	})

	t.Run("recipient headroom", func(t *testing.T) {
		env, _ := newSizedQuotaCompose(t, 0, 9_990_000)
		w := sendSized(t, env, []string{"bob@hosted.example", "ext@external.example"}, 50_000)
		if w.Code != http.StatusInsufficientStorage {
			t.Fatalf("status=%d want 507: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "bob@hosted.example") {
			t.Errorf("want recipient-full text naming bob, got %q", w.Body.String())
		}
		if count(env, quotaUser, "Sent") != 0 || count(env, "u-bob", "INBOX") != 0 || pending(env) != 0 {
			t.Error("refused send delivered or queued something")
		}
	})

	t.Run("draft", func(t *testing.T) {
		env, _ := newSizedQuotaCompose(t, 9_990_000, 0)
		save := func(n int) *httptest.ResponseRecorder {
			form := url.Values{"to": {"bob@hosted.example"}, "subject": {"d"}, "body": {strings.Repeat("abcdefghi\n", n/10)}}
			w := httptest.NewRecorder()
			env.handler.SaveDraft(w, withUserContext(formReq(http.MethodPost, "/compose/draft", form), quotaUser, quotaEmail))
			return w
		}
		if w := save(50_000); w.Code != http.StatusInsufficientStorage {
			t.Fatalf("50 KB draft: status=%d want 507", w.Code)
		}
		if count(env, quotaUser, "Drafts") != 0 {
			t.Error("refused draft was stored")
		}
		if w := save(5_000); w.Code != http.StatusSeeOther {
			t.Fatalf("small draft: status=%d want 303", w.Code)
		}
	})

	t.Run("size equals stored size, computed once", func(t *testing.T) {
		env, uid, email := newQuotaCompose(t, &fakeQuota{})
		rq := &recordingQuota{}
		env.handler.SetQuotaChecker(rq)
		if w := sendCompose(t, env, uid, email, []string{"bob@example.com"}, nil); w.Code != http.StatusSeeOther {
			t.Fatalf("status=%d: %s", w.Code, w.Body.String())
		}
		msgs, err := env.mailboxes.ListMessages(uid, "Sent")
		if err != nil || len(msgs) != 1 {
			t.Fatalf("Sent: %v %d", err, len(msgs))
		}
		raw, err := env.ps.Get([]byte("/user/" + uid + "/mail/" + msgs[0].MailGuid))
		if err != nil {
			t.Fatal(err)
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		want := meta.GetRawMessage().GetSize()
		// Tie the recorded size to the bytes actually stored, not to the
		// variable both values are copied from.
		stored, err := store.RawMessageBytes(env.blobs, &meta)
		if err != nil {
			t.Fatal(err)
		}
		if want != int64(len(stored)) {
			t.Errorf("RawMessage.Size = %d, stored message is %d bytes", want, len(stored))
		}
		if want == 0 || len(rq.sizes) != 2 {
			t.Fatalf("stored size %d, checker sizes %v; want 2 calls", want, rq.sizes)
		}
		for _, got := range rq.sizes {
			if got != want {
				t.Errorf("addBytes = %d, stored RawMessage.Size = %d", got, want)
			}
		}
	})
}

// quotaRefusalCases drives every refuseQuota caller, and the compose
// recipient refusal, into a refusal. Each returns the recorder and the
// translated text the response must carry for the locale.
var quotaRefusalCases = []struct {
	name string
	run  func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string)
}{
	{"compose sender", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env, uid, email := newQuotaCompose(t, &fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		form := url.Values{"send_id": {sendID()}, "to": {"bob@example.com"}, "subject": {"s"}, "body": {"b"}}
		req := quotaReqFor(withUserContext(formReq(http.MethodPost, "/compose/send", form), uid, email), locale, hx)
		w := httptest.NewRecorder()
		env.handler.Send(w, req)
		return w, i18n.T(locale, "quota.exceeded")
	}},
	{"compose recipient", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env, uid, email := newQuotaCompose(t, &fakeQuota{headroom: map[string]int64{"u-bob": 0}})
		form := url.Values{"send_id": {sendID()}, "to": {"bob@example.com"}, "subject": {"s"}, "body": {"b"}}
		req := quotaReqFor(withUserContext(formReq(http.MethodPost, "/compose/send", form), uid, email), locale, hx)
		w := httptest.NewRecorder()
		env.handler.Send(w, req)
		return w, fmt.Sprintf(i18n.T(locale, "quota.recipientExceeded"), "bob@example.com")
	}},
	{"compose draft", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env, uid, email := newQuotaCompose(t, &fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		form := url.Values{"to": {"bob@example.com"}, "subject": {"d"}, "body": {"wip"}}
		req := quotaReqFor(withUserContext(formReq(http.MethodPost, "/compose/draft", form), uid, email), locale, hx)
		w := httptest.NewRecorder()
		env.handler.SaveDraft(w, req)
		return w, i18n.T(locale, "quota.exceeded")
	}},
	{"calendar create", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		h.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		w := httptest.NewRecorder()
		h.CreateEvent(w, quotaReqFor(formReq(http.MethodPost, "/calendar/event", eventForm("Lunch")), locale, hx))
		return w, i18n.T(locale, "quota.exceeded")
	}},
	{"calendar update", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		h.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		seed, _ := parseEventForm(formReq(http.MethodPut, "/", eventForm("Standup")))
		guid, err := env.events.Create(quotaUser, seed)
		if err != nil {
			t.Fatal(err)
		}
		grown := eventForm("Standup")
		grown.Set("description", strings.Repeat("g", 613))
		w := httptest.NewRecorder()
		h.UpdateEvent(w, withGUID(quotaReqFor(formReq(http.MethodPut, "/calendar/event/"+guid, grown), locale, hx), guid))
		return w, i18n.T(locale, "quota.exceeded")
	}},
	{"contact create", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env := newQuotaEnv(t)
		h := NewContactsHandler(env.contacts, nil)
		h.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		w := httptest.NewRecorder()
		h.CreateContact(w, quotaReqFor(formReq(http.MethodPost, "/contacts", url.Values{"display_name": {"Ada"}}), locale, hx))
		return w, i18n.T(locale, "quota.exceeded")
	}},
	{"contact update", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env := newQuotaEnv(t)
		h := NewContactsHandler(env.contacts, nil)
		h.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		seed := &bongov1.Contact{}
		buildVCardFromForm(seed, formReq(http.MethodPut, "/", url.Values{"display_name": {"Ada"}}))
		guid, err := env.contacts.Create(quotaUser, seed)
		if err != nil {
			t.Fatal(err)
		}
		grown := url.Values{"display_name": {"Ada"}, "notes": {strings.Repeat("n", 700)}}
		w := httptest.NewRecorder()
		h.UpdateContact(w, withGUID(quotaReqFor(formReq(http.MethodPut, "/contacts/"+guid, grown), locale, hx), guid))
		return w, i18n.T(locale, "quota.exceeded")
	}},
	{"mbox import", func(t *testing.T, locale string, hx bool) (*httptest.ResponseRecorder, string) {
		env := newQuotaEnv(t)
		h := NewMailHandler(env.ps, env.blobs, env.mailboxes, discardLogger())
		h.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{quotaUser: 0}})
		req := quotaReqFor(uploadReq(t, "/import", "mbox", []byte(mboxMsgA+mboxMsgB), map[string]string{"new_mailbox": "Imported"}), locale, hx)
		w := httptest.NewRecorder()
		h.ImportMbox(w, req)
		return w, i18n.T(locale, "quota.exceeded")
	}},
}

// quotaReqFor sets the request locale and, for hx, the htmx request header.
func quotaReqFor(req *http.Request, locale string, hx bool) *http.Request {
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	return req.WithContext(middleware.WithLocale(req.Context(), locale))
}

// TestQuotaRefusalPageVersusText: a full-page post is refused with the app
// layout and the translated text; an htmx request keeps the short text.
func TestQuotaRefusalPageVersusText(t *testing.T) {
	for _, tc := range quotaRefusalCases {
		for _, locale := range []string{"en", "de", "fr", "es"} {
			t.Run(tc.name+"/"+locale+"/page", func(t *testing.T) {
				w, want := tc.run(t, locale, false)
				if w.Code != http.StatusInsufficientStorage {
					t.Fatalf("status=%d want 507: %s", w.Code, w.Body.String())
				}
				if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
					t.Errorf("Content-Type=%q want text/html", ct)
				}
				body := w.Body.String()
				// id="main-content" comes from quota.templ; the other
				// markers are emitted only by LayoutWithContent.
				for _, marker := range []string{`id="main-content"`, `id="i18n-data"`, `class="topbar"`, `<!doctype html>`} {
					if !strings.Contains(body, marker) {
						t.Errorf("no app layout marker %s in body: %q", marker, body)
					}
				}
				if !strings.Contains(body, html.EscapeString(want)) {
					t.Errorf("body lacks escaped %q", want)
				}
			})
			t.Run(tc.name+"/"+locale+"/htmx", func(t *testing.T) {
				w, want := tc.run(t, locale, true)
				if w.Code != http.StatusInsufficientStorage {
					t.Fatalf("status=%d want 507", w.Code)
				}
				if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
					t.Errorf("Content-Type=%q want text/plain", ct)
				}
				if got := strings.TrimSpace(w.Body.String()); got != want {
					t.Errorf("body=%q want exactly %q", got, want)
				}
			})
		}
	}
}
