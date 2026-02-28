package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func phase11Quarantine(c *checker, browser *rod.Browser) {
	fmt.Println("\n--- Phase 11: Quarantine Admin ---")

	page := browser.MustPage("")
	defer page.MustClose()

	if err := login(page, *adminUser, *adminPass); err != nil {
		c.check("Quarantine", "Admin login", err)
		return
	}

	// --- Navigate to quarantine ---
	if err := nav(page, "/admin/quarantine"); err != nil {
		c.check("Quarantine", "Navigate to quarantine", err)
		return
	}

	c.check("Quarantine", "Quarantine page loads",
		boolErr(strings.Contains(currentURL(page), "/admin/quarantine")))

	// Page title and subtitle.
	c.check("Quarantine", "Page title",
		boolErr(pageContains(page, "Quarantine")))
	c.check("Quarantine", "Page subtitle",
		boolErr(pageContains(page, "Messages held for review")))

	// Table structure.
	c.check("Quarantine", "Quarantine table exists",
		boolErr(has(page, "table.admin-table")))
	c.check("Quarantine", "Table has aria-label",
		boolErr(has(page, `table[aria-label="Quarantined messages"]`)))

	// Table headers.
	for _, col := range []string{"Date", "Sender", "Subject", "Reason", "Score", "Actions"} {
		c.check("Quarantine", fmt.Sprintf("Column header: %s", col),
			boolErr(pageContains(page, col)))
	}

	// Select-all checkbox in header.
	c.check("Quarantine", "Select-all checkbox",
		boolErr(has(page, `thead input[type="checkbox"]`)))

	// Empty state (dev mode likely has no quarantined messages).
	bodyText := pageText(page)
	hasMessages := !strings.Contains(bodyText, "No quarantined messages")
	if hasMessages {
		// If there are messages, check row structure.
		c.check("Quarantine", "Message rows present",
			boolErr(countEls(page, "tbody tr") > 0))
		c.check("Quarantine", "Row checkboxes",
			boolErr(has(page, `tbody input[type="checkbox"]`)))
		c.check("Quarantine", "Release button exists",
			boolErr(has(page, `form[action*="/release"] button`)))
		c.check("Quarantine", "Delete button exists",
			boolErr(has(page, `form[action*="/delete"] button`)))
		c.check("Quarantine", "Score badge",
			boolErr(has(page, ".badge")))
	} else {
		c.check("Quarantine", "Empty state message",
			boolErr(pageContains(page, "No quarantined messages")))
	}

	// Bulk action forms exist (hidden via Alpine x-show when nothing selected).
	c.check("Quarantine", "Bulk release form",
		boolErr(has(page, `form[action="/admin/quarantine/bulk"]`)))

	// Alpine.js quarantineBulk data binding.
	c.check("Quarantine", "Alpine quarantineBulk component",
		boolErr(has(page, `[x-data="quarantineBulk()"]`)))

	// --- Admin nav has quarantine link ---
	c.check("Quarantine", "Admin nav quarantine link",
		boolErr(has(page, `a[href="/admin/quarantine"]`)))
	c.check("Quarantine", "Admin nav quarantine active",
		boolErr(hasAny(page,
			`a[href="/admin/quarantine"].active`,
			`a[href="/admin/quarantine"][aria-current]`)))
}
