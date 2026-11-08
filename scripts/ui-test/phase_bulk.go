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
		`(function(){ var el = document.querySelector('[x-data="messageList"]'); if (el && el._x_dataStack) return String(el._x_dataStack[0].count); return '0'; })()`)
	c.check("Bulk", fmt.Sprintf("Selection count > 0 (got %s)", countVal),
		boolErr(countVal != "0" && countVal != ""))

	// Bulk toolbar HTML — read innerHTML to check for action buttons.
	toolbarHTML := evalPage(page,
		`(function(){ var tb = document.querySelector('.bulk-toolbar'); return tb ? tb.innerHTML : ''; })()`)

	c.check("Bulk", "Select All button exists",
		boolErr(strings.Contains(toolbarHTML, "selectAll")))
	c.check("Bulk", "Deselect button exists",
		boolErr(strings.Contains(toolbarHTML, "deselectAll")))

	evalPage(page, `(function(){ var btn = Array.from(document.querySelectorAll('.bulk-toolbar button')).find(function(el){ return el.getAttribute('x-on:click') === 'selectAll()'; }); if (btn) btn.click(); return 'ok'; })()`)
	time.Sleep(300 * time.Millisecond)
	selectAllState := evalPage(page,
		`(function(){ var el = document.querySelector('[x-data="messageList"]'); if (!el || !el._x_dataStack) return 'missing'; var selected = Object.keys(el._x_dataStack[0].selected); var rows = document.querySelectorAll('.msg-row[data-uid]').length; return selected.indexOf('NaN') === -1 && selected.length === rows ? 'ok' : selected.length + '/' + rows + '/nan=' + (selected.indexOf('NaN') !== -1); })()`)
	c.check("Bulk", "Select All selects only real message rows",
		boolErr(selectAllState == "ok"))

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
	pressAndWait := func(key string, delay time.Duration) {
		pressKey(page, key)
		time.Sleep(delay)
	}
	navigateInbox := func(delay time.Duration) {
		_ = nav(page, "/mail/INBOX")
		time.Sleep(delay)
	}
	navigateInboxAndSelect := func() {
		navigateInbox(400 * time.Millisecond)
		pressKey(page, "j")
	}
	// Blur any focused element.
	evalPage(page, `document.activeElement ? document.activeElement.blur() : null`)
	time.Sleep(200 * time.Millisecond)

	// ? opens shortcuts modal.
	pressKey(page, "?")
	time.Sleep(500 * time.Millisecond)
	c.check("Keyboard", "? opens shortcuts modal",
		boolErr(isVisible(page, "#shortcut-modal-overlay")))

	// Escape closes it.
	pressKey(page, "Escape")
	time.Sleep(300 * time.Millisecond)
	c.check("Keyboard", "Escape closes shortcuts modal",
		boolErr(!isVisible(page, "#shortcut-modal-overlay")))

	// Every documented key, including the shifted forms emitted by some Mac
	// browsers, resolves through the central registry before action dispatch.
	mapped := evalPage(page, `(function(){ var events = [
  {key:'j'}, {key:'k'}, {key:'Enter'}, {key:'r'}, {key:'d'},
  {key:'3', shiftKey:true}, {key:'Delete'}, {key:'e'}, {key:'u'},
  {key:'s'}, {key:'/'}, {key:'c'}, {key:'/', shiftKey:true}
]; return events.map(function(event){ return keyboardShortcutAction(keyboardShortcutKey(event)); }).join(','); })()`)
	c.check("Keyboard", "All documented keys map to their actions", boolErr(mapped ==
		"next,previous,open,reply,delete,delete,delete,archive,toggle-read,toggle-star,search,compose,help"))

	// c opens compose.
	pressKey(page, "c")
	time.Sleep(500 * time.Millisecond)
	c.check("Keyboard", "c navigates to compose",
		boolErr(strings.Contains(currentURL(page), "/compose") || has(page, ".compose-form")))

	// Navigate back.
	navigateInbox(500 * time.Millisecond)

	// / focuses the global search.
	evalPage(page, `document.activeElement ? document.activeElement.blur() : null`)
	time.Sleep(100 * time.Millisecond)
	pressKey(page, "/")
	time.Sleep(300 * time.Millisecond)
	activeID := evalPage(page, `document.activeElement ? document.activeElement.id : ''`)
	c.check("Keyboard", fmt.Sprintf("/ focuses global search (activeElement id=%s)", activeID),
		boolErr(activeID == "global-search"))

	// Blur and test j/k navigation.
	evalPage(page, `document.activeElement ? document.activeElement.blur() : null`)
	time.Sleep(200 * time.Millisecond)
	selectedUID := func() string {
		return evalPage(page, `document.querySelector('.msg-row.keyboard-focus')?.dataset.uid || ''`)
	}

	pressAndWait("j", 300*time.Millisecond)
	firstUID := selectedUID()
	c.check("Keyboard", "j selects the first message", boolErr(firstUID != ""))

	pressAndWait("j", 300*time.Millisecond)
	secondUID := selectedUID()
	c.check("Keyboard", "j advances to the next message", boolErr(secondUID != "" && secondUID != firstUID))

	pressAndWait("k", 300*time.Millisecond)
	backUID := selectedUID()
	c.check("Keyboard", "k returns to the previous message", boolErr(backUID == firstUID))

	assertToggles := func(key, expression, label string) {
		before := evalPage(page, expression)
		pressAndWait(key, 500*time.Millisecond)
		after := evalPage(page, expression)
		c.check("Keyboard", label, boolErr(after != before))
	}
	assertToggles("s", `document.querySelector('.msg-row.keyboard-focus .msg-star')?.classList.contains('flagged') ? 'yes' : 'no'`, "s toggles the selected message star")
	assertToggles("u", `document.querySelector('.msg-row.keyboard-focus')?.classList.contains('unread') ? 'yes' : 'no'`, "u toggles the selected message read state")

	assertNavigates := func(key, path, label string) {
		pressAndWait(key, 500*time.Millisecond)
		c.check("Keyboard", label, boolErr(strings.Contains(currentURL(page), path)))
	}
	assertNavigates("Enter", "/mail/INBOX/", "Enter opens the selected message")

	navigateInboxAndSelect()
	assertNavigates("r", "/compose/reply/", "r opens reply for the selected message")

	assertRemovesRow := func(key, label string) {
		pressKey(page, "j")
		before := countEls(page, ".msg-row[data-uid]")
		pressAndWait(key, 800*time.Millisecond)
		c.check("Keyboard", label, boolErr(countEls(page, ".msg-row[data-uid]") == before-1))
	}
	navigateInbox(400 * time.Millisecond)
	assertRemovesRow("e", "e archives the selected message")
	assertRemovesRow("d", "d deletes the selected message")
}
