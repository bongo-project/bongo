//go:build uishots

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

// TestDetectionEndToEnd proves the harness reports a console error, a blocked
// inline script and a 404 asset, and that a clean page stays clean. It serves
// pages behind the real SecurityHeaders middleware. Run with:
//
//	go test -tags uishots ./scripts/ui-shots/...
func TestDetectionEndToEnd(t *testing.T) {
	bin, ok := launcher.LookPath()
	if !ok {
		t.Skip("no Chrome or Chromium found; not downloading one")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>ok</title></head><body><h1>ok</h1></body></html>`))
	})
	mux.HandleFunc("/broken", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>broken</title></head><body>
<script>document.title = 'inline ran'</script>
<img src="/static/missing.png" alt="">
<script src="/static/err.js"></script>
</body></html>`))
	})
	mux.HandleFunc("/static/err.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(`console.error('deliberate console error');`))
	})
	srv := httptest.NewServer(middleware.SecurityHeaders()(mux))
	defer srv.Close()

	l := launcher.New().Bin(bin).Headless(true).Leakless(false).UserDataDir(t.TempDir())
	defer func() { l.Kill(); l.Cleanup() }()
	url, err := l.Launch()
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	b := rod.New().ControlURL(url)
	if err := b.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = b.Close() }()

	opts := captureOpts{origin: srv.URL, timeout: 20 * time.Second, settle: 100 * time.Millisecond}
	v := variants[0]
	dir := t.TempDir()

	ok1 := capturePage(context.Background(), b, "ok", "/ok", v, filepath.Join(dir, "ok.png"), opts)
	if ok1.Err != nil || len(ok1.Findings) != 0 {
		t.Fatalf("clean page: err=%v findings=%v", ok1.Err, ok1.Findings)
	}

	bad := capturePage(context.Background(), b, "broken", "/broken", v, filepath.Join(dir, "broken.png"), opts)
	if bad.Err != nil {
		t.Fatalf("broken page capture: %v", bad.Err)
	}
	if bad.count(KindCSP) < 1 {
		t.Errorf("no CSP violation reported: %v", bad.Findings)
	}
	if bad.count(KindAsset) < 1 {
		t.Errorf("no 404 asset reported: %v", bad.Findings)
	}
	if bad.count(KindConsole) < 1 {
		t.Errorf("no console error reported: %v", bad.Findings)
	}
	foundScriptSrc := false
	for _, f := range bad.Findings {
		if f.Kind == KindCSP && len(f.Detail) >= 10 && f.Detail[:10] == "script-src" {
			foundScriptSrc = true
		}
	}
	if !foundScriptSrc {
		t.Errorf("no script-src violation: %v", bad.Findings)
	}
	if got := exitCode([]Result{ok1, bad}); got == 0 {
		t.Errorf("exit code = 0 with a broken route")
	}

	// The dark guard: /ok never sets data-theme=dark, so the dark variant must
	// fail and write no screenshot.
	darkShot := filepath.Join(dir, "dark.png")
	dark := capturePage(context.Background(), b, "ok", "/ok", variants[1], darkShot, opts)
	if dark.Err == nil {
		t.Errorf("dark variant on a page without data-theme=dark captured without error")
	}
	if _, err := os.Stat(darkShot); err == nil {
		t.Errorf("screenshot written despite the dark guard failing")
	}
	if dark.Screenshot != "" {
		t.Errorf("failed result still names a screenshot: %q", dark.Screenshot)
	}
}
