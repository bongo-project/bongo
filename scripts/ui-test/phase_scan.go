package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func phase12Scan(c *checker, browser *rod.Browser) {
	fmt.Println("\n--- Phase 12: Mailbox Spam Scan ---")

	page := browser.MustPage("")
	defer page.MustClose()

	// Login as admin (admin has seed data in INBOX).
	if err := login(page, *adminUser, *adminPass); err != nil {
		c.check("Scan", "Admin login", err)
		return
	}

	// --- Navigate to INBOX ---
	if err := nav(page, "/mail/INBOX"); err != nil {
		c.check("Scan", "Navigate to INBOX", err)
		return
	}
	waitStable(page)

	c.check("Scan", "INBOX page loads",
		boolErr(strings.Contains(currentURL(page), "/mail/INBOX")))

	// --- Scan button exists in toolbar ---
	c.check("Scan", "Toolbar exists",
		boolErr(has(page, `div.toolbar[role="toolbar"]`)))

	// The scan button is rendered by Alpine x-text, so check for the
	// container with spamScan Alpine data binding.
	c.check("Scan", "Spam scan Alpine component",
		boolErr(has(page, `[x-data*="spamScan"]`)))

	// The button inside the spamScan component.
	c.check("Scan", "Scan button exists",
		boolErr(has(page, `[x-data*="spamScan"] button.tool-btn`)))

	c.check("Scan", "Scan button has title",
		boolErr(has(page, `button[title="Scan mailbox for spam"]`)))

	// Button text should show "Scan for spam" when not scanning
	// (Alpine.js sets x-text dynamically).
	btnText := evalStr(page, `button[title="Scan mailbox for spam"]`, `() => this.textContent.trim()`)
	c.check("Scan", "Scan button text",
		boolErr(btnText == "Scan for spam"))

	// Progress spans exist (hidden via x-show/x-cloak when not scanning).
	c.check("Scan", "Progress indicator element",
		boolErr(has(page, `[x-data*="spamScan"] [x-show="scanning"]`)))

	// Result element (shown after scan completes).
	c.check("Scan", "Result display element",
		boolErr(has(page, `[x-data*="spamScan"] [x-show*="result"]`)))

	// --- Verify scan button on another mailbox ---
	if err := nav(page, "/mail/Sent"); err == nil {
		waitStable(page)
		c.check("Scan", "Scan button on Sent mailbox",
			boolErr(has(page, `[x-data*="spamScan"] button.tool-btn`)))
	}

	// --- Verify Junk mailbox shows spam badge on messages ---
	if err := nav(page, "/mail/Junk"); err == nil {
		waitStable(page)
		c.check("Scan", "Junk mailbox loads",
			boolErr(strings.Contains(currentURL(page), "/mail/Junk")))
		// Spam badge is only rendered when SpamScore > 0, so check element
		// existence rather than requiring messages.
		// If there are messages with spam scores, badges should exist.
		if countEls(page, ".msg-row") > 0 {
			c.check("Scan", "Spam score badge in Junk",
				boolErr(has(page, ".spam-badge")))
		} else {
			c.check("Scan", "Junk empty or no scored messages (skip badge check)", nil)
		}
	}
}
