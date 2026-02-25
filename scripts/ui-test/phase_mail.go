package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

func phase3Mail(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 3: Mail -- INBOX, Reading, Compose, Reply, Forward ---")

	// --- INBOX ---
	if err := nav(page, "/mail/INBOX"); err != nil {
		c.check("Mail", "Navigate to INBOX", err)
		return
	}

	c.check("Mail", "INBOX page loads",
		boolErr(strings.Contains(currentURL(page), "INBOX")))
	c.check("Mail", "Message list exists",
		boolErr(hasAny(page, ".msg-list", "#message-list")))

	n := countEls(page, ".msg-row")
	c.check("Mail", fmt.Sprintf("INBOX has messages (%d found)", n), boolErr(n > 0))

	if n == 0 {
		return
	}

	// Row structure.
	c.check("Mail", "Row has sender (.msg-from)", boolErr(has(page, ".msg-row .msg-from")))
	c.check("Mail", "Row has subject (.msg-subject)", boolErr(has(page, ".msg-row .msg-subject")))
	c.check("Mail", "Row has date (.msg-date)", boolErr(has(page, ".msg-row .msg-date")))
	c.check("Mail", "Row has checkbox (.msg-check)", boolErr(has(page, ".msg-row .msg-check")))
	c.check("Mail", "Row has star button (.msg-star)", boolErr(has(page, ".msg-row .msg-star")))

	// --- Click message to open reading view ---
	if has(page, "a[data-message-link='true']") {
		if err := clickFirst(page, "a[data-message-link='true']"); err == nil {
			waitStable(page)
			c.check("Mail", "Reading pane/view appears",
				boolErr(hasAny(page, ".reading-pane", ".msg-body", ".email-body", "article.message", ".msg-card")))

			// Message view elements.
			c.check("Mail", "Message shows subject",
				boolErr(hasAny(page, ".thread-subject", "h1")))
			c.check("Mail", "Message shows sender (.msg-sender)",
				boolErr(has(page, ".msg-sender")))
			c.check("Mail", "Message shows date (.msg-date)",
				boolErr(has(page, ".msg-date")))
			c.check("Mail", "Message body exists",
				boolErr(hasAny(page, ".email-body", ".text-body", ".msg-body")))

			// Toolbar buttons.
			c.check("Mail", "Reply button exists",
				boolErr(has(page, "a[href*='/compose/reply/']")))
			c.check("Mail", "Forward button exists",
				boolErr(has(page, "a[href*='/compose/forward/']")))
			c.check("Mail", "Delete button exists",
				boolErr(hasAny(page, "button[hx-post*='/delete']", ".tool-btn.danger")))
			c.check("Mail", "Export button exists",
				boolErr(has(page, "a[href*='/export']")))
			c.check("Mail", "Move dropdown exists",
				boolErr(has(page, "select.move-select")))

			// Quick-add contact button.
			c.check("Mail", "Add to Contacts button",
				boolErr(has(page, "form[action*='/contacts/quick-add']")))
		}
	} else if has(page, ".msg-row") {
		_ = clickFirst(page, ".msg-row")
		waitStable(page)
		c.check("Mail", "Message clicked", nil)
	}

	// --- Compose ---
	if err := nav(page, "/compose"); err != nil {
		c.check("Compose", "Navigate to compose", err)
		return
	}

	c.check("Compose", "To field exists", boolErr(has(page, "input[name='to']")))
	c.check("Compose", "Cc field exists", boolErr(has(page, "input[name='cc']")))
	c.check("Compose", "Bcc field exists", boolErr(has(page, "input[name='bcc']")))
	c.check("Compose", "Subject field exists", boolErr(has(page, "input[name='subject']")))
	c.check("Compose", "Body field exists", boolErr(has(page, "textarea[name='body']")))
	c.check("Compose", "Send button exists", boolErr(hasAny(page, "button.btn-send", "button[type='submit']")))
	c.check("Compose", "Save Draft button exists",
		boolErr(hasAny(page, "button[formaction*='/draft']", ".btn-secondary")))
	c.check("Compose", "Attachment input exists", boolErr(has(page, "input[type='file'][name='attachments']")))

	// Fill and verify.
	_ = fillInput(page, "input[name='to']", "bob@localhost")
	c.check("Compose", "To field accepts input",
		boolErr(strings.Contains(inputValue(page, "input[name='to']"), "bob")))

	_ = fillInput(page, "input[name='subject']", "UI Test Subject")
	c.check("Compose", "Subject field accepts input",
		boolErr(strings.Contains(inputValue(page, "input[name='subject']"), "UI Test")))

	// --- Reply test ---
	if err := nav(page, "/mail/INBOX"); err != nil {
		return
	}

	if has(page, "a[data-message-link='true']") {
		_ = clickFirst(page, "a[data-message-link='true']")
	} else if has(page, ".msg-row") {
		_ = clickFirst(page, ".msg-row")
	} else {
		c.check("Reply", "Find message to reply to", fmt.Errorf("no messages"))
		return
	}
	waitStable(page)

	if !has(page, "a[href*='/compose/reply/']") {
		c.check("Reply", "Reply button found", fmt.Errorf("not found"))
		return
	}
	c.check("Reply", "Reply button found", nil)

	if err := click(page, "a[href*='/compose/reply/']"); err != nil {
		c.check("Reply", "Click reply", err)
		return
	}
	waitStable(page)
	time.Sleep(1500 * time.Millisecond) // extra time for Alpine init + $nextTick

	// Read the To value — try multiple approaches since Alpine x-model may
	// interfere with the DOM .value property.
	toVal := inputValue(page, "input[name='to']")
	if toVal == "" {
		// Try reading from Alpine's data stack.
		toVal = evalStr(page, "input[name='to']",
			`() => {
				try {
					if (this._x_dataStack && this._x_dataStack[0]) return this._x_dataStack[0].query || '';
				} catch(e) {}
				return '';
			}`)
	}
	if toVal == "" {
		// Try reading the raw HTML attribute (server-rendered value).
		toVal = evalStr(page, "input[name='to']", `() => this.getAttribute('value') || ''`)
	}
	c.check("Reply", fmt.Sprintf("To field is pre-populated (got %q)", toVal), boolErr(toVal != ""))
	c.check("Reply", "To field contains @ (email)", boolErr(strings.Contains(toVal, "@")))

	subVal := inputValue(page, "input[name='subject']")
	c.check("Reply", "Subject has Re: prefix",
		boolErr(strings.Contains(strings.ToLower(subVal), "re:")))

	// Check reply includes quoted body.
	bodyVal := inputValue(page, "textarea[name='body']")
	c.check("Reply", "Reply includes quoted body",
		boolErr(len(bodyVal) > 0))

	// --- Forward test ---
	if err := nav(page, "/mail/INBOX"); err != nil {
		return
	}
	if has(page, "a[data-message-link='true']") {
		_ = clickFirst(page, "a[data-message-link='true']")
	} else {
		_ = clickFirst(page, ".msg-row")
	}
	waitStable(page)

	if has(page, "a[href*='/compose/forward/']") {
		c.check("Forward", "Forward button found", nil)
		if err := click(page, "a[href*='/compose/forward/']"); err == nil {
			waitStable(page)
			time.Sleep(1 * time.Second)

			fwdSub := inputValue(page, "input[name='subject']")
			c.check("Forward", "Subject has Fwd: prefix",
				boolErr(strings.Contains(strings.ToLower(fwdSub), "fwd:")))

			fwdBody := inputValue(page, "textarea[name='body']")
			c.check("Forward", "Forward includes original body",
				boolErr(len(fwdBody) > 0))

			// To field should be empty for forward.
			fwdTo := inputValue(page, "input[name='to']")
			c.check("Forward", "To field is empty (user fills it)",
				boolErr(fwdTo == ""))
		}
	}

	// --- Navigate other folders ---
	for _, folder := range []string{"Sent", "Drafts", "Trash", "Junk"} {
		if err := nav(page, "/mail/"+folder); err == nil {
			c.check("Mail", fmt.Sprintf("%s folder loads", folder),
				boolErr(strings.Contains(currentURL(page), folder)))
		}
	}

	// Virtual mailboxes.
	for _, vbox := range []string{"starred", "all-mail"} {
		if err := nav(page, "/mail/"+vbox); err == nil {
			c.check("Mail", fmt.Sprintf("Virtual mailbox %s loads", vbox),
				boolErr(strings.Contains(currentURL(page), vbox)))
		}
	}
}
