//go:build uishots

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// messageShotDirEnv, when set, makes TestMessageMoreMenuHeadless write
// screenshots into that directory.
const messageShotDirEnv = "BONGO_MESSAGE_SHOT_DIR"

const longTextLine = "landmark-no-duplicate-banner (1 instance) landmark-no-duplicate-banner-and-a-very-long-unbroken-token-" +
	"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"

func messageFixture() templates.MessageDetail {
	return templates.MessageDetail{
		UID: 7, Mailbox: "INBOX", Subject: "Roadmap review",
		From: "Marcus Webb <marcus@example.com>", To: []string{"jane@bongo.dev"},
		Date:     time.Date(2026, 4, 19, 9, 30, 0, 0, time.UTC),
		TextBody: "Body of the opened message.",
		Attachments: []templates.Attachment{
			{Filename: "report.txt", ContentType: "text/plain", Size: 120, Hash: "abc123", URL: "/mail/blob/abc123"},
		},
		Thread: []templates.MessageDetail{{
			UID: 3, Mailbox: "INBOX", Subject: "Re: Roadmap review", From: "jane@bongo.dev",
			To: []string{"marcus@example.com"}, Date: time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC),
			TextBody: "earlier reply",
		}},
	}
}

func messageShell(body, theme string) string {
	return `<!doctype html><html lang="en" data-theme="` + theme + `" data-accent="bongo" data-sidebar="dark"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="htmx-config" content='{"includeIndicatorStyles":false}'>
<title>message</title>
<link rel="stylesheet" href="/static/css/componentlib/colors_and_type.css">
<link rel="stylesheet" href="/static/css/bongo.css">
<link rel="stylesheet" href="/static/css/generated_geometry.css">
</head><body>
<main class="content" id="main-content">` + body + `</main>
<script src="/static/js/htmx.min.js"></script>
<script src="/static/js/alpine-csp.min.js" defer></script>
<script src="/static/js/bongo.js"></script>
</body></html>`
}

const messageErrHook = `(() => {
  window.__errs = [];
  const orig = console.error;
  console.error = function() { window.__errs.push(Array.from(arguments).join(' ')); orig.apply(console, arguments); };
  window.addEventListener('error', (e) => window.__errs.push(String(e.message)));
})();`

// TestMessageMoreMenuHeadless drives the real message view template with the
// shipped CSS and bongo.js behind the production CSP: the More menu opens with
// Enter and with Space, Escape closes it and returns focus to its button, an
// outside click closes it, and the text attachment preview stays inside its
// card at 390 px.
func TestMessageMoreMenuHeadless(t *testing.T) {
	bin, ok := launcher.LookPath()
	if !ok {
		t.Skip("no Chrome or Chromium found; not downloading one")
	}
	i18n.Init("en")
	secret := []byte("0123456789abcdef0123456789abcdef")
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static.Files))))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/mail/blob/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<pre class="attachment-text">` + longTextLine + "\nsecond line\n</pre>"))
	})
	mux.Handle("/message", middleware.InjectCSRF(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.WithLocale(r.Context(), "en")
		var buf bytes.Buffer
		if err := templates.MessagePage(messageFixture(), []templates.MailboxInfo{{Name: "INBOX"}, {Name: "Sent"}}).Render(ctx, &buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		theme := r.URL.Query().Get("theme")
		if theme == "" {
			theme = "light"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(messageShell(buf.String(), theme)))
	})))
	srv := httptest.NewServer(middleware.SecurityHeaders()(mux))
	defer srv.Close()

	l := launcher.New().Bin(bin).Headless(true).Leakless(false).UserDataDir(t.TempDir())
	defer func() { l.Kill(); l.Cleanup() }()
	u, err := l.Launch()
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = b.Close() }()

	open := func(t *testing.T, width, height int, theme string) *rod.Page {
		t.Helper()
		p, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
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
		for _, h := range []string{cspHook, messageErrHook} {
			if _, err := p.EvalOnNewDocument(h); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Navigate(srv.URL + "/message?theme=" + theme); err != nil {
			t.Fatal(err)
		}
		if err := p.WaitLoad(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			v, err := p.Eval(`() => !!document.querySelector('.msg-more')._x_dataStack`)
			if err == nil && v.Value.Bool() {
				return p
			}
			if time.Now().After(deadline) {
				t.Fatal("Alpine never initialised messageMore")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	state := func(t *testing.T, p *rod.Page) (hidden bool, expanded string, focusOnButton bool) {
		t.Helper()
		time.Sleep(120 * time.Millisecond)
		v, err := p.Eval(`() => {
  const wrap = document.querySelector('.msg-more');
  const btn = wrap.querySelector('button');
  const menu = wrap.querySelector('.msg-more-menu');
  return JSON.stringify({
    hidden: menu.hidden || getComputedStyle(menu).display === 'none',
    expanded: btn.getAttribute('aria-expanded'),
    focus: document.activeElement === btn
  });
}`)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Hidden   bool   `json:"hidden"`
			Expanded string `json:"expanded"`
			Focus    bool   `json:"focus"`
		}
		if err := json.Unmarshal([]byte(v.Value.Str()), &s); err != nil {
			t.Fatal(err)
		}
		return s.Hidden, s.Expanded, s.Focus
	}
	assertClean := func(t *testing.T, p *rod.Page) {
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
		if len(got.CSP) != 0 || len(got.Errs) != 0 {
			t.Errorf("CSP violations or console errors: %s", v.Value.Str())
		}
	}

	t.Run("keyboard", func(t *testing.T) {
		p := open(t, 1440, 900, "light")
		btn, err := p.Element(".msg-more > button")
		if err != nil {
			t.Fatal(err)
		}
		if hidden, exp, _ := state(t, p); !hidden || exp != "false" {
			t.Fatalf("initial: hidden=%v aria-expanded=%s, want closed", hidden, exp)
		}

		for _, key := range []struct {
			name string
			k    input.Key
		}{{"Enter", input.Enter}, {"Space", input.Space}} {
			if err := btn.Focus(); err != nil {
				t.Fatal(err)
			}
			if err := p.Keyboard.Type(key.k); err != nil {
				t.Fatal(err)
			}
			if hidden, exp, _ := state(t, p); hidden || exp != "true" {
				t.Errorf("%s: hidden=%v aria-expanded=%s, want open", key.name, hidden, exp)
			}
			if err := p.Keyboard.Type(input.Escape); err != nil {
				t.Fatal(err)
			}
			if hidden, exp, focus := state(t, p); !hidden || exp != "false" || !focus {
				t.Errorf("Escape after %s: hidden=%v aria-expanded=%s focusOnButton=%v, want closed with focus back", key.name, hidden, exp, focus)
			}
		}

		// Escape while focus has moved into the menu still returns focus.
		if err := btn.Focus(); err != nil {
			t.Fatal(err)
		}
		_ = p.Keyboard.Type(input.Enter)
		if _, err := p.Eval(`() => document.querySelector('.msg-more-menu a').focus()`); err != nil {
			t.Fatal(err)
		}
		_ = p.Keyboard.Type(input.Escape)
		if hidden, _, focus := state(t, p); !hidden || !focus {
			t.Errorf("Escape from inside the menu: hidden=%v focusOnButton=%v", hidden, focus)
		}

		// Outside click closes without stealing focus back.
		if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
			t.Fatal(err)
		}
		if hidden, exp, _ := state(t, p); hidden || exp != "true" {
			t.Fatalf("click: hidden=%v aria-expanded=%s, want open", hidden, exp)
		}
		if err := p.Mouse.MoveTo(proto.Point{X: 700, Y: 700}); err != nil {
			t.Fatal(err)
		}
		if err := p.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
			t.Fatal(err)
		}
		if hidden, exp, _ := state(t, p); !hidden || exp != "false" {
			t.Errorf("outside click: hidden=%v aria-expanded=%s, want closed", hidden, exp)
		}
		assertClean(t, p)
	})

	t.Run("mobile text preview stays in the card", func(t *testing.T) {
		p := open(t, 390, 844, "light")
		deadline := time.Now().Add(10 * time.Second)
		for {
			v, err := p.Eval(`() => !!document.querySelector('.text-preview pre')`)
			if err == nil && v.Value.Bool() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("text preview never loaded")
			}
			time.Sleep(50 * time.Millisecond)
		}
		v, err := p.Eval(`() => {
  const card = document.querySelector('.msg-attachments').getBoundingClientRect();
  const preEl = document.querySelector('.text-preview pre');
  const pre = preEl.getBoundingClientRect();
  return JSON.stringify({cardRight: card.right, preRight: pre.right, preScroll: preEl.scrollWidth, preClient: preEl.clientWidth, docW: document.documentElement.scrollWidth, vw: window.innerWidth});
}`)
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			CardRight float64 `json:"cardRight"`
			PreRight  float64 `json:"preRight"`
			PreScroll int     `json:"preScroll"`
			PreClient int     `json:"preClient"`
			DocW      int     `json:"docW"`
			VW        int     `json:"vw"`
		}
		if err := json.Unmarshal([]byte(v.Value.Str()), &r); err != nil {
			t.Fatal(err)
		}
		if r.PreRight > r.CardRight+0.5 {
			t.Errorf("text preview right edge %.1f runs past the card's %.1f", r.PreRight, r.CardRight)
		}
		if r.PreScroll > r.PreClient {
			t.Errorf("text preview content is wider (%d) than its box (%d): long lines must wrap", r.PreScroll, r.PreClient)
		}
		if r.DocW > r.VW {
			t.Errorf("page scrolls horizontally: scrollWidth %d > viewport %d", r.DocW, r.VW)
		}
		if dir := os.Getenv(messageShotDirEnv); dir != "" {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			img, err := p.Screenshot(true, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "message-mobile-light.png"), img, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		assertClean(t, p)
	})
}
