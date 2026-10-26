package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

type checker struct {
	passed int
	failed int
	lines  []reportLine
}

type reportLine struct {
	section string
	detail  string
	ok      bool
}

func (c *checker) check(section, name string, err error) {
	ok := err == nil
	if ok {
		c.passed++
		fmt.Printf("  PASS  %s: %s\n", section, name)
	} else {
		c.failed++
		fmt.Printf("  FAIL  %s: %s -- %v\n", section, name, err)
	}
	c.lines = append(c.lines, reportLine{section: section, detail: name, ok: ok})
}

func boolErr(ok bool) error {
	if ok {
		return nil
	}
	return fmt.Errorf("assertion failed")
}

func (c *checker) report() {
	fmt.Println("\n========== UI Test Report ==========")
	for _, l := range c.lines {
		mark := "PASS"
		if !l.ok {
			mark = "FAIL"
		}
		fmt.Printf("  %s  [%s] %s\n", mark, l.section, l.detail)
	}
	fmt.Printf("\nTotal: %d passed, %d failed out of %d checks\n",
		c.passed, c.failed, c.passed+c.failed)
	if c.failed > 0 {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: ALL PASSED")
	}
}

// ---------------------------------------------------------------------------
// Browser helpers
// ---------------------------------------------------------------------------

func baseURL() string {
	return fmt.Sprintf("https://%s:%d", *host, *webPort)
}

// has checks whether a selector exists on the page within the timeout.
func has(page *rod.Page, selector string) bool {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	_, err := p.Element(selector)
	return err == nil
}

// hasAny checks whether any of the given selectors exist.
func hasAny(page *rod.Page, selectors ...string) bool {
	for _, s := range selectors {
		if has(page, s) {
			return true
		}
	}
	return false
}

// countEls returns how many elements match the selector.
func countEls(page *rod.Page, selector string) int {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	elems, err := p.Elements(selector)
	if err != nil {
		return 0
	}
	return len(elems)
}

// inputValue reads the value property of an input element.
func inputValue(page *rod.Page, selector string) string {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return ""
	}
	val, err := e.Eval(`() => this.value`)
	if err != nil {
		return ""
	}
	return val.Value.String()
}

// evalOnEl evaluates JS on an element with an argument.
func evalOnEl(page *rod.Page, selector, js string, args ...interface{}) {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return
	}
	_, _ = e.Eval(js, args...)
}

// evalStr evaluates JS on an element and returns the string result.
func evalStr(page *rod.Page, selector, js string) string {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return ""
	}
	val, err := e.Eval(js)
	if err != nil {
		return ""
	}
	return val.Value.String()
}

// fillInput clears and types into an input field.
func fillInput(page *rod.Page, selector, value string) error {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return fmt.Errorf("element %q not found: %w", selector, err)
	}
	_, err = e.Eval(`(v) => { this.focus(); this.value = v; this.dispatchEvent(new Event('input', {bubbles: true})); }`, value)
	if err != nil {
		return fmt.Errorf("filling %q: %w", selector, err)
	}
	return nil
}

// click clicks an element matching the selector.
func click(page *rod.Page, selector string) error {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return fmt.Errorf("element %q not found: %w", selector, err)
	}
	return e.Click(proto.InputMouseButtonLeft, 1)
}

// clickFirst clicks the first element matching the selector (alias for click).
func clickFirst(page *rod.Page, selector string) error {
	return click(page, selector)
}

// pageText returns the full text content of the page body.
func pageText(page *rod.Page) string {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element("body")
	if err != nil {
		return ""
	}
	text, err := e.Text()
	if err != nil {
		return ""
	}
	return text
}

// pageContains checks if the page body contains the given text.
func pageContains(page *rod.Page, text string) bool {
	return strings.Contains(pageText(page), text)
}

// currentURL returns the current page URL.
func currentURL(page *rod.Page) string {
	info, err := page.Info()
	if err != nil {
		return ""
	}
	return info.URL
}

// nav navigates to a URL and waits for the page to stabilize.
func nav(page *rod.Page, path string) error {
	if err := page.Navigate(baseURL() + path); err != nil {
		return err
	}
	// Use a timeout to prevent hanging if the page never stabilizes
	// (e.g. SSE connections, polling).
	p := page.Timeout(10 * time.Second)
	defer p.CancelTimeout()
	_ = p.WaitStable(300 * time.Millisecond)
	return nil
}

// login navigates to the login page and authenticates.
func login(page *rod.Page, email, password string) error {
	if err := nav(page, "/login"); err != nil {
		return fmt.Errorf("navigate to login: %w", err)
	}
	if err := fillInput(page, "input[name='email']", email); err != nil {
		return fmt.Errorf("fill email: %w", err)
	}
	if err := fillInput(page, "input[name='password']", password); err != nil {
		return fmt.Errorf("fill password: %w", err)
	}
	if err := click(page, "button[type='submit']"); err != nil {
		return fmt.Errorf("click submit: %w", err)
	}
	p := page.Timeout(10 * time.Second)
	defer p.CancelTimeout()
	_ = p.WaitStable(300 * time.Millisecond)
	return nil
}

// waitStable waits for the page to stabilize and gives Alpine.js time to init.
// Uses a timeout to prevent hanging on pages with SSE/polling.
func waitStable(page *rod.Page) {
	p := page.Timeout(10 * time.Second)
	defer p.CancelTimeout()
	_ = p.WaitStable(300 * time.Millisecond)
	time.Sleep(500 * time.Millisecond)
}

// selectOption selects an option in a <select> element by value.
func selectOption(page *rod.Page, selector, value string) error {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return fmt.Errorf("element %q not found: %w", selector, err)
	}
	_, err = e.Eval(`(v) => { this.value = v; this.dispatchEvent(new Event('change', {bubbles: true})); }`, value)
	if err != nil {
		return fmt.Errorf("selecting %q in %q: %w", value, selector, err)
	}
	return nil
}

// isChecked returns whether a checkbox is checked.
func isChecked(page *rod.Page, selector string) bool {
	return evalStr(page, selector, `() => String(this.checked)`) == "true"
}

// isVisible checks if an element exists and has visible dimensions.
func isVisible(page *rod.Page, selector string) bool {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return false
	}
	box, err := e.Shape()
	if err != nil || len(box.Quads) == 0 {
		return false
	}
	return true
}

// pressKey dispatches a keyboard keydown event via JS (for shortcut testing).
func pressKey(page *rod.Page, key string) {
	evalPage(page, fmt.Sprintf(
		`document.dispatchEvent(new KeyboardEvent('keydown', {key: '%s', bubbles: true}))`, key))
}

// evalPage runs JS on the page and returns the string result.
// The js parameter must be a valid JS expression (not a statement).
// For multi-statement code, wrap in an IIFE: (function(){ ...; return val; })()
// Rod's Eval wraps code as `function(){ return <js> }.call(this)`, so plain
// IIFEs cause ".apply is not a function". To avoid this, we use MustEval-style
// direct evaluation via the CDP Runtime.evaluate.
func evalPage(page *rod.Page, js string) string {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	// Use Evaluate directly to avoid Rod's function-wrapping behavior.
	res, err := proto.RuntimeEvaluate{
		Expression: js,
	}.Call(p)
	if err != nil || res.ExceptionDetails != nil {
		return ""
	}
	if res.Result.Value.Nil() {
		return ""
	}
	return res.Result.Value.String()
}

// rightClick dispatches a contextmenu event on an element.
func rightClick(page *rod.Page, selector string) error {
	p := page.Timeout(elTimeout)
	defer p.CancelTimeout()
	e, err := p.Element(selector)
	if err != nil {
		return fmt.Errorf("element %q not found: %w", selector, err)
	}
	_, err = e.Eval(`() => this.dispatchEvent(new MouseEvent('contextmenu', {bubbles: true, clientX: 100, clientY: 100}))`)
	return err
}

// openAccountMenu opens the top bar account menu if it is closed.
func openAccountMenu(page *rod.Page) error {
	if isVisible(page, ".account-dropdown:not([hidden])") {
		return nil
	}
	if err := click(page, ".avatar-chip"); err != nil {
		return err
	}
	waitStable(page)
	return nil
}
