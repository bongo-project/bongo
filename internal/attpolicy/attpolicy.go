// Package attpolicy finds blocked attachments in a raw RFC 5322 message and
// removes them.
//
// Detection is by declared media type and by file name. Names come from the
// Content-Disposition filename and the Content-Type name parameters, including
// RFC 2231 continuations and extended values and RFC 2047 encoded words, and
// only the last extension counts, so invoice.pdf.exe is an exe and a.exe.pdf
// is a pdf. The walker reads LF as well as CRLF mail, inspects every leaf part
// whatever its disposition, and descends into message/rfc822 parts that are
// stored unencoded; one with a transfer encoding it does not know is held. It
// does not open archives, and it does not look inside base64 or
// quoted-printable message/rfc822, message/global, uuencoded, S/MIME or PGP
// content.
//
// Stripping replaces the whole blocked part with a short text/plain notice and
// copies every other byte of the message unchanged, the header block
// included. The notice is base64 encoded, so its text can never forge a MIME
// boundary or a header. The result is checked again with this walker and with
// the production MIME parser (mimesafe); anything either parser still finds,
// and anything over the structure limits, is reported as a quarantine instead
// of a rewrite.
//
// The package is pure: no I/O, no clock, no randomness, so a rewrite is
// deterministic and a replay after a crash produces the same bytes.
package attpolicy

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/bongo-project/bongo/internal/mimesafe"
)

// DefaultExtensions and DefaultMimeTypes are the values the admin page offers
// when a domain has no policy yet.
var (
	DefaultExtensions = []string{"exe", "scr", "js", "vbs", "bat", "cmd", "com", "jar", "ps1", "msi"}
	DefaultMimeTypes  = []string{
		"application/x-msdownload", "application/x-dosexec", "application/x-msdos-program",
		"application/x-ms-installer", "application/java-archive",
	}
)

// Reasons a message is quarantined instead of stripped. They are fixed
// strings: they are stored in stage results and shown to admins.
const (
	ReasonBlocked   = "blocked_attachment"
	ReasonComplex   = "mime_too_complex"
	ReasonMismatch  = "parser_mismatch"
	ReasonUnchecked = "rewrite_not_verified"
)

// maxNameRunes caps the file name shown in a notice.
const maxNameRunes = 255

// Policy is a set of blocked extensions and media types.
type Policy struct {
	exts  map[string]struct{}
	types map[string]struct{}
}

// NewPolicy builds a Policy. Entries are lower-cased; extensions lose one
// leading dot.
func NewPolicy(exts, types []string) *Policy {
	p := &Policy{exts: map[string]struct{}{}, types: map[string]struct{}{}}
	for _, e := range exts {
		e = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), ".")
		if e != "" {
			p.exts[e] = struct{}{}
		}
	}
	for _, m := range types {
		m = strings.ToLower(strings.TrimSpace(m))
		if m != "" {
			p.types[m] = struct{}{}
		}
	}
	return p
}

// Empty reports whether the policy blocks nothing.
func (p *Policy) Empty() bool { return p == nil || (len(p.exts) == 0 && len(p.types) == 0) }

// Result is the outcome of Evaluate.
type Result struct {
	// Rules are the distinct matched rules: ".ext" or a media type, sorted.
	Rules []string
	// Parts is the number of blocked parts found.
	Parts int
	// Out is the rewritten message when blocked parts were stripped; nil when
	// nothing was found or the message is to be quarantined.
	Out []byte
	// Quarantine asks for the message to be held; Reason says why.
	Quarantine bool
	Reason     string
}

// NoticeFunc returns the text that replaces a stripped part. name is the
// sanitised file name, or the media type when the part has no name.
type NoticeFunc func(name, rule string) string

// Evaluate checks raw against p. With strip false a blocked part quarantines
// the message; with strip true it is replaced by a notice. An empty policy
// passes everything untouched.
func Evaluate(raw []byte, p *Policy, strip bool, notice NoticeFunc) Result {
	if p.Empty() {
		return Result{}
	}
	w := &walker{raw: raw, pol: p, eol: detectEOL(raw)}
	w.entity(0, len(raw), 1, true)
	if w.complex {
		return Result{Quarantine: true, Reason: ReasonComplex}
	}
	if w.ambiguous {
		return Result{Quarantine: true, Reason: ReasonMismatch}
	}
	res := Result{Parts: len(w.spans), Rules: w.rules()}
	if len(w.spans) == 0 {
		// The walker saw nothing. Ask the production parser as well: a
		// message structured in a way only a lenient reader follows must not
		// pass.
		if p.parserFinds(raw, w.structured) {
			return Result{Quarantine: true, Reason: ReasonMismatch}
		}
		return Result{}
	}
	if !strip {
		res.Quarantine, res.Reason = true, ReasonBlocked
		return res
	}
	out := w.rewrite(notice)
	// The rewrite must hold up under both parsers.
	w2 := &walker{raw: out, pol: p, eol: w.eol}
	w2.entity(0, len(out), 1, true)
	if w2.complex || w2.ambiguous || len(w2.spans) > 0 || p.parserFinds(out, w2.structured) {
		res.Quarantine, res.Reason = true, ReasonUnchecked
		return res
	}
	res.Out = out
	return res
}

func detectEOL(raw []byte) string {
	if i := bytes.IndexByte(raw, '\n'); i > 0 && raw[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// span is one blocked entity to replace.
type span struct {
	start, end int
	// message is true for a whole message (the top level or an unencoded
	// message/rfc822 body): its header block survives, only the content
	// headers and the body change.
	message bool
	hdrEnd  int      // message: end of the header block
	drop    [][2]int // message: byte ranges of content header fields to remove
	hasMIME bool     // message: a MIME-Version field exists
	name    string
	rule    string
}

type walker struct {
	raw     []byte
	pol     *Policy
	eol     string
	spans   []span
	parts   int
	complex bool
	// ambiguous is set when an entity's structure is stated twice or cannot
	// be read: parsers disagree about which statement wins.
	ambiguous bool
	// structured is set when the walker followed a multipart or message
	// structure.
	structured bool
	hdrBytes   int
}

func (w *walker) rules() []string {
	set := map[string]struct{}{}
	for _, s := range w.spans {
		set[s.rule] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// field is one header field with its byte range (continuation lines included).
type field struct {
	name, value string
	start, end  int
	valueAt     int
}

// splitHeaders finds the end of the header block of the entity in [start,end)
// and where its body starts. The blank line (CRLF or LF) lies between them.
func splitHeaders(raw []byte, start, end int) (hdrEnd, bodyStart int) {
	for pos := start; pos < end; {
		nl := bytes.IndexByte(raw[pos:end], '\n')
		next := end
		line := raw[pos:end]
		if nl >= 0 {
			next = pos + nl + 1
			line = raw[pos : pos+nl]
		}
		if len(bytes.TrimSuffix(line, []byte("\r"))) == 0 {
			return pos, next
		}
		pos = next
	}
	return end, end
}

// parseFields reads the header lines in [start,end). A line without a colon is
// skipped, so a malformed line cannot hide the fields after it. Each value is
// built once from its line range, so the cost is linear in the header size.
func parseFields(raw []byte, start, end int) []field {
	var out []field
	cur := -1
	for pos := start; pos < end; {
		nl := bytes.IndexByte(raw[pos:end], '\n')
		next := end
		line := raw[pos:end]
		if nl >= 0 {
			next = pos + nl + 1
			line = raw[pos : pos+nl]
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && cur >= 0:
			out[cur].end = next
		default:
			cur = -1
			if i := bytes.IndexByte(line, ':'); i > 0 {
				name := strings.ToLower(strings.TrimSpace(string(line[:i])))
				if name != "" {
					out = append(out, field{name: name, start: pos, end: next, valueAt: pos + i + 1})
					cur = len(out) - 1
				}
			}
		}
		pos = next
	}
	for i := range out {
		out[i].value = foldedValue(raw, out[i].valueAt, out[i].end)
	}
	return out
}

// foldedValue unfolds the header value in raw[from:end): each line trimmed and
// joined by one space.
func foldedValue(raw []byte, from, end int) string {
	var b strings.Builder
	first := true
	for pos := from; pos < end; {
		nl := bytes.IndexByte(raw[pos:end], '\n')
		next := end
		line := raw[pos:end]
		if nl >= 0 {
			next = pos + nl + 1
			line = raw[pos : pos+nl]
		}
		line = bytes.TrimSpace(line)
		if first {
			first = false
		} else {
			b.WriteByte(' ')
		}
		b.Write(line)
		pos = next
	}
	return strings.TrimSpace(b.String())
}

func values(fs []field, name string) []string {
	var out []string
	for _, f := range fs {
		if f.name == name {
			out = append(out, f.value)
		}
	}
	return out
}

// Header limits: one entity's header block, and all header blocks of a
// message together. A message over either is held, not parsed further.
const (
	maxEntityHeaderBytes = 1 << 20
	maxTotalHeaderBytes  = 4 << 20
)

// maxParams caps the ';'-separated segments of one Content-Type or
// Content-Disposition value. Real mail has a handful; a long file name split
// into RFC 2231 continuations needs a few dozen.
const maxParams = 256

// entity examines the entity occupying [start,end).
func (w *walker) entity(start, end, depth int, message bool) {
	if w.complex {
		return
	}
	if depth > mimesafe.MaxDepth {
		w.complex = true
		return
	}
	if w.parts++; w.parts > mimesafe.MaxParts {
		w.complex = true
		return
	}
	hdrEnd, bodyStart := splitHeaders(w.raw, start, end)
	if w.hdrBytes += hdrEnd - start; hdrEnd-start > maxEntityHeaderBytes || w.hdrBytes > maxTotalHeaderBytes {
		w.complex = true
		return
	}
	fs := parseFields(w.raw, start, hdrEnd)

	cts, cds := values(fs, "content-type"), values(fs, "content-disposition")
	for _, v := range slices.Concat(cts, cds) {
		if strings.Count(v, ";") > maxParams {
			w.complex = true
			return
		}
	}
	if rule, name, ok := w.pol.check(cts, cds); ok {
		s := span{start: start, end: end, name: name, rule: rule}
		if message {
			s.message, s.hdrEnd = true, hdrEnd
			for _, f := range fs {
				switch f.name {
				case "content-type", "content-transfer-encoding", "content-disposition", "content-description":
					s.drop = append(s.drop, [2]int{f.start, f.end})
				case "mime-version":
					s.hasMIME = true
				}
			}
		}
		w.spans = append(w.spans, s)
		return
	}

	ctes := values(fs, "content-transfer-encoding")
	if len(cts) > 1 || len(cds) > 1 || len(ctes) > 1 {
		w.ambiguous = true
		return
	}
	var mt, ctValue string
	var params []param
	if len(cts) > 0 {
		ctValue = cts[0]
		mt, params = splitParams(ctValue)
	}
	switch {
	case strings.HasPrefix(mt, "multipart/"):
		if _, _, err := mime.ParseMediaType(ctValue); err != nil {
			w.ambiguous = true
			return
		}
		bs := paramValues(params, "boundary")
		if len(bs) != 1 || bs[0] == "" {
			w.ambiguous = true
			return
		}
		w.structured = true
		w.multipart(bodyStart, end, bs[0], depth)
	case mt == "message/rfc822":
		cte := ""
		if len(ctes) > 0 {
			cte = strings.ToLower(strings.TrimSpace(stripComments(ctes[0])))
		}
		switch cte {
		case "", "7bit", "8bit", "binary":
			w.structured = true
			w.entity(bodyStart, end, depth+1, true)
		case "base64", "quoted-printable":
			// An encoded message is not opened.
		default:
			// Readers disagree about what an unknown encoding means for a
			// message part, and the production parser does not descend.
			w.ambiguous = true
		}
	}
}

// multipart walks the parts of the multipart body in [bs,be).
func (w *walker) multipart(bs, be int, boundary string, depth int) {
	type delim struct {
		line, next int
		closing    bool
	}
	var ds []delim
	prefix := []byte("--" + boundary)
	for pos := bs; pos < be; {
		nl := bytes.IndexByte(w.raw[pos:be], '\n')
		next := be
		line := w.raw[pos:be]
		if nl >= 0 {
			next = pos + nl + 1
			line = w.raw[pos : pos+nl]
		}
		if bytes.HasPrefix(line, prefix) {
			rest := bytes.Trim(line[len(prefix):], " \t\r")
			switch {
			case len(rest) == 0:
				ds = append(ds, delim{pos, next, false})
			case bytes.Equal(rest, []byte("--")):
				ds = append(ds, delim{pos, next, true})
			}
			if len(ds) > mimesafe.MaxParts {
				w.complex = true
				return
			}
		}
		pos = next
	}
	for k, d := range ds {
		if d.closing {
			return
		}
		pe := be
		if k+1 < len(ds) {
			pe = ds[k+1].line
			// The line break before a delimiter belongs to the delimiter.
			if pe-2 >= d.next && w.raw[pe-2] == '\r' && w.raw[pe-1] == '\n' {
				pe -= 2
			} else if pe-1 >= d.next && w.raw[pe-1] == '\n' {
				pe--
			}
		}
		if d.next > pe {
			continue
		}
		w.entity(d.next, pe, depth+1, false)
		if w.complex {
			return
		}
	}
}

// rewrite builds the output: every byte outside the blocked entities is copied
// as it is.
func (w *walker) rewrite(notice NoticeFunc) []byte {
	spans := slices.Clone(w.spans)
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out bytes.Buffer
	pos := 0
	for _, s := range spans {
		if s.start < pos {
			continue
		}
		out.Write(w.raw[pos:s.start])
		body := w.noticeBody(notice, s)
		if s.message {
			out.Write(w.messageHead(s))
			out.WriteString("Content-Type: text/plain; charset=utf-8" + w.eol +
				"Content-Disposition: inline" + w.eol +
				"Content-Transfer-Encoding: base64" + w.eol + w.eol)
			out.WriteString(body)
			if s.end == len(w.raw) {
				out.WriteString(w.eol)
			}
		} else {
			out.WriteString("Content-Type: text/plain; charset=utf-8" + w.eol +
				"Content-Disposition: inline" + w.eol +
				"Content-Transfer-Encoding: base64" + w.eol + w.eol)
			out.WriteString(body)
		}
		pos = s.end
	}
	out.Write(w.raw[pos:])
	return out.Bytes()
}

// messageHead returns the header block of a blocked whole message without its
// content fields, ending in a line break, with MIME-Version present.
func (w *walker) messageHead(s span) []byte {
	var b bytes.Buffer
	pos := s.start
	for _, d := range s.drop {
		b.Write(w.raw[pos:d[0]])
		pos = d[1]
	}
	b.Write(w.raw[pos:s.hdrEnd])
	if b.Len() > 0 && b.Bytes()[b.Len()-1] != '\n' {
		b.WriteString(w.eol)
	}
	if !s.hasMIME {
		b.WriteString("MIME-Version: 1.0" + w.eol)
	}
	return b.Bytes()
}

func (w *walker) noticeBody(notice NoticeFunc, s span) string {
	var text string
	if notice != nil {
		text = notice(s.name, s.rule)
	} else {
		text = "The attachment \"" + s.name + "\" was removed by the attachment policy (blocked: " + s.rule + ")."
	}
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + w.eol)
		enc = enc[76:]
	}
	b.WriteString(enc)
	return b.String()
}

// check reports whether a part with these Content-Type and Content-Disposition
// field values is blocked, with the matched rule and a display name.
func (p *Policy) check(cts, cds []string) (rule, name string, ok bool) {
	var names []string
	var types []string
	for _, v := range cts {
		mt, params := splitParams(v)
		types = append(types, mt)
		names = append(names, paramNames(params)...)
	}
	for _, v := range cds {
		_, params := splitParams(v)
		names = append(names, paramNames(params)...)
	}
	display := ""
	for _, n := range names {
		if display == "" {
			display = displayName(n)
		}
		for _, ext := range extensionsOf(n) {
			if _, hit := p.exts[ext]; hit {
				return "." + ext, orType(display, types), true
			}
		}
	}
	for _, mt := range types {
		if _, hit := p.types[mt]; hit {
			return mt, orType(display, types), true
		}
	}
	return "", "", false
}

func orType(display string, types []string) string {
	if display != "" {
		return display
	}
	for _, t := range types {
		if t != "" {
			return t
		}
	}
	return ""
}

// param is one parameter, RFC 2231 markers already split off the key.
type param struct {
	key string
	idx int  // continuation index, -1 when none
	enc bool // percent-encoded (key ended in *)
	val string
}

// splitParams splits a structured header value into its lower-cased media
// type (or disposition) and its parameters. It never fails: a malformed
// parameter yields what can be read, which is the fail-closed direction. The
// value is read in one forward pass, so the cost is linear in its length
// whatever the parameters look like.
func splitParams(v string) (mt string, params []param) {
	v = stripComments(v)
	head, rest, _ := strings.Cut(v, ";")
	mt = strings.Join(strings.Fields(strings.ToLower(head)), "")
	i := 0
	for i < len(rest) {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == ';') {
			i++
		}
		if i == len(rest) {
			break
		}
		// The key runs to '='; a ';' first means a segment without a value,
		// which is skipped.
		k := i
		for i < len(rest) && rest[i] != '=' && rest[i] != ';' {
			i++
		}
		if i == len(rest) || rest[i] == ';' {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(rest[k:i]))
		i++
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		var val string
		if i < len(rest) && rest[i] == '"' {
			var b strings.Builder
			for i++; i < len(rest) && rest[i] != '"'; i++ {
				if rest[i] == '\\' && i+1 < len(rest) {
					i++
				}
				b.WriteByte(rest[i])
			}
			val = b.String()
			if i < len(rest) {
				i++
			}
		} else {
			s := i
			for i < len(rest) && rest[i] != ';' {
				i++
			}
			val = strings.TrimSpace(rest[s:i])
		}
		params = append(params, newParam(key, val))
	}
	return mt, params
}

// stripComments removes RFC 5322 parenthesised comments outside quoted
// strings. An unterminated comment is left alone: dropping the rest of the
// value would hide a file name.
func stripComments(v string) string {
	if !strings.Contains(v, "(") {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case '"':
			j := i + 1
			for j < len(v) && v[j] != '"' {
				if v[j] == '\\' && j+1 < len(v) {
					j++
				}
				j++
			}
			if j >= len(v) {
				j = len(v) - 1
			}
			b.WriteString(v[i : j+1])
			i = j
		case '(':
			depth, j := 0, i
			for ; j < len(v); j++ {
				if v[j] == '\\' && j+1 < len(v) {
					j++
				} else if v[j] == '(' {
					depth++
				} else if v[j] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if depth != 0 {
				return v
			}
			b.WriteByte(' ')
			i = j
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func newParam(key, val string) param {
	p := param{key: key, idx: -1, val: val}
	if strings.HasSuffix(p.key, "*") {
		p.enc = true
		p.key = strings.TrimSuffix(p.key, "*")
	}
	if i := strings.LastIndexByte(p.key, '*'); i >= 0 {
		if n, err := strconv.Atoi(p.key[i+1:]); err == nil && n >= 0 {
			p.idx = n
			p.key = p.key[:i]
		}
	}
	return p
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(_ string, r io.Reader) (io.Reader, error) {
	// Only the ASCII extension matters for matching, and every charset a mail
	// client sends keeps ASCII as ASCII, so the bytes are read as they are.
	return r, nil
}}

// paramNames returns every file name the parameters carry: the filename and
// name parameters, RFC 2231 continuations joined, extended values decoded, and
// each of those with RFC 2047 encoded words decoded as well.
func paramNames(params []param) []string {
	out := paramValues(params, "filename", "name")
	for _, n := range slices.Clone(out) {
		if d, err := wordDecoder.DecodeHeader(n); err == nil && d != n {
			out = append(out, d)
		}
	}
	return out
}

// paramValues returns the values of the named parameters, RFC 2231
// continuations joined and extended values decoded: one entry per plain
// parameter and one per continued group.
func paramValues(params []param, keys ...string) []string {
	type seg struct {
		idx int
		val string
	}
	groups := map[string][]seg{}
	var order []string
	var out []string
	for _, p := range params {
		if !slices.Contains(keys, p.key) {
			continue
		}
		v := p.val
		if p.enc {
			if p.idx <= 0 {
				// charset'language'value
				if parts := strings.SplitN(v, "'", 3); len(parts) == 3 {
					v = parts[2]
				}
			}
			if d, err := url.PathUnescape(v); err == nil {
				v = d
			} else {
				v = lenientUnescape(v)
			}
		}
		if p.idx < 0 {
			out = append(out, v)
			continue
		}
		k := p.key
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], seg{p.idx, v})
	}
	for _, k := range order {
		segs := groups[k]
		sort.SliceStable(segs, func(i, j int) bool { return segs[i].idx < segs[j].idx })
		var b strings.Builder
		for _, s := range segs {
			b.WriteString(s.val)
		}
		out = append(out, b.String())
	}
	return out
}

// lenientUnescape decodes the valid %XX sequences and keeps the rest.
func lenientUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// hidden reports whether r is dropped before names are compared or shown:
// control characters, bidirectional overrides and zero-width characters.
func hidden(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069, r == 0x061c, r == 0xfeff, r == 0x00ad, r == 0xfffd:
		return true
	}
	return false
}

func stripHidden(s string) string {
	return strings.Map(func(r rune) rune {
		if hidden(r) {
			return -1
		}
		return r
	}, s)
}

// extensionsOf returns the extensions a file name can be taken to have: of the
// whole name, of the name cut at the first NUL (C programs stop there) and of
// the name cut at the first colon (an NTFS stream suffix). Only the last
// extension of each reading counts.
func extensionsOf(name string) []string {
	variants := []string{name}
	if i := strings.IndexByte(name, 0); i >= 0 {
		variants = append(variants, name[:i])
	}
	var out []string
	for _, v := range variants {
		v = norm.NFKC.String(strings.ToValidUTF8(stripHidden(v), ""))
		v = strings.ToLower(v)
		if i := strings.LastIndexAny(v, `/\`); i >= 0 {
			v = v[i+1:]
		}
		for _, cand := range []string{v, cutAt(v, ':')} {
			cand = strings.TrimRightFunc(cand, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
			if i := strings.LastIndexByte(cand, '.'); i >= 0 && i+1 < len(cand) {
				if e := cand[i+1:]; !slices.Contains(out, e) {
					out = append(out, e)
				}
			}
		}
	}
	return out
}

func cutAt(s string, c byte) string {
	if i := strings.IndexByte(s, c); i >= 0 {
		return s[:i]
	}
	return s
}

// displayName is the file name as shown in a notice: no path, no hidden
// characters, valid UTF-8, at most 255 runes.
func displayName(name string) string {
	name = strings.ToValidUTF8(stripHidden(name), "")
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if utf8.RuneCountInString(name) > maxNameRunes {
		name = string([]rune(name)[:maxNameRunes])
	}
	return strings.TrimSpace(name)
}

// parserFinds reports whether the production MIME parser sees a blocked part
// in raw, or cannot be trusted to have shown everything.
func (p *Policy) parserFinds(raw []byte, structured bool) bool {
	env, degraded, err := mimesafe.ReadEnvelope(raw)
	if degraded {
		return true
	}
	if err != nil || env == nil || env.Root == nil {
		// Nothing readable as MIME. That is only safe when the walker saw no
		// structure either; otherwise a reader that copes with it might show
		// a part this parser could not.
		return structured
	}
	for part := env.Root; part != nil; {
		var cts, cds []string
		cts = append(cts, part.Header.Values("Content-Type")...)
		cds = append(cds, part.Header.Values("Content-Disposition")...)
		cts = append(cts, part.ContentType)
		if _, _, hit := p.check(cts, cds); hit {
			return true
		}
		if part.FileName != "" {
			for _, e := range extensionsOf(part.FileName) {
				if _, hit := p.exts[e]; hit {
					return true
				}
			}
		}
		switch {
		case part.FirstChild != nil:
			part = part.FirstChild
		default:
			for part != nil && part.NextSibling == nil {
				part = part.Parent
			}
			if part != nil {
				part = part.NextSibling
			}
		}
	}
	return false
}
