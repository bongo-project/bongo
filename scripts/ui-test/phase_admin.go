package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func phase8Admin(c *checker, browser *rod.Browser) {
	fmt.Println("\n--- Phase 8: Admin Panel ---")

	page := browser.MustPage("")
	defer page.MustClose()

	if err := login(page, *adminUser, *adminPass); err != nil {
		c.check("Admin", "Admin login", err)
		return
	}

	// --- Dashboard ---
	if err := nav(page, "/admin"); err != nil {
		c.check("Admin", "Navigate to admin", err)
		return
	}

	c.check("Admin", "Dashboard loads",
		boolErr(strings.Contains(currentURL(page), "/admin")))

	// Stats cards.
	c.check("Admin", "Stat cards exist",
		boolErr(has(page, ".stat-card")))
	nStats := countEls(page, ".stat-card")
	c.check("Admin", fmt.Sprintf("Dashboard stat cards (%d)", nStats), boolErr(nStats >= 3))

	for _, section := range []string{"Users", "Domain", "Queue", "Cluster", "Log"} {
		c.check("Admin", fmt.Sprintf("Dashboard shows %s", section),
			boolErr(pageContains(page, section)))
	}

	// Admin nav sidebar.
	c.check("Admin", "Admin navigation sidebar",
		boolErr(hasAny(page, ".admin-nav", ".admin-sidebar")))

	// Require 2FA checkbox.
	c.check("Admin", "Require 2FA policy control",
		boolErr(hasAny(page,
			"form[action*='require-2fa']",
			"input[name='enabled']")))

	// --- Users ---
	if err := nav(page, "/admin/users"); err != nil {
		c.check("Admin", "Navigate to user list", err)
		return
	}

	for _, user := range []string{"alice@localhost", "bob@localhost", "charlie@localhost"} {
		c.check("Admin", fmt.Sprintf("User list shows %s", user),
			boolErr(pageContains(page, user)))
	}

	c.check("Admin", "User search input",
		boolErr(hasAny(page, "input[name='q']", ".search-input")))

	c.check("Admin", "Create user button",
		boolErr(hasAny(page, "a[href*='/users/new']", "a.btn-primary")))

	c.check("Admin", "User table exists",
		boolErr(hasAny(page, "table.admin-table", "table")))

	c.check("Admin", "Edit user links exist",
		boolErr(hasAny(page, "a[href*='/admin/users/']", "a.btn-outline")))

	// --- Create user form ---
	if err := nav(page, "/admin/users/new"); err == nil {
		c.check("Admin", "New user form loads",
			boolErr(strings.Contains(currentURL(page), "/users/new")))
		c.check("Admin", "User email field",
			boolErr(has(page, "input[name='email']")))
		c.check("Admin", "User display name field",
			boolErr(has(page, "input[name='display_name']")))
		c.check("Admin", "User password field",
			boolErr(has(page, "input[name='password']")))
		c.check("Admin", "Is admin checkbox",
			boolErr(has(page, "input[name='is_admin']")))
	}

	// --- Domains ---
	if err := nav(page, "/admin/domains"); err != nil {
		c.check("Admin", "Navigate to domains", err)
	} else {
		c.check("Admin", "Domains page loads",
			boolErr(strings.Contains(currentURL(page), "/domains")))
		c.check("Admin", "Add domain form",
			boolErr(hasAny(page, "form[action*='/domains']", "input[name='domain']")))
		c.check("Admin", "Domain table exists",
			boolErr(hasAny(page, "table.admin-table", "table")))
	}

	// --- Aliases ---
	if err := nav(page, "/admin/aliases"); err != nil {
		c.check("Admin", "Navigate to aliases", err)
	} else {
		c.check("Admin", "Aliases page loads",
			boolErr(strings.Contains(currentURL(page), "/aliases")))
		c.check("Admin", "Add alias form",
			boolErr(hasAny(page, "form[action*='/aliases']", "input[name='address']")))
		c.check("Admin", "Target user select",
			boolErr(has(page, "select[name='target_user']")))
	}

	// --- Queue ---
	if err := nav(page, "/admin/queue"); err != nil {
		c.check("Admin", "Navigate to queue", err)
	} else {
		c.check("Admin", "Queue page loads",
			boolErr(strings.Contains(currentURL(page), "/admin/queue")))
		c.check("Admin", "Flush queue button exists",
			boolErr(hasAny(page, "form[action*='/queue/flush']", "button.btn-flush")))
	}

	// --- Cluster ---
	if err := nav(page, "/admin/cluster"); err != nil {
		c.check("Admin", "Navigate to cluster", err)
	} else {
		c.check("Admin", "Cluster page loads",
			boolErr(strings.Contains(currentURL(page), "/admin/cluster")))
	}

	// --- Logs ---
	if err := nav(page, "/admin/logs"); err != nil {
		c.check("Admin", "Navigate to logs", err)
	} else {
		c.check("Admin", "Logs page loads",
			boolErr(strings.Contains(currentURL(page), "/admin/logs") || pageContains(page, "Log")))
		c.check("Admin", "Log level select",
			boolErr(has(page, "#log-level")))
		c.check("Admin", "Log component select",
			boolErr(has(page, "#log-component")))
		c.check("Admin", "Log connect button",
			boolErr(has(page, "#log-connect")))
		c.check("Admin", "Log output area",
			boolErr(has(page, "#log-output")))
	}
}
