package main

import (
	"fmt"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

func phase13Interactions(c *checker, page *rod.Page) {
	fmt.Println("\n--- Phase 13: Responsive & Keyboard Interactions ---")

	if err := nav(page, "/mail/INBOX"); err != nil {
		c.check("Interactions", "Navigate to inbox", err)
		return
	}
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: 390, Height: 844, DeviceScaleFactor: 1, Mobile: true,
	}); err != nil {
		c.check("Interactions", "Set mobile viewport", err)
		return
	}
	waitStable(page)

	if err := click(page, ".hamburger"); err != nil {
		c.check("Drawer", "Open mobile drawer", err)
	} else {
		waitStable(page)
		c.check("Drawer", "Drawer opens and reports expanded state", boolErr(
			isVisible(page, "#sidebar.open") && evalPage(page, `document.querySelector('.hamburger').getAttribute('aria-expanded')`) == "true"))
		c.check("Drawer", "Opening drawer moves focus inside", boolErr(
			evalPage(page, `document.querySelector('#sidebar').contains(document.activeElement)`) == "true"))

		if has(page, "#sidebar .sidebar-label-toggle") {
			before := evalPage(page, `document.querySelector('#sidebar .sidebar-label-toggle').getAttribute('aria-expanded')`)
			_ = click(page, "#sidebar .sidebar-label-toggle")
			waitStable(page)
			after := evalPage(page, `document.querySelector('#sidebar .sidebar-label-toggle').getAttribute('aria-expanded')`)
			c.check("Drawer", "Collapsible group toggles its expanded state", boolErr(before != after))
		}

		evalPage(page, `(function(){ var controls = Array.from(document.querySelectorAll('#sidebar a[href], #sidebar button:not([disabled]), #sidebar input:not([disabled])')).filter(function(el){ return el.offsetParent !== null; }); controls[controls.length - 1].focus(); return 'ok'; })()`)
		_ = page.Keyboard.Type(input.Tab)
		c.check("Drawer", "Tab wraps within open mobile drawer", boolErr(
			evalPage(page, `(function(){ var first = Array.from(document.querySelectorAll('#sidebar a[href], #sidebar button:not([disabled]), #sidebar input:not([disabled])')).find(function(el){ return el.offsetParent !== null; }); return document.activeElement === first; })()`) == "true"))
		_ = page.Keyboard.Type(input.Escape)
		waitStable(page)
		c.check("Drawer", "Escape closes drawer", boolErr(!isVisible(page, "#sidebar.open")))
		c.check("Drawer", "Escape restores hamburger focus", boolErr(
			evalPage(page, `document.activeElement.classList.contains('hamburger')`) == "true"))
	}

	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: 1440, Height: 900, DeviceScaleFactor: 1,
	}); err != nil {
		c.check("Interactions", "Restore desktop viewport", err)
	}

	if err := nav(page, "/settings"); err == nil {
		evalPage(page, `document.querySelector('[role="tab"]').focus()`)
		_ = page.Keyboard.Type(input.ArrowRight)
		waitStable(page)
		c.check("Tabs", "ArrowRight selects and focuses the next settings tab", boolErr(
			evalPage(page, `document.activeElement.id === 'settings-tab-display' && document.activeElement.getAttribute('aria-selected') === 'true'`) == "true"))
	} else {
		c.check("Tabs", "Navigate to settings", err)
	}

	if err := nav(page, "/mail/INBOX"); err != nil {
		c.check("Dialogs", "Return to inbox", err)
		return
	}
	evalPage(page, `document.querySelector('.layout-opt').focus()`)
	pressKey(page, "?")
	waitStable(page)
	c.check("Dialogs", "Shortcut dialog receives focus", boolErr(
		evalPage(page, `document.activeElement.id === 'shortcut-modal-close'`) == "true"))
	_ = page.Keyboard.Type(input.Escape)
	waitStable(page)
	c.check("Dialogs", "Shortcut dialog restores initiating focus", boolErr(
		evalPage(page, `document.activeElement.classList.contains('layout-opt')`) == "true"))

	evalPage(page, `showClientStatus('Simulated failure', 'error')`)
	c.check("Failure feedback", "Client failure is visible and announced", boolErr(
		isVisible(page, "#client-status.client-status-error") && evalPage(page, `document.querySelector('#client-status').textContent`) == "Simulated failure"))

	evalPage(page, `document.activeElement && document.activeElement.blur()`)
	pressKey(page, "/")
	c.check("Keyboard", "Slash focuses global search exactly", boolErr(
		evalPage(page, `document.activeElement.id`) == "global-search"))
}
