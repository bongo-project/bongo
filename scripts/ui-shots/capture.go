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

// variant is one viewport and accessibility-media combination. onlyRoute keeps
// focused checks such as 200% zoom from multiplying the complete route matrix.
type variant struct {
	name          string
	width         int
	height        int
	mobile        bool
	dark          bool
	scale         float64
	forcedColors  bool
	reducedMotion bool
	onlyRoute     string
}

var variants = []variant{
	{name: "desktop-light", width: 1440, height: 900},
	{name: "desktop-dark", width: 1440, height: 900, dark: true},
	{name: "mobile-light", width: 390, height: 844, mobile: true},
	{name: "mobile-dark", width: 390, height: 844, mobile: true, dark: true},
	{name: "tablet-light", width: 768, height: 1024, mobile: true},
	// At 200% browser zoom a 1440x900 device exposes a 720x450 CSS viewport.
	// Combining that reflow width with page scale exercises both layout and the
	// visual viewport instead of producing a desktop-sized false positive.
	{name: "desktop-zoom-200", width: 720, height: 450, scale: 2, onlyRoute: "inbox"},
	{name: "desktop-forced-colors", width: 1440, height: 900, forcedColors: true, onlyRoute: "inbox"},
	{name: "desktop-reduced-motion", width: 1440, height: 900, reducedMotion: true, onlyRoute: "inbox"},
}

// routeSpec is one page to capture. Path is empty for the message route until
// a message URL has been scraped from the inbox.
type routeSpec struct {
	name       string
	path       string
	mobileOnly bool
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
		{name: "inbox-nav-open", path: "/mail/INBOX", mobileOnly: true},
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
		{name: "calendar-settings", path: "/calendar/settings"},
		{name: "calendar-shares", path: "/calendar/shares"},
		{name: "calendar-event-new", path: "/calendar/event/new"},
		{name: "tasks", path: "/tasks"},
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
	if err := capture(ctx, b, route, path, v, shot, o, &res); err != nil {
		res.Err = err
		res.Screenshot = ""
	}
	return res
}

func capture(parent context.Context, b *rod.Browser, route, path string, v variant, shot string, o captureOpts, res *Result) error {
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
	if v.scale > 0 {
		if err := (proto.EmulationSetPageScaleFactor{PageScaleFactor: v.scale}).Call(p); err != nil {
			return fmt.Errorf("set page scale: %w", err)
		}
	}
	scheme := "light"
	if v.dark {
		scheme = "dark"
	}
	features := []*proto.EmulationMediaFeature{{Name: "prefers-color-scheme", Value: scheme}}
	for _, media := range []struct {
		enabled     bool
		name, value string
	}{
		{v.forcedColors, "forced-colors", "active"},
		{v.reducedMotion, "prefers-reduced-motion", "reduce"},
	} {
		if media.enabled {
			features = append(features, &proto.EmulationMediaFeature{Name: media.name, Value: media.value})
		}
	}
	if err := (proto.EmulationSetEmulatedMedia{
		Features: features,
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
	if route == "inbox-nav-open" {
		hamburger, err := p.Element(".hamburger")
		if err != nil {
			return fmt.Errorf("open mobile navigation: %w", err)
		}
		if err := hamburger.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("open mobile navigation: %w", err)
		}
		utils.Sleep(0.1)
	}

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
	a11y, err := readAccessibilityFindings(p)
	if err != nil {
		return err
	}
	res.Findings = append(res.Findings, a11y...)

	png, err := p.Screenshot(true, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng})
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

// readAccessibilityFindings catches structural failures that are reliable to
// detect without an external audit runtime. Visual and interaction checks stay
// in the screenshot matrix and browser tests respectively.
func readAccessibilityFindings(p *rod.Page) ([]Finding, error) {
	v, err := p.Eval(`() => {
  const visible = (el) => {
    const style = getComputedStyle(el);
    return style.display !== 'none' && style.visibility !== 'hidden' && el.getClientRects().length > 0;
  };
  const text = (el) => (el.textContent || '').replace(/\s+/g, ' ').trim();
  const named = (el) => {
    if ((el.getAttribute('aria-label') || '').trim()) return true;
    const ids = (el.getAttribute('aria-labelledby') || '').trim().split(/\s+/).filter(Boolean);
    if (ids.some((id) => { const ref = document.getElementById(id); return ref && text(ref); })) return true;
    if (el.id && document.querySelector('label[for="' + CSS.escape(el.id) + '"]')) return true;
    if (el.closest('label')) return true;
    if (el.tagName === 'IMG') return el.hasAttribute('alt');
    if (el.tagName === 'INPUT' && ['button', 'submit', 'reset'].includes(el.type)) return !!el.value;
    return !!text(el) || !!(el.getAttribute('title') || '').trim();
  };
  const describe = (el) => {
    const id = el.id ? '#' + el.id : '';
    const cls = typeof el.className === 'string' && el.className.trim()
      ? '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.') : '';
    return el.tagName.toLowerCase() + id + cls;
  };
  const issues = [];
  const ids = new Map();
  document.querySelectorAll('[id]').forEach((el) => ids.set(el.id, (ids.get(el.id) || 0) + 1));
  ids.forEach((count, id) => { if (count > 1) issues.push('duplicate id #' + id + ' (' + count + ')'); });
  document.querySelectorAll('button, a[href], input, select, textarea, img').forEach((el) => {
    if (visible(el) && !named(el)) issues.push('missing accessible name: ' + describe(el));
  });
  if (document.documentElement.scrollWidth > document.documentElement.clientWidth + 1) {
    issues.push('horizontal page overflow: ' + document.documentElement.scrollWidth + 'px > ' + document.documentElement.clientWidth + 'px');
  }
  return JSON.stringify(issues);
}`)
	if err != nil {
		return nil, fmt.Errorf("accessibility audit: %w", err)
	}
	var issues []string
	if err := json.Unmarshal([]byte(v.Value.String()), &issues); err != nil {
		return nil, fmt.Errorf("decode accessibility audit: %w", err)
	}
	findings := make([]Finding, 0, len(issues))
	for _, issue := range issues {
		findings = append(findings, Finding{Kind: KindA11y, Detail: issue})
	}
	return findings, nil
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
