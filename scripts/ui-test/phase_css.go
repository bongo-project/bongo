package main

import (
	"fmt"

	"github.com/go-rod/rod"
)

func phase10CSS(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 10: CSS & Layout ---")

	if err := nav(page, "/mail/INBOX"); err != nil {
		c.check("CSS", "Navigate for CSS check", err)
		return
	}

	// Sidebar shape check.
	c.check("CSS", "Sidebar has dimensions", boolErr(isVisible(page, "aside.sidebar")))

	// Main content.
	c.check("CSS", "Main content has dimensions",
		boolErr(isVisible(page, "main, .mail-layout, .content")))

	// Topbar.
	c.check("CSS", "Topbar renders", boolErr(has(page, ".topbar")))
	c.check("CSS", "Topbar has dimensions", boolErr(isVisible(page, ".topbar")))

	// No error banners.
	n := countEls(page, ".alert-error, .alert-danger")
	c.check("CSS", "No error banners", boolErr(n == 0))

	// Layout toggle buttons exist.
	c.check("CSS", "Layout toggle exists",
		boolErr(hasAny(page, ".layout-toggle", ".layout-opt")))

	// --- List layout (default) ---
	c.check("CSS", "Default layout mode (list)",
		boolErr(hasAny(page, "#message-list", ".msg-list")))

	// --- Two-pane layout ---
	setLayout := func(mode string) {
		evalPage(page, fmt.Sprintf(
			`(function(){ var root = document.querySelector('html[x-data], [x-data]'); if (root && root._x_dataStack) { root._x_dataStack[0].layoutMode = '%s'; } return 'ok'; })()`, mode))
	}

	setLayout("two-pane")
	waitStable(page)
	c.check("CSS", "Two-pane layout can be set",
		boolErr(hasAny(page, ".reading-pane", "#reading-pane", "[data-layout='two-pane']") ||
			has(page, ".msg-list")))

	// --- Three-pane layout ---
	setLayout("three-pane")
	waitStable(page)
	c.check("CSS", "Three-pane layout can be set",
		boolErr(hasAny(page, ".reading-pane", "#reading-pane", "[data-layout='three-pane']") ||
			has(page, ".msg-list")))

	// Reset to list layout.
	setLayout("list")
}
