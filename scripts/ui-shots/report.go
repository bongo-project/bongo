package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// Finding kinds.
const (
	KindConsole = "console"
	KindCSP     = "csp"
	KindAsset   = "asset"
)

// Finding is one problem seen while capturing a route.
type Finding struct {
	Kind   string
	Detail string
}

// Result is the outcome of capturing one route in one variant.
type Result struct {
	Route      string
	Variant    string
	Screenshot string
	Findings   []Finding
	// Err is set when the capture itself failed (navigation timeout, wrong
	// theme, missing element). It makes the run fail like a finding does.
	Err error
}

func (r Result) count(kind string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Kind == kind {
			n++
		}
	}
	return n
}

// sameOrigin reports whether rawURL is on origin (scheme://host:port).
func sameOrigin(origin, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Scheme == o.Scheme && u.Host == o.Host
}

// isAssetFailure reports whether a response is a same-origin HTTP error.
// 401 and 403 are deliberately included so a wrong route list is visible.
func isAssetFailure(origin, rawURL string, status int) bool {
	return status >= 400 && sameOrigin(origin, rawURL)
}

// isLoadFailure reports whether a failed network load matters. Cancelled
// loads (navigation away, HTMX aborts) are ignored unless the browser blocked
// them, as it does for a CSP violation.
func isLoadFailure(origin, rawURL string, canceled bool, blockedReason string) bool {
	if !sameOrigin(origin, rawURL) {
		return false
	}
	return !canceled || blockedReason != ""
}

// exitCode is 0 when every capture was clean and 1 otherwise.
func exitCode(results []Result) int {
	for _, r := range results {
		if r.Err != nil || len(r.Findings) > 0 {
			return 1
		}
	}
	return 0
}

// writeSummary prints the per-route table followed by detail lines.
func writeSummary(w io.Writer, results []Result) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ROUTE\tVARIANT\tCONSOLE\tCSP\tASSETS\tSTATUS\tSCREENSHOT")
	for _, r := range results {
		status := "ok"
		if r.Err != nil {
			status = "FAILED"
		} else if len(r.Findings) > 0 {
			status = "ERRORS"
		}
		shot := r.Screenshot
		if r.Err != nil || shot == "" {
			shot = "(withheld)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\n", r.Route, r.Variant,
			r.count(KindConsole), r.count(KindCSP), r.count(KindAsset), status, shot)
	}
	_ = tw.Flush()

	var details []string
	for _, r := range results {
		if r.Err != nil {
			details = append(details, fmt.Sprintf("  %s/%s: capture failed: %v", r.Route, r.Variant, r.Err))
		}
		counts := map[string]int{}
		var order []string
		for _, f := range r.Findings {
			line := fmt.Sprintf("[%s] %s", f.Kind, shorten(f.Detail))
			if counts[line] == 0 {
				order = append(order, line)
			}
			counts[line]++
		}
		for _, line := range order {
			suffix := ""
			if counts[line] > 1 {
				suffix = fmt.Sprintf(" (x%d)", counts[line])
			}
			details = append(details, fmt.Sprintf("  %s/%s: %s%s", r.Route, r.Variant, line, suffix))
		}
	}
	if len(details) > 0 {
		fmt.Fprintln(w, "\nDetails:")
		for _, d := range details {
			fmt.Fprintln(w, d)
		}
	}
}

// shorten keeps the first line of a finding (stack traces follow it) and caps
// its length.
func shorten(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}

// resolvePath returns an absolute path with symlinks resolved as far as the
// path exists; the non-existent tail is appended unresolved.
func resolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	existing, tail := abs, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		tail = filepath.Join(filepath.Base(existing), tail)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	return filepath.Join(real, tail), nil
}

// checkOutDir resolves out and refuses any location inside repoRoot, so the
// harness can never write screenshots into the repository.
func checkOutDir(out, repoRoot string) (string, error) {
	realOut, err := resolvePath(out)
	if err != nil {
		return "", fmt.Errorf("resolve output dir: %w", err)
	}
	realRepo, err := resolvePath(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	rel, err := filepath.Rel(realRepo, realOut)
	// ".." and "../x" are outside the repository; "..shots" is a directory inside it.
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("output dir %s is inside the repository %s; choose a directory outside it", realOut, realRepo)
	}
	return realOut, nil
}

// findRepoRoot walks up from dir to the directory holding go.mod.
func findRepoRoot(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		d = parent
	}
}
