package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

func phase6Contacts(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 6: Contacts ---")

	if err := nav(page, "/contacts"); err != nil {
		c.check("Contacts", "Navigate to contacts", err)
		return
	}

	c.check("Contacts", "Contacts page loads",
		boolErr(strings.Contains(currentURL(page), "/contacts")))

	c.check("Contacts", "Contact list pane",
		boolErr(hasAny(page, ".contact-list-pane", ".contact-scroll")))

	c.check("Contacts", "New contact button",
		boolErr(hasAny(page, "button.tool-btn", "a[href*='/contacts/new']")))

	c.check("Contacts", "Export button",
		boolErr(has(page, "a[href='/contacts/export']")))

	c.check("Contacts", "Import form/button exists",
		boolErr(hasAny(page, "form[action*='/contacts/import']", "input[type='file']")))

	n := countEls(page, ".contact-card")
	c.check("Contacts", fmt.Sprintf("Contact cards (%d found)", n), boolErr(n > 0))

	// Click first contact for detail pane.
	if n > 0 {
		if err := clickFirst(page, ".contact-card"); err == nil {
			waitStable(page)
			c.check("Contacts", "Contact detail pane appears",
				boolErr(hasAny(page, ".contact-detail-pane", "#contact-detail", ".detail-card")))

			// Detail pane shows name/email.
			c.check("Contacts", "Contact detail has content",
				boolErr(pageContains(page, "@") || pageContains(page, "Name")))
		}
	}

	// --- Create new contact ---
	// Navigate to new contact form (if route exists).
	if err := nav(page, "/contacts"); err != nil {
		return
	}

	// Try clicking new contact button.
	if has(page, "a[href*='/contacts/new']") {
		_ = click(page, "a[href*='/contacts/new']")
		waitStable(page)
	}

	// If we have a form on the page, try creating a contact.
	if has(page, "input[name='display_name'], input[name='name']") {
		nameField := "input[name='display_name']"
		if !has(page, nameField) {
			nameField = "input[name='name']"
		}
		_ = fillInput(page, nameField, "Test Contact")

		if has(page, "input[name='email']") {
			_ = fillInput(page, "input[name='email']", "test-contact@example.com")
		}

		c.check("Contacts", "New contact form accepts input",
			boolErr(strings.Contains(inputValue(page, nameField), "Test")))
	}

	// --- Delete contact (check button exists) ---
	if err := nav(page, "/contacts"); err == nil {
		if countEls(page, ".contact-card") > 0 {
			_ = clickFirst(page, ".contact-card")
			waitStable(page)
			time.Sleep(300 * time.Millisecond)

			c.check("Contacts", "Delete contact button exists",
				boolErr(hasAny(page,
					"button[hx-delete]",
					"form[action*='/delete']",
					"button.danger",
					"button.btn-danger")))
		}
	}
}
