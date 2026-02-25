package main

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func phase1Login(c *checker, browser *rod.Browser) {
	fmt.Println("\n--- Phase 1: Login ---")

	// Login page renders.
	page := browser.MustPage("")
	defer page.MustClose()
	_ = nav(page, "/login")
	c.check("Login", "Login page loads",
		boolErr(strings.Contains(currentURL(page), "/login")))
	c.check("Login", "Login form exists",
		boolErr(has(page, "form")))
	c.check("Login", "Email input exists",
		boolErr(has(page, "input[name='email']")))
	c.check("Login", "Password input exists",
		boolErr(has(page, "input[name='password']")))
	c.check("Login", "Submit button exists",
		boolErr(has(page, "button[type='submit']")))

	// Admin login.
	err := login(page, *adminUser, *adminPass)
	c.check("Login", "Admin login completes", err)
	url := currentURL(page)
	c.check("Login", "Admin redirected after login",
		boolErr(strings.Contains(url, "/mail") || strings.Contains(url, "/admin")))

	// Alice login.
	page2 := browser.MustPage("")
	defer page2.MustClose()
	err = login(page2, "alice@localhost", "alice123")
	c.check("Login", "Alice login completes", err)
	url2 := currentURL(page2)
	c.check("Login", "Alice redirected after login",
		boolErr(strings.Contains(url2, "/mail")))

	// Bad password.
	page3 := browser.MustPage("")
	defer page3.MustClose()
	_ = login(page3, "alice@localhost", "wrongpassword")
	url3 := currentURL(page3)
	c.check("Login", "Bad password stays on login",
		boolErr(strings.Contains(url3, "/login") || pageContains(page3, "nvalid")))

	// Bad password shows error message.
	c.check("Login", "Bad password shows error",
		boolErr(pageContains(page3, "nvalid") || pageContains(page3, "rror") || pageContains(page3, "incorrect")))

	// Logout clears session.
	page4 := browser.MustPage("")
	defer page4.MustClose()
	if err := login(page4, "alice@localhost", "alice123"); err == nil {
		// POST /logout via the form.
		if has(page4, "form[action='/logout']") {
			_ = click(page4, "form[action='/logout'] button")
			waitStable(page4)
			url4 := currentURL(page4)
			c.check("Login", "Logout redirects to login",
				boolErr(strings.Contains(url4, "/login")))
		} else {
			c.check("Login", "Logout form exists", fmt.Errorf("not found"))
		}
	}

	// Accessing /mail without auth redirects to /login.
	page5 := browser.MustPage("")
	defer page5.MustClose()
	_ = nav(page5, "/mail/INBOX")
	url5 := currentURL(page5)
	c.check("Login", "Unauthenticated /mail redirects to /login",
		boolErr(strings.Contains(url5, "/login")))

	// Accessing /admin without admin role.
	page6 := browser.MustPage("")
	defer page6.MustClose()
	if err := login(page6, "alice@localhost", "alice123"); err == nil {
		_ = nav(page6, "/admin")
		url6 := currentURL(page6)
		body6 := pageText(page6)
		c.check("Login", "Non-admin cannot access /admin",
			boolErr(strings.Contains(url6, "/login") || strings.Contains(url6, "/mail") ||
				strings.Contains(body6, "orbidden") || strings.Contains(body6, "uthorized")))
	}
}
