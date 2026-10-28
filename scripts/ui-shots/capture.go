package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/rod/lib/utils"
)

// variant is one viewport and colour-scheme combination.
type variant struct {
	name   string
	width  int
	height int
	mobile bool
	dark   bool
}

var variants = []variant{
	{name: "desktop-light", width: 1440, height: 900},
	{name: "desktop-dark", width: 1440, height: 900, dark: true},
	{name: "mobile-light", width: 390, height: 844, mobile: true},
}

// routeSpec is one page to capture. Path is empty for the message route until
// a message URL has been scraped from the inbox.
type routeSpec struct {
	name string
	path string
	// anon routes are captured before login.
	anon bool
}

// routeSpecs lists every route, with the calendar week and day paths computed
// from now.
func routeSpecs(now time.Time) []routeSpec {
	year, week := now.ISOWeek()
	return []routeSpec{
		{name: "login", path: "/login", anon: true},
		{name: "inbox", path: "/mail/INBOX"},
		{name: "message"},
		{name: "compose", path: "/compose"},
		{name: "calendar-month", path: "/calendar"},
		{name: "calendar-week", path: fmt.Sprintf("/calendar/week/%d/%d", year, week)},
		{name: "calendar-day", path: fmt.Sprintf("/calendar/day/%d/%d/%d", now.Year(), int(now.Month()), now.Day())},
		{name: "calendar-agenda", path: "/calendar/agenda"},
		{name: "contacts", path: "/contacts"},
		{name: "junk", path: "/mail/Junk"},
		{name: "rules", path: "/settings/rules"},
		{name: "settings", path: "/settings"},
		{name: "security", path: "/settings/security"},
		{name: "tags", path: "/settings/tags"},
		{name: "admin-dashboard", path: "/admin"},
		{name: "admin-users", path: "/admin/users"},
		{name: "admin-domains", path: "/admin/domains"},
	}
}

func routeNames() []string {
	var names []string
	for _, r := range routeSpecs(time.Now()) {
		names = append(names, r.name)
	}
	return names
}

// cspHook records securitypolicyviolation events. It is injected through the
// DevTools protocol, so the page's own script-src policy does not block it.
const cspHook = `(() => {
  window.__cspViolations = [];
  document.addEventListener('securitypolicyviolation', (e) => {
    window.__cspViolations.push({
      directive: e.violatedDirective,
      blocked: e.blockedURI,
      source: e.sourceFile,
      line: e.lineNumber
    });
  });
})();`

// streamPatterns are long-lived connections that never go idle.
var streamPatterns = []string{`/events`, `/admin/logs/stream`}

type captureOpts struct {
	origin  string
	timeout time.Duration
	// settle is a short pause for Alpine and HTMX after the network is idle.
	settle time.Duration
}

// collector gathers findings from CDP events, safe for concurrent use.
type collector struct {
	mu       sync.Mutex
	origin   string
	urls     map[proto.NetworkRequestID]string
	findings []Finding
	security []string
}

func (c *collector) add(kind, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.findings = append(c.findings, Finding{Kind: kind, Detail: detail})
}

func remoteText(args []*proto.RuntimeRemoteObject) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a.Value.Nil() && a.Description != "":
			parts = append(parts, a.Description)
		case !a.Value.Nil():
			parts = append(parts, a.Value.String())
		default:
			parts = append(parts, string(a.Type))
		}
	}
	return strings.Join(parts, " ")
}

// listen subscribes to console, exception, log and network events and returns
// a function that blocks until ctx ends.
func (c *collector) listen(p *rod.Page) func() {
	return p.EachEvent(
		func(e *proto.RuntimeConsoleAPICalled) {
			if e.Type == proto.RuntimeConsoleAPICalledTypeError {
				c.add(KindConsole, "console.error: "+remoteText(e.Args))
			}
		},
		func(e *proto.RuntimeExceptionThrown) {
			d := e.ExceptionDetails
			text := d.Text
			if d.Exception != nil && d.Exception.Description != "" {
				text = d.Exception.Description
			}
			c.add(KindConsole, "exception: "+text)
		},
		func(e *proto.LogEntryAdded) {
			if e.Entry.Level != proto.LogLogEntryLevelError {
				return
			}
			switch e.Entry.Source {
			case proto.LogLogEntrySourceNetwork:
				// Reported precisely by the Network domain below.
			case proto.LogLogEntrySourceSecurity:
				c.mu.Lock()
				c.security = append(c.security, e.Entry.Text)
				c.mu.Unlock()
			default:
				c.add(KindConsole, "log: "+e.Entry.Text)
			}
		},
		func(e *proto.NetworkRequestWillBeSent) {
			c.mu.Lock()
			c.urls[e.RequestID] = e.Request.URL
			c.mu.Unlock()
		},
		func(e *proto.NetworkResponseReceived) {
			if isAssetFailure(c.origin, e.Response.URL, e.Response.Status) {
				c.add(KindAsset, fmt.Sprintf("%d %s", e.Response.Status, e.Response.URL))
			}
		},
		func(e *proto.NetworkLoadingFailed) {
			c.mu.Lock()
			u := c.urls[e.RequestID]
			c.mu.Unlock()
			if isLoadFailure(c.origin, u, e.Canceled, string(e.BlockedReason)) {
				reason := e.ErrorText
				if e.BlockedReason != "" {
					reason += " (blocked: " + string(e.BlockedReason) + ")"
				}
				c.add(KindAsset, "load failed: "+reason+" "+u)
			}
		},
	)
}

// capturePage opens path in a new page of b (ctx cancels it; o.timeout bounds it), waits for it to settle, records
// findings and writes a PNG to shot. It never panics; any failure lands in
// Result.Err.
func capturePage(ctx context.Context, b *rod.Browser, route, path string, v variant, shot string, o captureOpts) (res Result) {
	res = Result{Route: route, Variant: v.name, Screenshot: shot}
	defer func() {
		if r := recover(); r != nil {
			res.Err = fmt.Errorf("panic: %v", r)
		}
	}()
	if err := capture(ctx, b, path, v, shot, o, &res); err != nil {
		res.Err = err
		res.Screenshot = ""
	}
	return res
}

func capture(parent context.Context, b *rod.Browser, path string, v variant, shot string, o captureOpts, res *Result) error {
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fmt.Errorf("open page: %w", err)
	}
	defer func() { _ = page.Close() }()

	ctx, cancel := context.WithTimeout(parent, o.timeout)
	defer cancel()
	p := page.Context(ctx)

	if err := p.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: v.width, Height: v.height, DeviceScaleFactor: 1, Mobile: v.mobile,
	}); err != nil {
		return fmt.Errorf("set viewport: %w", err)
	}
	if v.mobile {
		if err := (proto.EmulationSetTouchEmulationEnabled{Enabled: true}).Call(p); err != nil {
			return fmt.Errorf("enable touch: %w", err)
		}
	}
	scheme := "light"
	if v.dark {
		scheme = "dark"
	}
	if err := (proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
	}).Call(p); err != nil {
		return fmt.Errorf("emulate colour scheme: %w", err)
	}
	for _, enable := range []interface{ Call(c proto.Client) error }{
		proto.RuntimeEnable{}, proto.LogEnable{}, proto.NetworkEnable{},
	} {
		if err := enable.Call(p); err != nil {
			return fmt.Errorf("enable domain: %w", err)
		}
	}
	if _, err := p.EvalOnNewDocument(cspHook); err != nil {
		return fmt.Errorf("install csp hook: %w", err)
	}

	col := &collector{origin: o.origin, urls: map[proto.NetworkRequestID]string{}}
	go col.listen(p)()

	idle := p.WaitRequestIdle(500*time.Millisecond, nil, streamPatterns, []proto.NetworkResourceType{proto.NetworkResourceTypeEventSource})
	if err := p.Navigate(o.origin + path); err != nil {
		return fmt.Errorf("navigate %s: %w", path, err)
	}
	if err := p.WaitLoad(); err != nil {
		return fmt.Errorf("wait load %s: %w", path, err)
	}
	if _, err := p.Eval(`() => document.fonts.ready.then(() => true)`); err != nil {
		return fmt.Errorf("fonts ready: %w", err)
	}
	idle()
	if ctx.Err() != nil {
		return fmt.Errorf("timed out after %s waiting for %s", o.timeout, path)
	}
	utils.Sleep(o.settle.Seconds())

	if v.dark {
		st, err := readThemeState(p)
		if err != nil {
			return err
		}
		if !darkApplied(st) {
			return fmt.Errorf("dark variant rendered with data-theme=%q (prefers dark %v, background %s), not dark; screenshot withheld", st.Theme, st.PrefersDark, st.Background)
		}
	}

	csp, err := readCSP(p)
	if err != nil {
		return err
	}

	png, err := p.Screenshot(false, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng})
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	if err := writeFile(shot, png); err != nil {
		return err
	}

	// Let trailing events land before reading the collector.
	utils.Sleep(0.1)
	col.mu.Lock()
	res.Findings = append(res.Findings, col.findings...)
	security := col.security
	col.mu.Unlock()
	res.Findings = append(res.Findings, csp...)
	if len(csp) == 0 {
		for _, s := range security {
			res.Findings = append(res.Findings, Finding{Kind: KindCSP, Detail: s})
		}
	}
	return nil
}

func readCSP(p *rod.Page) ([]Finding, error) {
	v, err := p.Eval(`() => JSON.stringify(window.__cspViolations || [])`)
	if err != nil {
		return nil, fmt.Errorf("read csp violations: %w", err)
	}
	var raw []struct {
		Directive string `json:"directive"`
		Blocked   string `json:"blocked"`
		Source    string `json:"source"`
		Line      int    `json:"line"`
	}
	if err := json.Unmarshal([]byte(v.Value.String()), &raw); err != nil {
		return nil, fmt.Errorf("decode csp violations: %w", err)
	}
	var out []Finding
	for _, r := range raw {
		out = append(out, Finding{Kind: KindCSP, Detail: fmt.Sprintf("%s blocked %q at %s:%d", r.Directive, r.Blocked, r.Source, r.Line)})
	}
	return out, nil
}

// login signs in through the real form so CSRF and the cookie path are used.
func login(ctx context.Context, b *rod.Browser, origin, user, pass string, timeout time.Duration) error {
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return err
	}
	defer func() { _ = page.Close() }()
	p := page.Context(ctx).Timeout(timeout)
	if err := p.Navigate(origin + "/login"); err != nil {
		return err
	}
	if err := p.WaitLoad(); err != nil {
		return err
	}
	email, err := p.Element("#email")
	if err != nil {
		return fmt.Errorf("login form: %w", err)
	}
	if err := email.Input(user); err != nil {
		return err
	}
	pw, err := p.Element("#password")
	if err != nil {
		return err
	}
	if err := pw.Input(pass); err != nil {
		return err
	}
	wait := p.WaitNavigation(proto.PageLifecycleEventNameLoad)
	btn, err := p.Element(`form[action="/login"] button[type=submit]`)
	if err != nil {
		return err
	}
	if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	wait()
	info, err := p.Info()
	if err != nil {
		return err
	}
	if strings.Contains(info.URL, "/login") {
		return errors.New("login rejected: still on /login after submit")
	}
	return nil
}

// themeState is what the page reports about its theme once loaded.
type themeState struct {
	Theme       string `json:"theme"`
	PrefersDark bool   `json:"prefersDark"`
	Background  string `json:"background"`
}

func readThemeState(p *rod.Page) (themeState, error) {
	v, err := p.Eval(`() => JSON.stringify({
		theme: document.documentElement.dataset.theme || '',
		prefersDark: window.matchMedia('(prefers-color-scheme: dark)').matches,
		background: getComputedStyle(document.body).backgroundColor
	})`)
	if err != nil {
		return themeState{}, fmt.Errorf("read theme: %w", err)
	}
	var st themeState
	if err := json.Unmarshal([]byte(v.Value.String()), &st); err != nil {
		return themeState{}, fmt.Errorf("decode theme state: %w", err)
	}
	return st, nil
}

var rgbRe = regexp.MustCompile(`^rgba?\(\s*(\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\s*\)$`)

// darkApplied reports whether a dark screenshot is legitimate. An explicit
// data-theme="dark" is trusted. data-theme="auto" (public pages, which carry no
// script) counts only when the browser prefers dark AND the painted background
// is actually dark, so a page that ignores the preference is still withheld.
func darkApplied(st themeState) bool {
	switch st.Theme {
	case "dark":
		return true
	case "auto":
		if !st.PrefersDark {
			return false
		}
		m := rgbRe.FindStringSubmatch(st.Background)
		if m == nil {
			return false
		}
		if m[4] != "" {
			if a, err := strconv.ParseFloat(m[4], 64); err != nil || a < 1 {
				return false
			}
		}
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		lum := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
		return lum < 0.25
	}
	return false
}
