package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
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

	phaseShell(c, page)

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

// phaseShell checks the keyboard behaviour of the top bar: Ctrl-K focuses
// search, is ignored while typing, and the account menu opens and closes by
// keyboard and by an outside click.
func phaseShell(c *checker, page *rod.Page) {
	active := func() string {
		return evalPage(page, `document.activeElement ? document.activeElement.id || document.activeElement.tagName : ""`)
	}
	ctrlK := `document.dispatchEvent(new KeyboardEvent('keydown', {key: 'k', ctrlKey: true, bubbles: true, cancelable: true}))`
	ctrlKOn := func(sel string) string {
		return `document.querySelector("` + sel + `").dispatchEvent(new KeyboardEvent('keydown', {key: 'k', ctrlKey: true, bubbles: true, cancelable: true}))`
	}

	evalPage(page, `document.activeElement && document.activeElement.blur()`)
	evalPage(page, ctrlK)
	c.check("Shell", "Ctrl-K focuses search", boolErr(active() == "global-search"))

	// Ctrl-K inside another field must leave focus where it is.
	if has(page, "#main-content input, #main-content textarea") {
		evalPage(page, `(document.querySelector("#main-content input:not([type=hidden]), #main-content textarea")).focus()`)
		before := active()
		evalPage(page, ctrlKOn("#main-content input:not([type=hidden]), #main-content textarea"))
		c.check("Shell", "Ctrl-K ignored while typing", boolErr(active() == before && before != "global-search"))
	}

	// Account menu by keyboard.
	evalPage(page, `document.querySelector(".avatar-chip").focus()`)
	_ = page.Keyboard.Type(input.Enter)
	waitStable(page)
	c.check("Shell", "Enter opens account menu", boolErr(isVisible(page, ".account-dropdown:not([hidden])")))
	_ = page.Keyboard.Type(input.Escape)
	waitStable(page)
	c.check("Shell", "Escape closes account menu", boolErr(!isVisible(page, ".account-dropdown:not([hidden])")))
	c.check("Shell", "Escape returns focus to button", boolErr(evalPage(page, `document.activeElement.classList.contains("avatar-chip")`) == "true"))

	_ = openAccountMenu(page)
	evalPage(page, `document.querySelector("#main-content").click()`)
	waitStable(page)
	c.check("Shell", "Outside click closes account menu", boolErr(!isVisible(page, ".account-dropdown:not([hidden])")))
}
