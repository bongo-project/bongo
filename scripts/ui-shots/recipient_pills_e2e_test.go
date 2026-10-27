//go:build uishots

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/static"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// pillsShotDirEnv, when set, makes TestRecipientPillsHeadless write
// screenshots of the prefilled reply page into that directory.
const pillsShotDirEnv = "BONGO_PILLS_SHOT_DIR"

var pillsCSRFSecret = []byte("0123456789abcdef0123456789abcdef")

// composeShell wraps the real ComposePage output with the production asset
// links, so the test drives the shipped template, CSS and bongo.js.
func composeShell(body, theme string) string {
	return `<!doctype html><html lang="en" data-theme="` + theme + `" data-accent="bongo" data-sidebar="dark"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="htmx-config" content='{"includeIndicatorStyles":false}'>
<title>compose</title>
<link rel="stylesheet" href="/static/css/componentlib/colors_and_type.css">
<link rel="stylesheet" href="/static/css/bongo.css">
<link rel="stylesheet" href="/static/css/generated_geometry.css">
</head><body>
<main class="content" id="main-content">` + body + `</main>
<script src="/static/js/htmx.min.js"></script>
<script src="/static/js/richeditor.js"></script>
<script src="/static/js/alpine-csp.min.js" defer></script>
<script src="/static/js/bongo.js"></script>
</body></html>`
}

// composeHandler renders templates.ComposePage from the query: mode, to, cc
// and theme.
func composeHandler() http.Handler {
	return middleware.InjectCSRF(pillsCSRFSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ctx := middleware.WithLocale(r.Context(), "en")
		var buf bytes.Buffer
		err := templates.ComposePage(q.Get("mode"), q.Get("to"), q.Get("cc"), "Subject", "", "INBOX", 7, nil, "", "").Render(ctx, &buf)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		theme := q.Get("theme")
		if theme == "" {
			theme = "light"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(composeShell(buf.String(), theme)))
	}))
}

type pillsEnv struct {
	srv *httptest.Server
	b   *rod.Browser
}

func newPillsEnv(t *testing.T) *pillsEnv {
	t.Helper()
	bin, ok := launcher.LookPath()
	if !ok {
		t.Skip("no Chrome or Chromium found; not downloading one")
	}
	i18n.Init("en")
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static.Files))))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/api/gal", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	mux.Handle("/compose", composeHandler())
	srv := httptest.NewServer(middleware.SecurityHeaders()(mux))
	t.Cleanup(srv.Close)

	l := launcher.New().Bin(bin).Headless(true).Leakless(false).UserDataDir(t.TempDir())
	t.Cleanup(func() { l.Kill(); l.Cleanup() })
	u, err := l.Launch()
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return &pillsEnv{srv: srv, b: b}
}

const pillsErrHook = `(() => {
  window.__errs = [];
  const w = console.warn;
  console.warn = function() { window.__errs.push('warn: ' + Array.from(arguments).map(String).join(' ')); w.apply(console, arguments); };
  const orig = console.error;
  console.error = function() { window.__errs.push(Array.from(arguments).join(' ')); orig.apply(console, arguments); };
  window.addEventListener('error', (e) => window.__errs.push(String(e.message) + ' @' + e.filename + ':' + e.lineno));
})();`

// open loads a compose page and waits for Alpine to start the pills.
func (e *pillsEnv) open(t *testing.T, query string, width, height int) *rod.Page {
	t.Helper()
	p, err := e.b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := (proto.EmulationSetFocusEmulationEnabled{Enabled: true}).Call(p); err != nil {
		t.Fatal(err)
	}
	if err := p.SetViewport(&proto.EmulationSetDeviceMetricsOverride{Width: width, Height: height, DeviceScaleFactor: 1}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{cspHook, pillsErrHook} {
		if _, err := p.EvalOnNewDocument(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Navigate(e.srv.URL + "/compose?" + query); err != nil {
		t.Fatal(err)
	}
	if err := p.WaitLoad(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := p.Eval(`() => !!document.querySelector('.recipient-field')._x_dataStack`)
		if err == nil && v.Value.Bool() {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatal("Alpine never initialised recipientPills")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func assertClean(t *testing.T, p *rod.Page, allow ...string) {
	t.Helper()
	v, err := p.Eval(`() => JSON.stringify({csp: window.__cspViolations, errs: window.__errs})`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		CSP  []json.RawMessage `json:"csp"`
		Errs []string          `json:"errs"`
	}
	if err := json.Unmarshal([]byte(v.Value.Str()), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.CSP) != 0 {
		t.Errorf("CSP violations: %s", v.Value.Str())
	}
next:
	for _, e := range got.Errs {
		for _, a := range allow {
			if strings.Contains(e, a) {
				continue next
			}
		}
		t.Errorf("console error: %s", e)
	}
}

type fieldState struct {
	Value    string   `json:"value"`
	Posted   string   `json:"posted"`
	Labels   []string `json:"labels"`
	Initials []string `json:"initials"`
	Classes  []string `json:"classes"`
	Has      bool     `json:"has"`
	Active   string   `json:"active"`
}

// field reads one recipient field (to, cc or bcc) of the real compose form.
func field(t *testing.T, p *rod.Page, id string) fieldState {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	v, err := p.Eval(`(id) => {
  const input = document.getElementById(id);
  const wrap = input.closest('.recipient-field');
  return JSON.stringify({
    value: input.value,
    posted: new FormData(input.form).get(id),
    labels: Array.from(wrap.querySelectorAll('.recipient-label')).map(e => e.textContent),
    initials: Array.from(wrap.querySelectorAll('.recipient-avatar')).map(e => e.textContent),
    classes: Array.from(wrap.querySelectorAll('.recipient-avatar')).map(e => e.className),
    has: wrap.classList.contains('has-pills'),
    active: document.activeElement.id || document.activeElement.className
  });
}`, id)
	if err != nil {
		t.Fatal(err)
	}
	var st fieldState
	if err := json.Unmarshal([]byte(v.Value.Str()), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// avatarColour is an independent copy of the Go avatarClass formula (name and
// email, uint32 *31 per code point, mod 7).
func avatarColour(name, email string) string {
	var h uint32
	for _, r := range name + email {
		h = h*31 + uint32(r)
	}
	return fmt.Sprintf("avatar-colour-%d", h%7)
}

func hasClass(classes, want string) bool {
	for _, c := range strings.Fields(classes) {
		if c == want {
			return true
		}
	}
	return false
}

// TestRecipientPillsHeadless drives the rendered compose template with the
// shipped bongo.js and CSS in Chrome, behind the production CSP.
func TestRecipientPillsHeadless(t *testing.T) {
	env := newPillsEnv(t)

	t.Run("prefilled", func(t *testing.T) {
		const to = `"Doe, Jane" <j@x.example>`
		const cc = `bob@y.example, Sam Lee <s@z.example>`
		q := "mode=reply&to=" + url.QueryEscape(to) + "&cc=" + url.QueryEscape(cc)
		p := env.open(t, q, 1440, 900)

		// No interaction: the pills must be there from the server value.
		st := field(t, p, "to")
		if !st.Has || strings.Join(st.Labels, "|") != "Doe, Jane" || st.Value != to || st.Posted != to {
			t.Errorf("prefilled to: has=%v labels=%v value=%q posted=%q", st.Has, st.Labels, st.Value, st.Posted)
		}
		if len(st.Classes) != 1 || !hasClass(st.Classes[0], avatarColour("Doe, Jane", "j@x.example")) {
			t.Errorf("to avatar classes = %v, want %s", st.Classes, avatarColour("Doe, Jane", "j@x.example"))
		}
		st = field(t, p, "cc")
		if !st.Has || strings.Join(st.Labels, "|") != "bob@y.example|Sam Lee" || st.Posted != cc {
			t.Errorf("prefilled cc: has=%v labels=%v posted=%q", st.Has, st.Labels, st.Posted)
		}
		if strings.Join(st.Initials, "|") != "BO|SL" {
			t.Errorf("cc initials = %v", st.Initials)
		}
		wantClasses := []string{avatarColour("", "bob@y.example"), avatarColour("Sam Lee", "s@z.example")}
		for i, c := range st.Classes {
			if i < len(wantClasses) && !hasClass(c, wantClasses[i]) {
				t.Errorf("cc avatar %d class %q, want %s", i, c, wantClasses[i])
			}
		}
		if st = field(t, p, "bcc"); st.Has || len(st.Labels) != 0 {
			t.Errorf("empty bcc shows pills: %+v", st)
		}

		if dir := os.Getenv(pillsShotDirEnv); dir != "" {
			shoot := func(name, theme string, w, h int) {
				sp := env.open(t, q+"&theme="+theme, w, h)
				field(t, sp, "to")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				data, err := sp.Screenshot(false, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			shoot("compose-pills-1440-light.png", "light", 1440, 900)
			shoot("compose-pills-1440-dark.png", "dark", 1440, 900)
			shoot("compose-pills-390-light.png", "light", 390, 844)
			shoot("compose-pills-390-dark.png", "dark", 390, 844)
		}
		assertClean(t, p)
	})

	t.Run("editing", func(t *testing.T) {
		p := env.open(t, "mode=new", 1440, 900)
		in, err := p.Element("#to")
		if err != nil {
			t.Fatal(err)
		}
		subject, err := p.Element("#subject")
		if err != nil {
			t.Fatal(err)
		}

		typed := `"Doe, Jane" <j@x.example>, bob@y.example, Sam Lee <s@z>`
		if err := in.Focus(); err != nil {
			t.Fatal(err)
		}
		if err := in.Input(typed); err != nil {
			t.Fatal(err)
		}
		if st := field(t, p, "to"); st.Has {
			t.Errorf("pills shown while the input is focused")
		}
		if err := subject.Focus(); err != nil {
			t.Fatal(err)
		}
		st := field(t, p, "to")
		if !st.Has || strings.Join(st.Labels, "|") != "Doe, Jane|bob@y.example|Sam Lee" {
			t.Fatalf("blurred state: has=%v labels=%v", st.Has, st.Labels)
		}
		if strings.Join(st.Initials, "|") != "DJ|BO|SL" {
			t.Errorf("initials = %v", st.Initials)
		}
		want := []string{
			avatarColour("Doe, Jane", "j@x.example"),
			avatarColour("", "bob@y.example"),
			avatarColour("Sam Lee", "s@z"),
		}
		for i, c := range st.Classes {
			if !hasClass(c, want[i]) {
				t.Errorf("avatar %d class %q, want %s", i, c, want[i])
			}
		}
		if st.Value != typed || st.Posted != typed {
			t.Errorf("with pills shown: value=%q posted=%q, want the typed string", st.Value, st.Posted)
		}

		// Tabbing straight back into the input reveals the raw text again.
		if err := in.Focus(); err != nil {
			t.Fatal(err)
		}
		if st = field(t, p, "to"); st.Has || st.Value != typed || st.Active != "to" {
			t.Errorf("focusing the input left pills shown: has=%v value=%q active=%q", st.Has, st.Value, st.Active)
		}

		// Clicking the pills, pressing Enter on them and pressing Backspace on
		// them each go back to editing AND leave the input focused: without
		// the focus the blur that brings the pills back never fires.
		pills, err := p.Element("#to ~ .recipient-pills")
		if err != nil {
			t.Fatal(err)
		}
		type way struct {
			name string
			act  func()
		}
		for _, w := range []way{
			{"click", func() {
				if err := pills.Click(proto.InputMouseButtonLeft, 1); err != nil {
					t.Fatal(err)
				}
			}},
			{"enter", func() {
				if err := pills.Focus(); err != nil {
					t.Fatal(err)
				}
				if err := p.Keyboard.Type(input.Enter); err != nil {
					t.Fatal(err)
				}
			}},
			{"backspace", func() {
				if err := pills.Focus(); err != nil {
					t.Fatal(err)
				}
				if err := p.Keyboard.Type(input.Backspace); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			if err := subject.Focus(); err != nil {
				t.Fatal(err)
			}
			if st = field(t, p, "to"); !st.Has {
				t.Fatalf("%s: pills not shown before the interaction", w.name)
			}
			w.act()
			st = field(t, p, "to")
			if st.Has || st.Active != "to" || st.Value != typed || st.Posted != typed {
				t.Errorf("%s: has=%v active=%q value=%q posted=%q", w.name, st.Has, st.Active, st.Value, st.Posted)
			}
		}

		// A token that is not an address keeps the plain text visible.
		if err := in.SelectAllText(); err != nil {
			t.Fatal(err)
		}
		bad := "bob@y.example, not an address"
		if err := in.Input(bad); err != nil {
			t.Fatal(err)
		}
		if err := subject.Focus(); err != nil {
			t.Fatal(err)
		}
		if st = field(t, p, "to"); st.Has || len(st.Labels) != 0 || st.Value != bad || st.Posted != bad {
			t.Errorf("unparseable input: %+v", st)
		}

		// Separator-only input renders no pills.
		if err := in.SelectAllText(); err != nil {
			t.Fatal(err)
		}
		if err := in.Input(" , ,"); err != nil {
			t.Fatal(err)
		}
		if err := subject.Focus(); err != nil {
			t.Fatal(err)
		}
		if st = field(t, p, "to"); st.Has {
			t.Errorf("separator-only input rendered pills")
		}
		assertClean(t, p)
	})

	t.Run("parse and initials", func(t *testing.T) {
		p := env.open(t, "mode=new", 1440, 900)
		cases := []struct{ in, want string }{
			{"bob@y.example,", `[{"name":"","email":"bob@y.example","label":"bob@y.example"}]`},
			{"bob@y.example, , ", `[{"name":"","email":"bob@y.example","label":"bob@y.example"}]`},
			{`Jane <a@x.example>, bob@y.example`, `[{"name":"Jane","email":"a@x.example","label":"Jane"},{"name":"","email":"bob@y.example","label":"bob@y.example"}]`},
			{`"A \" , B" <a@x.example>`, `[{"name":"A \" , B","email":"a@x.example","label":"A \" , B"}]`},
			{`"Jane \"JJ\" Doe" <j@x.example>`, `[{"name":"Jane \"JJ\" Doe","email":"j@x.example","label":"Jane \"JJ\" Doe"}]`},
			{`Jane < a@x.example >`, `[{"name":"Jane","email":"a@x.example","label":"Jane"}]`},
			{`Jane <a,b@x.example>`, `null`},
			{`Jane <a@x.example, b@y.example>`, `null`},
			{`no-at-sign`, `null`},
		}
		for _, c := range cases {
			r, err := p.Eval(`(s) => JSON.stringify(Alpine.$data(document.querySelector('.recipient-field')).parse(s))`, c.in)
			if err != nil {
				t.Fatal(err)
			}
			if r.Value.Str() != c.want {
				t.Errorf("parse(%q) = %s, want %s", c.in, r.Value.Str(), c.want)
			}
		}
		// Initials count code points: an astral first letter is not cut in half.
		initials := []struct{ name, email, want string }{
			{"Zoë", "z@x.example", "ZO"},
			{"Ωmega Δelta", "o@x.example", "ΩΔ"},
			{"\U0001D4D0lice", "a@x.example", "\U0001D4D0L"},
			{"", "\U0001D4D0b@x.example", "\U0001D4D0B"},
			{"", "x", "??"},
		}
		for _, c := range initials {
			r, err := p.Eval(`(n, e) => Alpine.$data(document.querySelector('.recipient-field')).initials(n, e)`, c.name, c.email)
			if err != nil {
				t.Fatal(err)
			}
			if r.Value.Str() != c.want {
				t.Errorf("initials(%q, %q) = %q, want %q", c.name, c.email, r.Value.Str(), c.want)
			}
		}
		assertClean(t, p)
	})

	t.Run("dropping files", func(t *testing.T) {
		p := env.open(t, "mode=new", 1440, 900)
		// A drag that carries text does nothing.
		r, err := p.Eval(`() => {
  const card = document.querySelector('.compose-card');
  const dt = new DataTransfer();
  dt.setData('text/plain', 'hello');
  const ev = new DragEvent('dragover', {dataTransfer: dt, bubbles: true, cancelable: true});
  card.dispatchEvent(ev);
  return JSON.stringify({active: card.classList.contains('drop-active'), prevented: ev.defaultPrevented});
}`)
		if err != nil {
			t.Fatal(err)
		}
		if r.Value.Str() != `{"active":false,"prevented":false}` {
			t.Errorf("text drag: %s", r.Value.Str())
		}
		// Nor does dropping text: the browser keeps its default for it.
		r, err = p.Eval(`() => {
  const dt = new DataTransfer();
  dt.setData('text/plain', 'hello');
  const ev = new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true});
  document.querySelector('.compose-card').dispatchEvent(ev);
  return ev.defaultPrevented;
}`)
		if err != nil || r.Value.Bool() {
			t.Errorf("text drop was prevented: %v %v", r, err)
		}

		drop := func(names ...string) string {
			t.Helper()
			r, err := p.Eval(`async (names) => {
  const card = document.querySelector('.compose-card');
  const dt = new DataTransfer();
  names.forEach((n, i) => dt.items.add(new File([new Uint8Array(1024 * (i + 1))], n)));
  const over = new DragEvent('dragover', {dataTransfer: dt, bubbles: true, cancelable: true});
  card.dispatchEvent(over);
  await new Promise(r => setTimeout(r, 50));
  const during = card.classList.contains('drop-active');
  const drop = new DragEvent('drop', {dataTransfer: dt, bubbles: true, cancelable: true});
  card.dispatchEvent(drop);
  return JSON.stringify({during: during, overPrevented: over.defaultPrevented, dropPrevented: drop.defaultPrevented});
}`, names)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			return r.Value.Str()
		}
		if got := drop("a.txt", "b.txt"); got != `{"during":true,"overPrevented":true,"dropPrevented":true}` {
			t.Errorf("first drop: %s", got)
		}
		read := func() string {
			r, err := p.Eval(`() => JSON.stringify({
  active: document.querySelector('.compose-card').classList.contains('drop-active'),
  inputCount: document.getElementById('attachments').files.length,
  posted: new FormData(document.getElementById('attachments').form).getAll('attachments').map(f => f.name),
  rows: Array.from(document.querySelectorAll('.drop-zone .u-file-row span')).map(e => e.textContent),
  summary: (document.querySelector('.drop-zone .u-text-muted') || {textContent: 'MISSING ' + document.querySelector('.drop-zone').innerHTML}).textContent.trim()
})`)
			if err != nil {
				t.Fatal(err)
			}
			return r.Value.Str()
		}
		if got := read(); got != `{"active":false,"inputCount":2,"posted":["a.txt","b.txt"],"rows":["a.txt","b.txt"],"summary":"2 file(s), total: 3.0 KB"}` {
			t.Errorf("after first drop: %s", got)
		}
		// A second drop adds to the files already chosen.
		drop("c.txt")
		if got := read(); got != `{"active":false,"inputCount":3,"posted":["a.txt","b.txt","c.txt"],"rows":["a.txt","b.txt","c.txt"],"summary":"3 file(s), total: 4.0 KB"}` {
			t.Errorf("after second drop: %s", got)
		}
		// Leaving the card clears the target state.
		r, err = p.Eval(`async () => {
  const card = document.querySelector('.compose-card');
  const dt = new DataTransfer();
  dt.items.add(new File(['x'], 'z.txt'));
  card.dispatchEvent(new DragEvent('dragover', {dataTransfer: dt, bubbles: true, cancelable: true}));
  await new Promise(r => setTimeout(r, 50));
  return card.classList.contains('drop-active');
}`)
		if err != nil || !r.Value.Bool() {
			t.Fatalf("drop target not set before dragleave: %v %v", r, err)
		}
		if _, err := p.Eval(`() => document.querySelector('.compose-card').dispatchEvent(new DragEvent('dragleave', {bubbles: true, relatedTarget: document.body}))`); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		if v, _ := p.Eval(`() => document.querySelector('.compose-card').classList.contains('drop-active')`); v.Value.Bool() {
			t.Errorf("drop target state survived dragleave")
		}
		assertClean(t, p)
	})
}
