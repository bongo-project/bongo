// ui-test exercises the Bongo web UI end-to-end using a headless browser (Rod).
// It assumes validate-server has already run (users + seed data exist).
//
// Usage:
//
//	go run ./scripts/ui-test/... [flags]
//
// Flags:
//
//	-host       Server hostname          (default "localhost")
//	-web-port   HTTPS web port           (default 8080)
//	-headed     Run with visible browser  (default false)
//	-slow       Slow-motion delay in ms   (default 0)
//	-admin-user Admin email               (default "admin@localhost")
//	-admin-pass Admin password            (default "admin")
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

var (
	host       = flag.String("host", "localhost", "Server hostname")
	webPort    = flag.Int("web-port", 8080, "Web UI port (HTTPS)")
	headed     = flag.Bool("headed", false, "Run with visible browser")
	slowMotion = flag.Int("slow", 0, "Slow-motion delay in milliseconds")
	adminUser  = flag.String("admin-user", "admin@localhost", "Admin email")
	adminPass  = flag.String("admin-pass", "admin", "Admin password")
)

// Default timeout for element lookups.
const elTimeout = 5 * time.Second

func main() {
	flag.Parse()

	fmt.Println("=========================================")
	fmt.Println("    Bongo Mail -- UI Test Suite (Rod)    ")
	fmt.Println("=========================================")
	fmt.Printf("Target: %s\n", baseURL())

	// Launch browser.
	l := launcher.New()
	if *headed {
		l = l.Headless(false)
	} else {
		l = l.Headless(true)
	}
	l = l.Set("ignore-certificate-errors", "")

	controlURL, err := l.Launch()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to launch browser: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure Chrome/Chromium is installed.\n")
		fmt.Fprintf(os.Stderr, "  On macOS:  brew install --cask chromium\n")
		fmt.Fprintf(os.Stderr, "  On Linux:  apt install chromium-browser\n")
		os.Exit(1)
	}

	browser := rod.New().ControlURL(controlURL)
	if *slowMotion > 0 {
		browser = browser.SlowMotion(time.Duration(*slowMotion) * time.Millisecond)
	}
	browser = browser.MustConnect()
	defer browser.MustClose()

	browser.MustIgnoreCertErrors(true)

	c := &checker{}

	// Phase 1: Login / Auth
	phase1Login(c, browser)

	// Phase 2: Navigation (returns alice's logged-in page)
	alicePage := phase2Navigation(c, browser)
	defer alicePage.MustClose()

	// Phase 3: Mail reading, compose, reply, forward
	phase3Mail(c, alicePage)

	// Phase 4: Bulk actions, context menu, keyboard shortcuts
	phase4Bulk(c, alicePage)

	// Phase 5: Calendar
	phase5Calendar(c, alicePage)

	// Phase 6: Contacts
	phase6Contacts(c, alicePage)

	// Phase 7: Settings, security, filter rules
	phase7Settings(c, alicePage)

	// Phase 8: Admin panel (fresh admin login)
	phase8Admin(c, browser)

	// Phase 9: Send & receive (alice -> bob)
	phase9Send(c, browser)

	// Phase 10: CSS & layout checks
	phase10CSS(c, alicePage)

	// Phase 11: Quarantine admin page
	phase11Quarantine(c, browser)

	// Phase 12: Mailbox spam scan UI
	phase12Scan(c, browser)

	c.report()

	if c.failed > 0 {
		os.Exit(1)
	}
}
