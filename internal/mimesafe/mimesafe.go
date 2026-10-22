// Package mimesafe is the single choke point for parsing untrusted mail with
// enmime. enmime has no depth, part-count or context option: unclosed nested
// multipart boundaries double its parse time per level (depth 22 takes about a
// second on a 1.3KB message) and a flat multipart with N parts costs O(N^2).
// Check measures both in a linear pre-scan and ReadEnvelope refuses to hand
// over-limit messages to enmime, degrading them to a headers-only envelope.
//
// No code outside this package may call enmime.ReadEnvelope or enmime.ReadParts;
// TestNoDirectEnmimeParsing enforces that.
package mimesafe

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"strings"
	"sync"
	"time"

	"github.com/jhillyerd/enmime"
)

const (
	// MaxDepth is the deepest multipart nesting that is parsed. Measured on an
	// unclosed chain, depth 16 costs about 15ms and 20 costs 250ms, so anything
	// higher defends nothing. Legitimate mail stays at depth 6 or less, and
	// forwarded messages (message/rfc822) are not recursed into by enmime so they
	// add no depth.
	MaxDepth = 16

	// MaxParts is the largest number of MIME parts that is parsed. A flat
	// multipart with 1000 parts costs about 40ms; 10k costs 0.75s.
	MaxParts = 1000
)

// blank is the in-line white space enmime tolerates around header names and
// parameters; a fuzzer found a form feed accepted between "boundary" and "=".
const blank = " \t\f\v"

// ErrTooComplex is returned (wrapped, with the measured counts) when a message
// exceeds MaxDepth or MaxParts.
var ErrTooComplex = errors.New("mime structure too complex")

// Check scans raw for multipart structure without parsing it. It returns the
// deepest nesting level and the number of part delimiters seen, stopping as
// soon as a limit is exceeded. The scan is linear in len(raw). It mirrors
// enmime: only parts whose own Content-Type is multipart with a boundary are
// descended into, and message/rfc822 bodies are not.
//
// The counts are an upper bound on what enmime builds; over-counting is safe,
// under-counting would reintroduce the problem.
func Check(raw []byte) (depth, parts int, err error) {
	hdrEnd, bodyStart := splitHeader(raw)
	var stack []level // active boundaries, innermost last

	b, err := multipartBoundary(raw[:hdrEnd], true)
	if err != nil {
		return 0, 0, err
	}
	if b == nil {
		return 0, 0, nil
	}

	// open pushes boundary b, then follows any chain of nested multipart headers
	// starting at pos. The text after a boundary is read as a part header block
	// both after a delimiter and before the first one, because enmime treats an
	// unterminated or delimiter-free body as a single part and parses its headers.
	open := func(b [][]byte, pos int) (int, error) {
		for b != nil {
			stack = append(stack, newLevel(b))
			if len(stack) > depth {
				depth = len(stack)
				if depth > MaxDepth {
					return pos, fmt.Errorf("%w: depth %d exceeds %d", ErrTooComplex, depth, MaxDepth)
				}
			}
			var hdr []byte
			hdr, pos = partHeader(raw, pos, stack, true)
			var err error
			if b, err = multipartBoundary(hdr, false); err != nil {
				return pos, err
			}
		}
		return pos, nil
	}

	pos, err := open(b, bodyStart)
	if err != nil {
		return depth, parts, err
	}
	for pos < len(raw) {
		line, next := nextLine(raw, pos)
		pos = next
		idx, atStart, col0, closing := matchBoundary(stack, line)
		if idx < 0 {
			continue
		}
		// Only a delimiter at column 0 changes the stack: a near-start or mid-line
		// one is counted as a part but enmime may read it as header text, and
		// wrongly popping would under-count what follows.
		stack[idx].seen = true
		if atStart && col0 {
			// A delimiter for an outer boundary implicitly ends everything inside it.
			stack = stack[:idx+1]
			if closing {
				stack = stack[:idx]
				if len(stack) == 0 {
					// The epilogue after the outermost close is ignored by enmime.
					return depth, parts, nil
				}
				continue
			}
		}
		parts++
		if parts > MaxParts {
			return depth, parts, fmt.Errorf("%w: %d parts exceeds %d", ErrTooComplex, parts, MaxParts)
		}
		var hdr []byte
		hdr, pos = partHeader(raw, pos, stack, false)
		nb, merr := multipartBoundary(hdr, false)
		if merr != nil {
			return depth, parts, merr
		}
		if nb != nil {
			if pos, err = open(nb, pos); err != nil {
				return depth, parts, err
			}
		}
	}
	// A level still open at EOF may yield one more part holding the remainder.
	parts += len(stack)
	if parts > MaxParts {
		return depth, parts, fmt.Errorf("%w: %d parts exceeds %d", ErrTooComplex, parts, MaxParts)
	}
	return depth, parts, nil
}

// partHeader returns the header block starting at pos and the offset after it.
// The block ends at a blank line, or just before a line that delimits a stacked
// boundary (the next delimiter, when the part has no blank line).
//
// With preamble set, pos is the start of a level's text before its first
// delimiter. enmime treats that as a part and finds its headers leniently, past
// junk lines and blank lines, so the block returned is the first Content-Type
// header found in it (with its continuation lines), and the scan resumes after
// that header rather than skipping the preamble, so later delimiters still count.
func partHeader(raw []byte, pos int, stack []level, preamble bool) (hdr []byte, next int) {
	start := pos
	for pos < len(raw) {
		l, n := nextLine(raw, pos)
		if len(l) == 0 && !preamble {
			return raw[start:pos], n
		}
		if idx, atStart, _, _ := matchBoundary(stack, l); idx >= 0 && atStart {
			break
		}
		if _, ok := headerNamed(l, "content-type"); preamble && ok {
			end := n
			for end < len(raw) {
				cl, cn := nextLine(raw, end)
				if idx, atStart, _, _ := matchBoundary(stack, cl); (idx >= 0 && atStart) || len(cl) == 0 || !isContinuation(cl) {
					break
				}
				end = cn
			}
			if b, _ := multipartBoundary(raw[pos:end], false); b != nil {
				return raw[pos:end], end
			}
			n = end
		}
		pos = n
	}
	if preamble {
		return nil, pos
	}
	return raw[start:pos], pos
}

// ReadEnvelope parses raw with enmime after Check. A message over the limits is
// not parsed: the returned envelope is built from the top-level header block
// alone (headers, Subject, From and so on work; Text, HTML and attachments are
// empty) and degraded is true. The error, if any, is enmime's.
func ReadEnvelope(raw []byte) (env *enmime.Envelope, degraded bool, err error) {
	depth, parts, cerr := Check(raw)
	if cerr == nil {
		env, err = enmime.ReadEnvelope(bytes.NewReader(raw))
		return env, false, err
	}
	logDegraded(depth, parts)

	hdrEnd, _ := splitHeader(raw)
	hdr := stripContentHeaders(raw[:hdrEnd])
	hdr = append(hdr, "Content-Type: text/plain\r\n\r\n"...)
	env, err = enmime.ReadEnvelope(bytes.NewReader(hdr))
	return env, true, err
}

// warnInterval is the minimum gap between Warn lines about degraded parses.
const warnInterval = time.Minute

// degradedLog rate-limits the degraded-parse log. One stored hostile message is
// re-parsed on every view, FETCH, index and rescan, so a line per parse would
// let a sender inflate log volume at will. Only counts are logged, never content.
var degradedLog = struct {
	sync.Mutex
	now        func() time.Time // replaced in tests
	last       time.Time
	suppressed int
}{now: time.Now}

// logDegraded logs at Warn at most once per warnInterval, reporting how many
// occurrences were suppressed since the last Warn, and at Debug otherwise.
func logDegraded(depth, parts int) {
	degradedLog.Lock()
	now := degradedLog.now()
	warn := degradedLog.last.IsZero() || now.Sub(degradedLog.last) >= warnInterval
	suppressed := degradedLog.suppressed
	if warn {
		degradedLog.last = now
		degradedLog.suppressed = 0
	} else {
		degradedLog.suppressed++
	}
	degradedLog.Unlock()

	if warn {
		slog.Warn("mimesafe: message exceeds MIME limits, parsing headers only",
			"depth", depth, "parts", parts, "max_depth", MaxDepth, "max_parts", MaxParts,
			"suppressed_since_last", suppressed)
		return
	}
	slog.Debug("mimesafe: message exceeds MIME limits, parsing headers only",
		"depth", depth, "parts", parts)
}

// splitHeader returns the end of the top-level header block and the offset at
// which the body starts.
func splitHeader(raw []byte) (hdrEnd, bodyStart int) {
	pos := 0
	for pos < len(raw) {
		line, next := nextLine(raw, pos)
		if len(line) == 0 {
			return pos, next
		}
		pos = next
	}
	return len(raw), len(raw)
}

// nextLine returns the line at pos without its terminator, and the offset of
// the following line.
func nextLine(raw []byte, pos int) (line []byte, next int) {
	i := bytes.IndexByte(raw[pos:], '\n')
	if i < 0 {
		return bytes.TrimSuffix(raw[pos:], []byte("\r")), len(raw)
	}
	return bytes.TrimSuffix(raw[pos:pos+i], []byte("\r")), pos + i + 1
}

// level is one open multipart level. prefixes are the candidate delimiters
// ("--" plus a candidate boundary): enmime's lenient header handling can read a
// Content-Type in more than one way, so every plausible reading is matched.
// seen records whether any delimiter for the level has appeared yet.
type level struct {
	prefixes [][]byte
	seen     bool
}

func newLevel(boundaries [][]byte) level {
	l := level{prefixes: make([][]byte, len(boundaries))}
	for i, b := range boundaries {
		l.prefixes[i] = append([]byte("--"), b...)
	}
	return l
}

// maxBoundaryCandidates bounds the readings tried for one Content-Type. More
// than this is not legitimate mail, and matching cost grows with the count.
const maxBoundaryCandidates = 16

// nearStart is how far into a line enmime still notices a delimiter. It peeks a
// fixed window after each newline and searches that window, so a delimiter a
// character or two in ("0--b") counts, while one deep in a header line does not.
const nearStart = 2

// matchBoundary reports which stacked delimiter occurs in line, whether it is
// structural (atStart), and whether it is the closing form that pops a level.
//
// A delimiter is structural when it begins within nearStart of the line start,
// when its level has not seen a delimiter yet (enmime skips such a preamble with
// a plain substring search). Any other occurrence is reported with atStart
// false: the caller counts it as a part without touching the stack, which can
// only over-count. The caller pops a level only for a delimiter at column 0,
// since a nearer-the-middle one depends on enmime's window and state.
func matchBoundary(stack []level, line []byte) (idx int, atStart, col0, closing bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		for _, p := range stack[i].prefixes {
			j := findDelim(line, p)
			if j < 0 || (j > nearStart && stack[i].seen) {
				continue
			}
			return i, true, j == 0, j == 0 && bytes.HasPrefix(line[len(p):], []byte("--"))
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		for _, p := range stack[i].prefixes {
			if findDelim(line, p) >= 0 {
				return i, false, false, false
			}
		}
	}
	return -1, false, false, false
}

// findDelim returns the offset of the first occurrence of prefix in line that
// enmime accepts as a delimiter: followed by white space or the end of the line
// (a delimiter), or by "--" (a terminator). "--b;x" is not a delimiter for "b".
func findDelim(line, prefix []byte) int {
	for off := 0; off <= len(line); {
		k := bytes.Index(line[off:], prefix)
		if k < 0 {
			return -1
		}
		j := off + k
		after := line[j+len(prefix):]
		if len(after) == 0 || isSpace(after[0]) || bytes.HasPrefix(after, []byte("--")) {
			return j
		}
		off = j + 1
	}
	return -1
}

// isContinuation reports whether a header line continues the previous header.
// enmime folds not only lines starting with white space but any starting with a
// control character, any without a colon, and any whose name is not printable ASCII.
func isContinuation(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	if line[0] <= ' ' {
		return true
	}
	colon := bytes.IndexByte(line, ':')
	if colon <= 0 {
		return true
	}
	// A key that is not printable ASCII is not a header name; enmime skips it.
	key := bytes.TrimRight(line[:colon], blank)
	if len(key) == 0 {
		return true
	}
	for _, c := range key {
		if c <= ' ' || c >= 0x7f {
			return true
		}
	}
	return false
}

// isSpace matches unicode.IsSpace for a single byte, as enmime applies it.
func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0x85, 0xA0:
		return true
	}
	return false
}

// multipartBoundary returns the boundary for a header block's first
// Content-Type, or nil when the body is not descended into. enmime parses the
// top-level body as multipart only for a multipart/* type (even with no
// boundary, which yields an empty, match-everything boundary here), but
// recurses into a nested part whenever its Content-Type carries a boundary
// parameter, whatever the media type says.
func multipartBoundary(block []byte, top bool) ([][]byte, error) {
	var cands [][]byte
	add := func(v string) {
		// Cut at a parameter separator or space: a shorter prefix matches more
		// lines, which only over-counts, whereas enmime may have stopped the value
		// earlier than ParseMediaType or the lenient reader did.
		if i := strings.IndexAny(v, ";"+blank); i > 0 {
			v = v[:i]
		}
		if v == "" {
			return
		}
		for _, c := range cands {
			if string(c) == v {
				return
			}
		}
		cands = append(cands, []byte(v))
	}
	sawMultipart := false
	// Every Content-Type header is considered: duplicates are legal to send and
	// enmime does not reliably pick the first.
	for _, ct := range headerValues(block, "content-type") {
		// Anywhere in the value, not just at its start: folded junk can precede it.
		if strings.Contains(strings.ToLower(ct), "multipart/") {
			sawMultipart = true
		}
		if _, params, err := mime.ParseMediaType(ct); err == nil || errors.Is(err, mime.ErrInvalidMediaParameter) {
			add(params["boundary"])
		}
		for _, v := range lenientBoundaries(ct) {
			add(v)
		}
	}
	// enmime's header reader folds and skips malformed lines in ways a strict
	// reader does not predict (a fuzzer found a boundary reached through a stray
	// ":" line, and one whose quoting left two readings), so every boundary
	// parameter anywhere in the block is a candidate too.
	for _, v := range lenientBoundaries(string(block)) {
		add(v)
	}
	if len(cands) > maxBoundaryCandidates {
		return nil, fmt.Errorf("%w: %d boundary readings in one header", ErrTooComplex, len(cands))
	}
	if top && !sawMultipart {
		return nil, nil
	}
	if top && cands == nil {
		return [][]byte{{}}, nil
	}
	return cands, nil
}

// lenientBoundaries extracts every boundary=... value from text that
// mime.ParseMediaType may have rejected, since enmime is tolerant of such headers.
func lenientBoundaries(ct string) []string {
	// strings.ToLower can change the byte length of invalid UTF-8, so search
	// the original with a case-folding compare instead of indexing a copy.
	const key = "boundary"
	var out []string
	for i := 0; i+len(key) <= len(ct); i++ {
		if !strings.EqualFold(ct[i:i+len(key)], key) {
			continue
		}
		rest := strings.TrimLeft(ct[i+len(key):], blank+"\r\n")
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		v := strings.TrimSpace(rest[1:])
		if rest, ok := strings.CutPrefix(v, `"`); ok {
			v, _, _ = strings.Cut(rest, `"`)
		} else if j := strings.IndexAny(v, ";"+blank+"\r\n"); j >= 0 {
			v = v[:j]
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// headerNamed reports whether line is a header called name (lower case) and
// returns its value. enmime accepts white space between the name and the colon.
func headerNamed(line []byte, name string) (value []byte, ok bool) {
	if len(line) <= len(name) || !strings.EqualFold(string(line[:len(name)]), name) {
		return nil, false
	}
	rest := bytes.TrimLeft(line[len(name):], blank)
	if len(rest) == 0 || rest[0] != ':' {
		return nil, false
	}
	return rest[1:], true
}

// headerValues returns the unfolded value of every header called name (lower
// case) in block.
func headerValues(block []byte, name string) []string {
	var vals []string
	var val []byte
	found := false
	flush := func() {
		if found {
			vals = append(vals, strings.TrimSpace(strings.ReplaceAll(string(val), "\r", "")))
		}
		found = false
		val = val[:0]
	}
	for pos := 0; pos < len(block); {
		line, next := nextLine(block, pos)
		pos = next
		// enmime also folds colon-less lines into the previous header.
		if found && len(line) > 0 && isContinuation(line) {
			val = append(val, ' ')
			val = append(val, bytes.TrimSpace(line)...)
			continue
		}
		flush()
		if v, ok := headerNamed(line, name); ok {
			found = true
			val = append(val, bytes.TrimSpace(v)...)
		}
	}
	flush()
	return vals
}

// stripContentHeaders removes Content-Type, Content-Transfer-Encoding and
// their folded continuations so the degraded envelope parses as plain text.
func stripContentHeaders(block []byte) []byte {
	out := make([]byte, 0, len(block)+32)
	skipping := false
	for pos := 0; pos < len(block); {
		line, next := nextLine(block, pos)
		pos = next
		if len(line) == 0 {
			continue
		}
		// Folded and colon-less lines continue the previous header, as in headerValues.
		if !isContinuation(line) {
			_, isCT := headerNamed(line, "content-type")
			_, isCTE := headerNamed(line, "content-transfer-encoding")
			skipping = isCT || isCTE
		}
		if !skipping {
			out = append(append(out, line...), '\r', '\n')
		}
	}
	return out
}
