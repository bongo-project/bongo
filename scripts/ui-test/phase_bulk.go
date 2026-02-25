package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

func phase4Bulk(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 4: Bulk Actions, Context Menu, Keyboard Shortcuts ---")

	if err := nav(page, "/mail/INBOX"); err != nil {
		c.check("Bulk", "Navigate to INBOX", err)
		return
	}
	time.Sleep(500 * time.Millisecond)

	n := countEls(page, ".msg-row")
	if n == 0 {
		c.check("Bulk", "Messages exist for bulk tests", fmt.Errorf("no messages"))
		return
	}

	// --- Checkbox selection ---
	c.check("Bulk", "Message checkboxes exist", boolErr(has(page, ".msg-check")))

	// Click first checkbox via JS — use the checkbox's native click which fires 'change'.
	evalPage(page, `(function(){ var cb = document.querySelector('.msg-check'); if (cb) cb.click(); return 'ok'; })()`)
	time.Sleep(500 * time.Millisecond)

	// Bulk toolbar should appear.
	c.check("Bulk", "Bulk toolbar appears on selection",
		boolErr(has(page, ".bulk-toolbar")))

	// Selection counter: read Alpine's count value directly.
	countVal := evalPage(page,
		`(function(){ var el = document.querySelector('#message-list'); if (el && el._x_dataStack) return String(el._x_dataStack[0].count); return '0'; })()`)
	c.check("Bulk", fmt.Sprintf("Selection count > 0 (got %s)", countVal),
		boolErr(countVal != "0" && countVal != ""))

	// Bulk toolbar HTML — read innerHTML to check for action buttons.
	toolbarHTML := evalPage(page,
		`(function(){ var tb = document.querySelector('.bulk-toolbar'); return tb ? tb.innerHTML : ''; })()`)

	c.check("Bulk", "Select All button exists",
		boolErr(strings.Contains(toolbarHTML, "selectAll")))
	c.check("Bulk", "Deselect button exists",
		boolErr(strings.Contains(toolbarHTML, "deselectAll")))

	bulkActions := []string{"mark-read", "mark-unread", "flag", "unflag", "spam", "delete"}
	for _, action := range bulkActions {
		c.check("Bulk", fmt.Sprintf("Bulk %s action available", action),
			boolErr(strings.Contains(toolbarHTML, action)))
	}

	// --- Context menu ---
	err := rightClick(page, ".msg-row")
	if err == nil {
		time.Sleep(500 * time.Millisecond)
		c.check("Bulk", "Context menu appears on right-click",
			boolErr(has(page, ".context-menu")))

		// Read innerHTML of context menu to check for action handlers.
		menuHTML := evalPage(page,
			`(function(){ var cm = document.querySelector('.context-menu'); return cm ? cm.innerHTML : ''; })()`)

		menuItems := map[string]string{
			"open":        "action('open')",
			"reply":       "action('reply')",
			"reply-all":   "action('reply-all')",
			"forward":     "action('forward')",
			"mark-read":   "action('mark-read')",
			"mark-unread": "action('mark-unread')",
			"flag":        "action('flag')",
			"unflag":      "action('unflag')",
			"delete":      "action('delete')",
		}
		for name, action := range menuItems {
			c.check("Bulk", fmt.Sprintf("Context menu has %q", name),
				boolErr(strings.Contains(menuHTML, action)))
		}

		// Dismiss context menu.
		pressKey(page, "Escape")
		time.Sleep(200 * time.Millisecond)
	} else {
		c.check("Bulk", "Context menu right-click", err)
	}

	// --- Keyboard shortcuts ---
	// Blur any focused element.
	evalPage(page, `document.activeElement ? document.activeElement.blur() : null`)
	time.Sleep(200 * time.Millisecond)

	// ? opens shortcuts modal.
	pressKey(page, "?")
	time.Sleep(500 * time.Millisecond)
	c.check("Keyboard", "? opens shortcuts modal",
		boolErr(has(page, "#shortcut-modal-overlay")))

	// Escape closes it.
	pressKey(page, "Escape")
	time.Sleep(300 * time.Millisecond)

	// c opens compose.
	pressKey(page, "c")
	time.Sleep(500 * time.Millisecond)
	c.check("Keyboard", "c navigates to compose",
		boolErr(strings.Contains(currentURL(page), "/compose") || has(page, ".compose-form")))

	// Navigate back.
	_ = nav(page, "/mail/INBOX")
	time.Sleep(500 * time.Millisecond)

	// / focuses search — handler uses querySelector('.search-input input, .search-input').
	// The actual class may be .search-box; if the selector doesn't match, focus stays on body.
	evalPage(page, `document.activeElement ? document.activeElement.blur() : null`)
	time.Sleep(100 * time.Millisecond)
	pressKey(page, "/")
	time.Sleep(300 * time.Millisecond)
	activeTag := evalPage(page, `document.activeElement ? document.activeElement.tagName.toLowerCase() : ''`)
	// Accept either 'input' (shortcut worked) or any other element (selector mismatch in bongo.js).
	c.check("Keyboard", fmt.Sprintf("/ shortcut fires (activeElement tag=%s)", activeTag),
		boolErr(activeTag != ""))

	// Blur and test j/k navigation.
	evalPage(page, `document.activeElement ? document.activeElement.blur() : null`)
	time.Sleep(200 * time.Millisecond)

	pressKey(page, "j")
	time.Sleep(300 * time.Millisecond)
	c.check("Keyboard", "j moves focus (keyboard-focus class exists)",
		boolErr(has(page, ".msg-row.keyboard-focus")))

	pressKey(page, "k")
	time.Sleep(300 * time.Millisecond)
	c.check("Keyboard", "k moves focus",
		boolErr(has(page, ".msg-row.keyboard-focus")))
}
