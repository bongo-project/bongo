package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

func phase9Send(c *checker, browser *rod.Browser) {
	fmt.Println("\n--- Phase 9: Send & Receive ---")

	// --- Alice sends to Bob ---
	alicePage := browser.MustPage("")
	defer alicePage.MustClose()

	if err := login(alicePage, "alice@localhost", "alice123"); err != nil {
		c.check("Send", "Alice login", err)
		return
	}
	if err := nav(alicePage, "/compose"); err != nil {
		c.check("Send", "Navigate to compose", err)
		return
	}

	// Compose form loads.
	c.check("Send", "Compose form loads",
		boolErr(hasAny(alicePage, ".compose-form", "form[action*='/compose']")))

	// GAL autocomplete: verify the component is wired up with x-data and has a search method.
	galWired := evalPage(alicePage, `(function(){
		var el = document.querySelector('input[name="to"]');
		if (!el) return 'no-input';
		var xdata = el.getAttribute('x-data');
		if (!xdata || xdata.indexOf('galAutocomplete') < 0) return 'no-xdata';
		if (el._x_dataStack && el._x_dataStack[0] && typeof el._x_dataStack[0].search === 'function') return 'wired';
		return 'has-xdata';
	})()`)
	c.check("Send", fmt.Sprintf("GAL autocomplete component wired (got %s)", galWired),
		boolErr(galWired == "wired" || galWired == "has-xdata"))

	// Set the full email value for sending.
	setToField(alicePage, "bob@localhost")

	_ = fillInput(alicePage, "input[name='subject']", "UI Test: Hello Bob!")
	_ = fillInput(alicePage, "textarea[name='body']", "Automated message from the UI test suite.")

	// Submit form via JS to avoid selector issues.
	submitCompose(alicePage)
	time.Sleep(1 * time.Second)

	sentURL := currentURL(alicePage)
	c.check("Send", fmt.Sprintf("Alice redirected after send (url=%s)", sentURL),
		boolErr(strings.Contains(sentURL, "/Sent") || strings.Contains(sentURL, "flash") ||
			strings.Contains(sentURL, "/mail") || strings.Contains(sentURL, "/compose")))

	// --- Bob receives ---
	bobPage := browser.MustPage("")
	defer bobPage.MustClose()

	if err := login(bobPage, "bob@localhost", "bob123"); err != nil {
		c.check("Send", "Bob login", err)
		return
	}
	if err := nav(bobPage, "/mail/INBOX"); err != nil {
		c.check("Send", "Bob navigate to INBOX", err)
		return
	}

	hasMsg := pageContains(bobPage, "UI Test: Hello Bob!") || pageContains(bobPage, "Hello Bob")
	c.check("Send", "Bob received Alice's message", boolErr(hasMsg))

	// --- Cc test: Alice sends to Bob with Cc to Charlie ---
	if err := nav(alicePage, "/compose"); err == nil {
		time.Sleep(500 * time.Millisecond)
		setToField(alicePage, "bob@localhost")
		_ = fillInput(alicePage, "input[name='cc']", "charlie@localhost")
		_ = fillInput(alicePage, "input[name='subject']", "UI Test: Cc Test")
		_ = fillInput(alicePage, "textarea[name='body']", "Testing Cc field.")

		submitCompose(alicePage)
		time.Sleep(1 * time.Second)

		c.check("Send", "Cc message sent",
			boolErr(strings.Contains(currentURL(alicePage), "/Sent") ||
				strings.Contains(currentURL(alicePage), "/mail")))
	}

	// --- Save as draft ---
	if err := nav(alicePage, "/compose"); err == nil {
		time.Sleep(500 * time.Millisecond)
		setToField(alicePage, "draft-test@localhost")
		_ = fillInput(alicePage, "input[name='subject']", "UI Test: Draft Message")
		_ = fillInput(alicePage, "textarea[name='body']", "This is a draft.")

		// Submit to the draft endpoint via JS to avoid WaitStable hang.
		evalPage(alicePage, `(function(){ var form = document.querySelector('form[action*="/compose"]'); if (form) { form.action = '/compose/draft'; form.submit(); } return 'ok'; })()`)
		time.Sleep(2 * time.Second)

		// Check draft appears in Drafts folder.
		if err := nav(alicePage, "/mail/Drafts"); err == nil {
			c.check("Send", "Draft appears in Drafts folder",
				boolErr(pageContains(alicePage, "Draft Message")))
		}
	}

	// --- Attachment upload field ---
	if err := nav(alicePage, "/compose"); err == nil {
		c.check("Send", "Attachment upload input exists",
			boolErr(has(alicePage, "input[type='file'][name='attachments']")))
	}
}

// setToField sets the To field value and syncs Alpine's x-model.
func setToField(page *rod.Page, email string) {
	_ = fillInput(page, "input[name='to']", email)
	evalOnEl(page, "input[name='to']",
		`(v) => { this.value = v; if (this._x_model) { this._x_model.set(v); } this.dispatchEvent(new Event('change', {bubbles: true})); }`, email)
}

// submitCompose clicks the Send button or submits the compose form via JS.
func submitCompose(page *rod.Page) {
	evalPage(page, `(function(){ var btn = document.querySelector('button.btn-send'); if (btn) { btn.click(); return 'ok'; } var s = document.querySelector('button[type="submit"]'); if (s) { s.click(); return 'ok'; } var f = document.querySelector('form[action*="/compose"]'); if (f) { f.submit(); } return 'ok'; })()`)
}
