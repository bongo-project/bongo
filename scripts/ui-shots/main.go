// ui-shots captures every main route of the web UI for visual review and
// reports console errors, CSP violations and failed same-origin requests.
//
// It builds the server into a temporary directory, starts a dev server on free
// ports with ephemeral storage, seeds it (validate-server plus testdata/demo),
// logs in as the dev admin and captures each route at 1440x900 light,
// 1440x900 dark and 390x844 light with headless Chrome. Screenshots are named
// <route>-<variant>.png and written outside the repository.
//
// Usage:
//
//	make ui-shots                 # screenshots go to a new temp dir
//	make ui-shots OUT=~/shots     # screenshots go to ~/shots
//	go run ./scripts/ui-shots -out ~/shots
//
// Flags:
//
//	-out      directory for screenshots (default: new temp dir; must be outside the repository)
//	-timeout  per-route timeout (default 30s)
//	-chrome   path to a Chrome or Chromium binary (default: auto-detected, never downloaded)
//
// Exit status: 0 when every capture is clean, 1 when any route has console,
// CSP or asset errors or a capture failed, 2 when setup failed. It is a local
// tool and is not run in CI.
//
// The 0/1/2 distinction is only visible when running "go run ./scripts/ui-shots"
// directly. "make ui-shots" runs it through go run, which collapses any non-zero
// exit of the program to 1, and make then fails with its own exit status 2. A UI
// finding and a setup failure therefore look the same there; read the output.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	adminUser = "admin@localhost"
	adminPass = "admin"
)

var messageLink = regexp.MustCompile(`/mail/INBOX/(\d+)(?:["'?#]|$)`)

func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func main() { os.Exit(realMain()) }

// realMain returns the exit code so every deferred cleanup runs before exit.
func realMain() (code int) {
	out := flag.String("out", "", "screenshot directory outside the repository (default: new temp dir)")
	timeout := flag.Duration("timeout", 30*time.Second, "per-route timeout")
	chrome := flag.String("chrome", "", "Chrome or Chromium binary (default: auto-detected)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "ui-shots: panic: %v\n", r)
			code = 2
		}
	}()

	results, dir, err := harness(ctx, *out, *chrome, *timeout)
	if err != nil {
		if len(results) > 0 {
			writeSummary(os.Stdout, results)
			fmt.Printf("\nScreenshots (partial): %s\n", dir)
		}
		fmt.Fprintf(os.Stderr, "ui-shots: setup failed: %v\n", err)
		return 2
	}
	writeSummary(os.Stdout, results)
	fmt.Printf("\nScreenshots: %s\n", dir)
	code = exitCode(results)
	if code != 0 {
		fmt.Fprintln(os.Stderr, "ui-shots: errors found (see the table above)")
	}
	return code
}

// harness does everything that needs teardown: its defers close the browser,
// stop the server and remove the work directory on every path.
func harness(ctx context.Context, outFlag, chrome string, timeout time.Duration) (results []Result, outDir string, err error) {
	repo, err := repoRootFromGit()
	if err != nil {
		return nil, "", err
	}
	outDir, err = prepareOutDir(outFlag, repo)
	if err != nil {
		return nil, "", err
	}
	bin, err := chromePath(chrome)
	if err != nil {
		return nil, "", err
	}

	work, err := os.MkdirTemp("", "bongo-ui-shots-work-*")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(work) }()

	fmt.Println("Building bongo, bongo-import and validate-server...")
	if err := build(ctx, repo, work); err != nil {
		return nil, "", err
	}

	fmt.Println("Starting dev server...")
	srv, err := startServer(ctx, work)
	if err != nil {
		return nil, "", err
	}
	defer srv.stop()
	origin := srv.ports.origin()
	fmt.Println("Dev server ready at", origin)

	fmt.Println("Seeding data...")
	if err := seed(ctx, repo, work, srv); err != nil {
		return nil, "", err
	}

	l := launcher.New().Bin(bin).Headless(true).Leakless(false).UserDataDir(filepath.Join(work, "chrome"))
	defer func() { l.Kill(); l.Cleanup() }()
	controlURL, err := l.Launch()
	if err != nil {
		return nil, "", fmt.Errorf("launch browser: %w", err)
	}
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, "", fmt.Errorf("connect browser: %w", err)
	}
	defer func() { _ = browser.Close() }()

	opts := captureOpts{origin: origin, timeout: timeout, settle: 400 * time.Millisecond}
	for _, v := range variants {
		if ctx.Err() != nil {
			return results, outDir, ctx.Err()
		}
		fmt.Println("Capturing", v.name)
		rs, err := captureVariant(ctx, browser, v, outDir, opts)
		results = append(results, rs...)
		if err != nil {
			return results, outDir, err
		}
	}
	return results, outDir, nil
}

// captureVariant uses a fresh incognito context so localStorage and cookies do
// not leak between variants.
func captureVariant(ctx context.Context, b *rod.Browser, v variant, outDir string, o captureOpts) ([]Result, error) {
	ic, err := b.Incognito()
	if err != nil {
		return nil, fmt.Errorf("incognito context: %w", err)
	}
	defer func() { _ = ic.Close() }()

	var results []Result
	shot := func(route string) string { return filepath.Join(outDir, route+"-"+v.name+".png") }
	specs := routeSpecs(time.Now())
	// Anonymous routes first, then log in.
	for _, s := range specs {
		if s.anon {
			results = append(results, capturePage(ctx, ic, s.name, s.path, v, shot(s.name), o))
		}
	}
	if err := login(ctx, ic, o.origin, adminUser, adminPass, o.timeout); err != nil {
		return results, fmt.Errorf("login (%s): %w", v.name, err)
	}
	msgPath, msgErr := findMessagePath(ctx, ic, o)
	for _, s := range specs {
		if s.anon {
			continue
		}
		if s.name == "message" {
			if msgErr != nil {
				results = append(results, Result{Route: s.name, Variant: v.name, Err: msgErr})
				continue
			}
			s.path = msgPath
		}
		results = append(results, capturePage(ctx, ic, s.name, s.path, v, shot(s.name), o))
	}
	return results, nil
}

// findMessagePath scrapes the first message link from the inbox.
func findMessagePath(ctx context.Context, b *rod.Browser, o captureOpts) (string, error) {
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return "", err
	}
	defer func() { _ = page.Close() }()
	p := page.Context(ctx).Timeout(o.timeout)
	if err := p.Navigate(o.origin + "/mail/INBOX"); err != nil {
		return "", err
	}
	if err := p.WaitLoad(); err != nil {
		return "", err
	}
	html, err := p.HTML()
	if err != nil {
		return "", err
	}
	m := messageLink.FindStringSubmatch(html)
	if m == nil {
		return "", errors.New("no message link found in the inbox; the seed data did not reach admin@localhost")
	}
	return "/mail/INBOX/" + m[1], nil
}

func chromePath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if p, ok := launcher.LookPath(); ok {
		return p, nil
	}
	return "", errors.New("no Chrome or Chromium found (the harness never downloads one).\n" +
		"  macOS:  brew install --cask chromium   (or install Google Chrome)\n" +
		"  Linux:  apt install chromium\n" +
		"  or pass -chrome /path/to/chrome")
}

func repoRootFromGit() (string, error) {
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil { // #nosec G204
		if s := strings.TrimSpace(string(out)); s != "" {
			return s, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return findRepoRoot(wd)
}

// prepareOutDir validates and creates the screenshot directory.
func prepareOutDir(flagValue, repo string) (string, error) {
	if flagValue == "" {
		d, err := os.MkdirTemp("", "bongo-ui-shots-*")
		if err != nil {
			return "", err
		}
		flagValue = d
	}
	dir, err := checkOutDir(flagValue, repo)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}
