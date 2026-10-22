package mimesafe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/testutil"
)

func TestReadEnvelopeDegradesOverLimit(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"depth 24 unclosed", testutil.DeepUnclosedMultipart(24)},
		{"depth just over cap", testutil.DeepUnclosedMultipart(MaxDepth + 1)},
		{"10k flat parts", testutil.FlatMultipart(10000)},
		{"parts just over cap", testutil.FlatMultipart(MaxParts + 1)},
		{"deep with folded spaced top Content-Type", bytes.Replace(testutil.DeepUnclosedMultipart(24),
			[]byte("Content-Type: multipart/mixed; boundary=\"b0\"\r\n"),
			[]byte("Content-Type :\r\n multipart/mixed;\r\n\tboundary=\"b0\"\r\n"), 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			env, degraded, err := ReadEnvelope(tt.raw)
			if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
				t.Errorf("took %v, want < 100ms", elapsed)
			}
			if err != nil {
				t.Fatalf("ReadEnvelope: %v", err)
			}
			if !degraded {
				t.Error("degraded = false, want true")
			}
			if got := env.GetHeader("Subject"); got == "" {
				t.Error("Subject lost in degraded envelope")
			}
			if got := env.GetHeader("From"); got != "sender@example.com" {
				t.Errorf("From = %q", got)
			}
			if env.Text != "" || env.HTML != "" || len(env.Attachments) != 0 {
				t.Errorf("degraded envelope has body content: text=%q html=%q att=%d", env.Text, env.HTML, len(env.Attachments))
			}
		})
	}
}

func TestCheckErrorWrapsSentinel(t *testing.T) {
	_, _, err := Check(testutil.DeepUnclosedMultipart(MaxDepth + 3))
	if !errors.Is(err, ErrTooComplex) {
		t.Errorf("err = %v, want ErrTooComplex", err)
	}
	_, _, err = Check(testutil.FlatMultipart(MaxParts + 3))
	if !errors.Is(err, ErrTooComplex) {
		t.Errorf("err = %v, want ErrTooComplex", err)
	}
}

const forwardedMessage = "From: a@example.com\r\nTo: b@example.com\r\nSubject: fwd\r\nMIME-Version: 1.0\r\n" +
	"Content-Type: multipart/alternative; boundary=\"alt\"\r\n\r\n" +
	"--alt\r\nContent-Type: text/plain\r\n\r\nplain body\r\n" +
	"--alt\r\nContent-Type: multipart/mixed; boundary=\"mix\"\r\n\r\n" +
	"--mix\r\nContent-Type: text/html\r\n\r\n<p>html body</p>\r\n" +
	"--mix\r\nContent-Type: message/rfc822\r\n\r\n" +
	"From: c@example.com\r\nSubject: inner\r\nContent-Type: multipart/mixed; boundary=\"inner\"\r\n\r\n" +
	"--inner\r\nContent-Type: text/plain\r\n\r\ninner body\r\n--inner--\r\n" +
	"--mix\r\nContent-Type: application/pdf; name=\"a.pdf\"\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nQUJD\r\n--mix--\r\n--alt--\r\n"

func closedNested(depth int) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nSubject: closed\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"c0\"\r\n\r\n")
	for i := range depth - 1 {
		fmt.Fprintf(&b, "--c%d\r\nContent-Type: multipart/mixed; boundary=\"c%d\"\r\n\r\n", i, i+1)
	}
	fmt.Fprintf(&b, "--c%d\r\nContent-Type: text/plain\r\n\r\nleaf\r\n", depth-1)
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "--c%d--\r\n", i)
	}
	return []byte(b.String())
}

func TestReadEnvelopeLegitimateMailUnchanged(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"forwarded inside mixed inside alternative", []byte(forwardedMessage)},
		{"depth at cap, closed", closedNested(MaxDepth)},
		{"parts at cap", testutil.FlatMultipart(MaxParts)},
		{"plain text", []byte("From: a@example.com\r\nSubject: x\r\n\r\nbody\r\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, wantErr := enmime.ReadEnvelope(bytes.NewReader(tt.raw))
			got, degraded, err := ReadEnvelope(tt.raw)
			if degraded {
				t.Fatal("legitimate message was degraded")
			}
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("err = %v, enmime err = %v", err, wantErr)
			}
			if got.Text != want.Text || got.HTML != want.HTML || len(got.Attachments) != len(want.Attachments) {
				t.Errorf("parse differs from enmime: text %q/%q html %q/%q att %d/%d",
					got.Text, want.Text, got.HTML, want.HTML, len(got.Attachments), len(want.Attachments))
			}
		})
	}
}

func TestReadEnvelopeForwardedBodyIntact(t *testing.T) {
	env, degraded, err := ReadEnvelope([]byte(forwardedMessage))
	if err != nil || degraded {
		t.Fatalf("err=%v degraded=%v", err, degraded)
	}
	if !strings.Contains(env.Text, "plain body") || !strings.Contains(env.HTML, "html body") {
		t.Errorf("bodies lost: text=%q html=%q", env.Text, env.HTML)
	}
	if len(env.Attachments) != 1 {
		t.Errorf("attachments = %d, want 1", len(env.Attachments))
	}
}

// TestCheckCounts pins the scanner's exit paths: header forms enmime accepts,
// implicit closing of inner levels, and non-multipart input.
func TestCheckCounts(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantDepth int
		wantParts int
	}{
		{"not multipart", "Subject: x\r\n\r\nbody", 0, 0},
		{"empty", "", 0, 0},
		{"header only no body", "Content-Type: multipart/mixed; boundary=b\r\n", 1, 1},
		{"lf only line endings", "Content-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: text/plain\n\nx\n--b--\n", 1, 1},
		{"folded content type", "Content-Type: multipart/mixed;\r\n\tboundary=\"b\"\r\n\r\n--b\r\n\r\nx\r\n--b--\r\n", 1, 1},
		{"upper case header and type", "CONTENT-TYPE: MULTIPART/Mixed; BOUNDARY=b\r\n\r\n--b\r\n\r\nx\r\n--b--\r\n", 1, 1},
		{"unquoted with trailing params", "Content-Type: multipart/mixed; boundary=b; charset=utf-8\r\n\r\n--b\r\n\r\nx\r\n--b\r\n\r\ny\r\n--b--\r\n", 1, 2},
		{"missing separator blank line in part", "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n--b\r\nContent-Type: text/plain\r\n--b--\r\n", 1, 2},
		{"nested closed then sibling", "Content-Type: multipart/mixed; boundary=o\r\n\r\n--o\r\nContent-Type: multipart/alternative; boundary=i\r\n\r\n--i\r\n\r\na\r\n--i--\r\n--o\r\n\r\nb\r\n--o--\r\n", 2, 3},
		{"outer delimiter ends unclosed inner", "Content-Type: multipart/mixed; boundary=o\r\n\r\n--o\r\nContent-Type: multipart/alternative; boundary=i\r\n\r\n--i\r\n\r\na\r\n--o\r\n\r\nb\r\n--o--\r\n", 2, 3},
		{"epilogue after close ignored", "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n\r\nx\r\n--b--\r\n--b\r\n--b\r\n--b\r\n", 1, 1},
		{"body text with dashes", "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\n\r\n--- not a boundary\r\n-- nor this\r\n--b--\r\n", 1, 1},
		{"malformed content type falls back", "Content-Type: multipart/mixed; boundary=\"b\r\n\r\n--b\r\n\r\nx\r\n--b--\r\n", 1, 1},
		// enmime reads a level's text before its first delimiter leniently, past
		// blank lines, and finds a nested Content-Type there. The scanner must
		// follow it (counting conservatively) or a chain of such levels is missed.
		{"preamble content type after blank line", "Content-Type: multipart/mixed; boundary=o\r\n\r\n\r\nContent-Type: multipart/mixed; boundary=i\r\n\r\n--i\r\nx\r\n--i--\r\n", 2, 2},
		{"preamble junk then content type", "Content-Type: multipart/mixed; boundary=o\r\n\r\n\r\njunk\r\n\r\nContent-Type: multipart/mixed; boundary=i\r\n\r\n--i\r\nContent-Type: multipart/mixed; boundary=j\r\n\r\n--j\r\nx\r\n--j--\r\n--i--\r\n--o--\r\n", 3, 2},
		{"multipart without boundary matches any delimiter", "Content-Type: multipart/mixed\r\n\r\n--b\r\n\r\nx\r\n", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, p, err := Check([]byte(tt.raw))
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if d != tt.wantDepth || p != tt.wantParts {
				t.Errorf("depth, parts = %d, %d; want %d, %d", d, p, tt.wantDepth, tt.wantParts)
			}
		})
	}
}

func TestCheckLinearOnHugeBody(t *testing.T) {
	big := append(testutil.FlatMultipart(3), bytes.Repeat([]byte("--x not a boundary\r\n"), 200000)...)
	start := time.Now()
	if _, _, err := Check(big); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("Check took %v on a 4MB body", elapsed)
	}
}

// enmimeShape returns the multipart nesting depth and descendant count of the
// tree enmime built.
func enmimeShape(p *enmime.Part) (depth, parts int) {
	childDepth := 0
	for c := p.FirstChild; c != nil; c = c.NextSibling {
		d, n := enmimeShape(c)
		childDepth = max(childDepth, d)
		parts += n + 1
	}
	if p.FirstChild != nil {
		return childDepth + 1, parts
	}
	return 0, parts
}

// genTree builds a random multipart message. closed controls whether inner
// levels write their closing delimiter.
func genTree(r *rand.Rand, b *strings.Builder, level, maxLevel int, next *int, closed bool) {
	id := *next
	*next++
	for range 1 + r.IntN(3) {
		fmt.Fprintf(b, "--b%d\r\n", id)
		if level < maxLevel && r.IntN(2) == 0 {
			fmt.Fprintf(b, "Content-Type: multipart/mixed; boundary=\"b%d\"\r\n\r\n", *next)
			genTree(r, b, level+1, maxLevel, next, closed)
			continue
		}
		fmt.Fprintf(b, "Content-Type: text/plain\r\n\r\nleaf %d\r\n", r.IntN(1000))
	}
	if closed || r.IntN(2) == 0 {
		fmt.Fprintf(b, "--b%d--\r\n", id)
	}
}

// TestCheckNeverUndercountsEnmime is the property that keeps the pre-scan
// honest: for generated trees, Check's depth and part counts are never below
// the tree enmime actually builds.
func TestCheckNeverUndercountsEnmime(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 300 {
		var b strings.Builder
		b.WriteString("From: a@example.com\r\nSubject: gen\r\nContent-Type: multipart/mixed; boundary=\"b0\"\r\n\r\n")
		next := 1
		genTree(r, &b, 1, 1+r.IntN(7), &next, i%2 == 0)
		raw := []byte(b.String())

		env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
		if err != nil || env.Root == nil {
			continue
		}
		wantDepth, wantParts := enmimeShape(env.Root)
		gotDepth, gotParts, cerr := Check(raw)
		if cerr != nil {
			continue
		}
		if gotDepth < wantDepth || gotParts < wantParts {
			t.Fatalf("case %d: Check depth/parts = %d/%d, enmime built %d/%d\n%s", i, gotDepth, gotParts, wantDepth, wantParts, raw)
		}
	}
}

func FuzzCheck(f *testing.F) {
	f.Add([]byte(forwardedMessage))
	f.Add(testutil.DeepUnclosedMultipart(5))
	f.Add(closedNested(4))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=b\n\n--b\n\n--b--"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, p, err := Check(raw)
		if err != nil {
			return
		}
		if d > MaxDepth || p > MaxParts {
			t.Fatalf("Check accepted depth=%d parts=%d", d, p)
		}
		if d > 8 || p > 50 || len(raw) > 1<<16 {
			return // keep enmime's own cost out of the fuzz loop
		}
		env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
		if err != nil || env.Root == nil {
			return
		}
		wd, wp := enmimeShape(env.Root)
		if d < wd || p < wp {
			t.Fatalf("under-count: Check %d/%d, enmime %d/%d", d, p, wd, wp)
		}
	})
}

// TestNoDirectEnmimeParsing keeps mimesafe the only caller of enmime's parse
// entry points, so no new site can bypass the depth and part limits.
func TestNoDirectEnmimeParsing(t *testing.T) {
	root := filepath.Join("..", "..")
	banned := map[string]bool{"ReadEnvelope": true, "ReadParts": true, "NewParser": true}
	var offenders []string

	self := filepath.Join(root, "internal", "mimesafe")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == self {
				return filepath.SkipDir
			}
			switch d.Name() {
			case ".git", ".claude", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// cmd/bongo-import parses the operator's own data, not untrusted mail.
		if strings.Contains(filepath.ToSlash(path), "cmd/bongo-import/") {
			return nil
		}
		src, rerr := os.ReadFile(path) // #nosec G304 -- walking the repo's own source tree
		if rerr != nil {
			return rerr
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if perr != nil {
			return perr
		}
		alias := ""
		for _, imp := range file.Imports {
			if imp.Path.Value == `"github.com/jhillyerd/enmime"` {
				alias = "enmime"
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				if alias == "." {
					// A dot-import hides the calls from the selector check.
					offenders = append(offenders, path+": dot-import of enmime")
				}
			}
		}
		if alias == "" {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == alias && banned[sel.Sel.Name] {
					offenders = append(offenders, path+": enmime."+sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("direct enmime parsing outside internal/mimesafe (use mimesafe.ReadEnvelope):\n%s", strings.Join(offenders, "\n"))
	}
}

// countingHandler counts records by level.
type countingHandler struct {
	mu     sync.Mutex
	counts map[slog.Level]int
	attrs  map[string]any
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.counts[r.Level]++
	if r.Level == slog.LevelWarn {
		h.attrs = map[string]any{}
		r.Attrs(func(a slog.Attr) bool { h.attrs[a.Key] = a.Value.Any(); return true })
	}
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }

func TestDegradedLogIsRateLimited(t *testing.T) {
	h := &countingHandler{counts: map[slog.Level]int{}}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	clock := time.Unix(1_700_000_000, 0)
	degradedLog.Lock()
	oldNow, oldLast, oldSup := degradedLog.now, degradedLog.last, degradedLog.suppressed
	degradedLog.now = func() time.Time { return clock }
	degradedLog.last, degradedLog.suppressed = time.Time{}, 0
	degradedLog.Unlock()
	t.Cleanup(func() {
		degradedLog.Lock()
		degradedLog.now, degradedLog.last, degradedLog.suppressed = oldNow, oldLast, oldSup
		degradedLog.Unlock()
	})

	raw := testutil.DeepUnclosedMultipart(MaxDepth + 1)
	const n = 20
	for range n {
		if _, degraded, err := ReadEnvelope(raw); err != nil || !degraded {
			t.Fatalf("degraded = %v, err = %v", degraded, err)
		}
		clock = clock.Add(time.Second)
	}
	if got := h.counts[slog.LevelWarn]; got != 1 {
		t.Errorf("Warn lines in one interval = %d, want 1", got)
	}
	if got := h.counts[slog.LevelDebug]; got != n-1 {
		t.Errorf("Debug lines = %d, want %d", got, n-1)
	}

	clock = clock.Add(warnInterval)
	if _, _, err := ReadEnvelope(raw); err != nil {
		t.Fatal(err)
	}
	if got := h.counts[slog.LevelWarn]; got != 2 {
		t.Errorf("Warn lines after interval = %d, want 2", got)
	}
	if got := h.attrs["suppressed_since_last"]; got != int64(n-1) {
		t.Errorf("suppressed_since_last = %v, want %d", got, n-1)
	}
}
