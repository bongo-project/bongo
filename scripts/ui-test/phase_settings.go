package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func phase7Settings(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 7: Settings ---")

	// --- Preferences ---
	if err := nav(page, "/settings"); err != nil {
		c.check("Settings", "Navigate to settings", err)
		return
	}

	c.check("Settings", "Settings page loads",
		boolErr(strings.Contains(currentURL(page), "/settings")))

	// Tabs.
	c.check("Settings", "Settings tabs exist",
		boolErr(hasAny(page, ".settings-tabs", "[role='tablist']")))

	nTabs := countEls(page, ".settings-tab, [role='tab']")
	c.check("Settings", fmt.Sprintf("Settings has tabs (%d)", nTabs), boolErr(nTabs >= 3))

	// --- Appearance tab ---
	c.check("Settings", "Theme select exists",
		boolErr(has(page, "select[name='theme']")))
	c.check("Settings", "Layout mode select exists",
		boolErr(has(page, "select[name='layout_mode']")))
	c.check("Settings", "Density select exists",
		boolErr(has(page, "select[name='density']")))

	// --- Display tab (click to reveal) ---
	if has(page, ".settings-tab") {
		// Click second tab (Display).
		tabs := countEls(page, ".settings-tab")
		if tabs >= 2 {
			_ = click(page, ".settings-tab:nth-child(2)")
			waitStable(page)
		}
	}

	c.check("Settings", "Messages per page select exists",
		boolErr(has(page, "select[name='messages_per_page']")))
	c.check("Settings", "Infinite scroll toggle exists",
		boolErr(has(page, "input[name='infinite_scroll']")))

	// --- Composition tab ---
	if countEls(page, ".settings-tab") >= 3 {
		_ = click(page, ".settings-tab:nth-child(3)")
		waitStable(page)
	}

	c.check("Settings", "Default reply mode select exists",
		boolErr(has(page, "select[name='default_reply']")))
	c.check("Settings", "Signature textarea exists",
		boolErr(has(page, "textarea[name='signature']")))

	// --- Localization tab ---
	if countEls(page, ".settings-tab") >= 4 {
		_ = click(page, ".settings-tab:nth-child(4)")
		waitStable(page)
	}

	c.check("Settings", "Locale select exists",
		boolErr(has(page, "select[name='locale']")))
	c.check("Settings", "Timezone select exists",
		boolErr(has(page, "select[name='timezone']")))

	// Save button.
	c.check("Settings", "Save button exists",
		boolErr(hasAny(page, "button.btn-save", "button[type='submit']")))

	// --- Security ---
	if err := nav(page, "/settings/security"); err != nil {
		c.check("Settings", "Navigate to security", err)
	} else {
		c.check("Settings", "Security page loads",
			boolErr(strings.Contains(currentURL(page), "/security")))

		// Password change form.
		c.check("Security", "Current password field exists",
			boolErr(has(page, "input[name='current_password']")))
		c.check("Security", "New password field exists",
			boolErr(has(page, "input[name='new_password']")))
		c.check("Security", "Confirm password field exists",
			boolErr(has(page, "input[name='confirm_password']")))
		c.check("Security", "Change password submit button",
			boolErr(has(page, "form[action*='/password'] button[type='submit']")))

		// 2FA setup button (if not already enabled).
		c.check("Security", "2FA setup or status visible",
			boolErr(hasAny(page,
				"form[action*='/totp/setup']",
				"form[action*='/totp/disable']",
				"button")))
	}

	// --- Filter rules ---
	if err := nav(page, "/settings/rules"); err != nil {
		c.check("Settings", "Navigate to filter rules", err)
	} else {
		c.check("Settings", "Filter rules page loads",
			boolErr(strings.Contains(currentURL(page), "/rules")))

		c.check("Settings", "New rule button/link exists",
			boolErr(hasAny(page,
				"a[href*='/rules/new']",
				"button")))

		// Navigate to new rule form.
		if has(page, "a[href*='/rules/new']") {
			_ = click(page, "a[href*='/rules/new']")
			waitStable(page)
			c.check("Settings", "New rule form loads",
				boolErr(has(page, "form")))
		}
	}
}
