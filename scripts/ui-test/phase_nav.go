package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func phase2Navigation(c *checker, browser *rod.Browser) *rod.Page {
	fmt.Println("\n--- Phase 2: Navigation ---")

	page := browser.MustPage("")
	if err := login(page, "alice@localhost", "alice123"); err != nil {
		c.check("Navigation", "Alice login", err)
		return page
	}

	c.check("Navigation", "Sidebar exists", boolErr(has(page, "aside.sidebar, .sidebar")))
	c.check("Navigation", "Compose button",
		boolErr(hasAny(page, "a.compose-btn", "a[href='/compose']", "a[hx-get='/compose']")))

	for _, folder := range []string{"INBOX", "Sent", "Drafts", "Trash", "Junk"} {
		found := hasAny(page,
			fmt.Sprintf("a[href='/mail/%s']", folder),
			fmt.Sprintf("a[data-folder='%s']", folder),
			fmt.Sprintf("a[hx-get='/mail/%s']", folder))
		c.check("Navigation", fmt.Sprintf("Sidebar has %s", folder), boolErr(found))
	}

	c.check("Navigation", "Calendar link", boolErr(has(page, "a[href*='/calendar']")))
	c.check("Navigation", "Contacts link", boolErr(has(page, "a[href*='/contacts']")))
	c.check("Navigation", "Search box", boolErr(hasAny(page, "input[name='q']", "input[type='search']")))
	c.check("Navigation", "Logout form", boolErr(has(page, "form[action='/logout']")))

	// Admin link should NOT be visible for alice (non-admin).
	c.check("Navigation", "No admin link for non-admin",
		boolErr(!has(page, "a[href='/admin']")))

	// Click INBOX link navigates correctly.
	if has(page, "a[data-folder='INBOX']") {
		_ = click(page, "a[data-folder='INBOX']")
		waitStable(page)
		c.check("Navigation", "Click INBOX navigates",
			boolErr(hasAny(page, "#message-list", ".msg-list")))
	}

	// Click Sent link.
	if has(page, "a[data-folder='Sent']") {
		_ = click(page, "a[data-folder='Sent']")
		waitStable(page)
		c.check("Navigation", "Click Sent navigates",
			boolErr(strings.Contains(currentURL(page), "Sent") || pageContains(page, "Sent")))
	}

	// Navigate back to INBOX for subsequent phases.
	_ = nav(page, "/mail/INBOX")
	waitStable(page)

	return page
}
