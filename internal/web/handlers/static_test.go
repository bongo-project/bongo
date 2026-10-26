package handlers

import (
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/web/static"
)

var cssURLPattern = regexp.MustCompile(`url\(\s*["']?(/static/[^"')\s]+)["']?\s*\)`)

func mountedStatic() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", StaticHandler()))
	return mux
}

func cssFontURLs(t *testing.T) []string {
	t.Helper()
	var urls []string
	for _, f := range []string{
		"../static/css/componentlib/colors_and_type.css",
		"../static/css/bongo.css",
	} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range cssURLPattern.FindAllStringSubmatch(string(data), -1) {
			urls = append(urls, m[1])
		}
	}
	return urls
}

// A hostile host mime table must not change the font type: the explicit
// header in StaticHandler is what makes it deterministic on minimal images.
func TestStaticHandlerServesEveryCSSURL(t *testing.T) {
	if err := mime.AddExtensionType(".woff2", "application/x-hostile"); err != nil {
		t.Fatal(err)
	}
	urls := cssFontURLs(t)
	fonts := 0
	h := mountedStatic()
	for _, u := range urls {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", u, rec.Code)
			continue
		}
		if strings.HasSuffix(u, ".woff2") {
			fonts++
			if ct := rec.Header().Get("Content-Type"); ct != "font/woff2" {
				t.Errorf("GET %s Content-Type = %q, want font/woff2", u, ct)
			}
			if !strings.HasPrefix(rec.Body.String(), "wOF2") {
				t.Errorf("GET %s is not a woff2 file", u)
			}
		}
	}
	if fonts < 18 {
		t.Errorf("CSS references %d woff2 fonts, want at least 18", fonts)
	}
}

func TestEmbeddedFontSet(t *testing.T) {
	entries, err := fs.ReadDir(static.Files, "fonts")
	if err != nil {
		t.Fatal(err)
	}
	weights := []string{"Thin", "ExtraLight", "Light", "Regular", "Medium", "SemiBold", "Bold", "ExtraBold", "Black"}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
		if path.Ext(e.Name()) == ".ttf" {
			t.Errorf("stale ttf font embedded: %s", e.Name())
		}
	}
	want := 0
	for _, fam := range []string{"Geist", "GeistMono"} {
		for _, w := range weights {
			want++
			if n := fam + "-" + w + ".woff2"; !have[n] {
				t.Errorf("missing embedded font %s", n)
			}
		}
	}
	if !have["OFL.txt"] {
		t.Error("missing OFL.txt")
	}
	if len(entries) != want+1 {
		t.Errorf("fonts dir has %d files, want %d", len(entries), want+1)
	}
}

func TestLoginPageSmoke(t *testing.T) {
	h := NewAuthHandler(nil, []byte("0123456789abcdef0123456789abcdef"))
	rr := httptest.NewRecorder()
	h.LoginPage(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("login page = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `data-accent="bongo"`) {
		t.Error("login page lacks data-accent on <html>")
	}
}
