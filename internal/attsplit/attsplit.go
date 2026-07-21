// Package attsplit is the task 9.1 spike for attachment deduplication. It splits
// a raw RFC822 message into a skeleton plus the exact ENCODED byte spans of its
// large attachment parts, and reconstructs the original message byte-for-byte.
//
// The design goal is byte-exact round-trip (so DKIM signatures survive a storage
// round-trip) while extracting attachment payloads that can be content-addressed
// and deduplicated. The key idea: never decode/re-encode. We locate each
// attachment part's encoded body as a byte range in the original raw and cut the
// message into an ordered sequence of literal spans and attachment references
// that exactly tile the original. Reconstruction concatenates them, so
// byte-exactness holds by construction.
package attsplit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/textproto"
	"sort"
	"strings"
)

// Part is an extracted attachment: the exact encoded bytes as they appear in the
// message, content-addressed by SHA-256 for deduplication.
type Part struct {
	Hash        string
	ContentType string
	Filename    string
	Encoded     []byte
	Size        int
}

// piece is one segment of the reconstruction sequence: either literal bytes or a
// reference to Parts[att].
type piece struct {
	literal []byte
	att     int // index into Skeleton.Parts, or -1 for a literal span
}

// Skeleton is the split form: an ordered sequence of pieces plus the extracted
// parts. Reconstruct() returns the original raw.
type Skeleton struct {
	pieces []piece
	Parts  []Part
}

// Reconstruct returns the original raw message bytes.
func (s *Skeleton) Reconstruct() []byte {
	var buf bytes.Buffer
	for _, p := range s.pieces {
		if p.att >= 0 {
			buf.Write(s.Parts[p.att].Encoded)
		} else {
			buf.Write(p.literal)
		}
	}
	return buf.Bytes()
}

// Segment describes one reconstruction step, proto-agnostic. When Attachment is
// false it is a literal of Length bytes consumed from the skeleton; when true it
// splices Parts[AttIndex].
type Segment struct {
	Attachment bool
	Length     int
	AttIndex   int
}

// Layout returns the skeleton (the concatenated literal spans) and the ordered
// segments that, together with Parts, reconstruct the original message.
func (s *Skeleton) Layout() (skeleton []byte, segments []Segment) {
	var buf bytes.Buffer
	for _, p := range s.pieces {
		if p.att >= 0 {
			segments = append(segments, Segment{Attachment: true, AttIndex: p.att})
		} else {
			buf.Write(p.literal)
			segments = append(segments, Segment{Length: len(p.literal)})
		}
	}
	return buf.Bytes(), segments
}

// SkeletonSize is the number of literal (non-attachment) bytes retained.
func (s *Skeleton) SkeletonSize() int {
	n := 0
	for _, p := range s.pieces {
		if p.att < 0 {
			n += len(p.literal)
		}
	}
	return n
}

type span struct {
	start, end int
	ct, name   string
}

// Split extracts attachment parts whose encoded body is >= minSize.
func Split(raw []byte, minSize int) *Skeleton {
	var spans []span
	if bodyStart, hdr := topHeaders(raw); bodyStart >= 0 {
		if mt, params := parseContentType(hdr); strings.HasPrefix(mt, "multipart/") && params["boundary"] != "" {
			collect(raw, bodyStart, len(raw), params["boundary"], minSize, &spans)
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	sk := &Skeleton{}
	pos := 0
	for _, sp := range spans {
		if sp.start < pos { // overlap guard (shouldn't happen with well-formed MIME)
			continue
		}
		if sp.start > pos {
			sk.pieces = append(sk.pieces, piece{literal: raw[pos:sp.start], att: -1})
		}
		enc := raw[sp.start:sp.end]
		sum := sha256.Sum256(enc)
		idx := len(sk.Parts)
		sk.Parts = append(sk.Parts, Part{
			Hash:        hex.EncodeToString(sum[:]),
			ContentType: sp.ct,
			Filename:    sp.name,
			Encoded:     enc,
			Size:        len(enc),
		})
		sk.pieces = append(sk.pieces, piece{att: idx})
		pos = sp.end
	}
	if pos < len(raw) {
		sk.pieces = append(sk.pieces, piece{literal: raw[pos:], att: -1})
	}
	if len(sk.pieces) == 0 {
		sk.pieces = append(sk.pieces, piece{literal: raw, att: -1})
	}
	return sk
}

// collect recursively finds attachment body ranges within a multipart region
// [bs,be) delimited by boundary, appending them to out.
func collect(raw []byte, bs, be int, boundary string, minSize int, out *[]span) {
	dashB := []byte("--" + boundary)

	// Find boundary delimiter lines (occurrences of "--boundary" at line start).
	var delims []int
	for i := bs; i < be; {
		j := bytes.Index(raw[i:be], dashB)
		if j < 0 {
			break
		}
		p := i + j
		if p == bs || raw[p-1] == '\n' {
			delims = append(delims, p)
		}
		nl := bytes.IndexByte(raw[p:be], '\n')
		if nl < 0 {
			break
		}
		i = p + nl + 1
	}

	for k := 0; k+1 < len(delims); k++ {
		nl := bytes.IndexByte(raw[delims[k]:be], '\n')
		if nl < 0 {
			continue
		}
		partStart := delims[k] + nl + 1
		bodyEnd := delims[k+1]
		// The CRLF (or LF) immediately before the next delimiter belongs to the
		// delimiter, not the part body.
		if bodyEnd-2 >= partStart && raw[bodyEnd-2] == '\r' && raw[bodyEnd-1] == '\n' {
			bodyEnd -= 2
		} else if bodyEnd-1 >= partStart && raw[bodyEnd-1] == '\n' {
			bodyEnd--
		}
		if partStart >= bodyEnd {
			continue
		}
		hi := bytes.Index(raw[partStart:bodyEnd], []byte("\r\n\r\n"))
		if hi < 0 {
			continue
		}
		partBodyStart := partStart + hi + 4
		mt, params, disp, dispParams := parsePartHeaders(raw[partStart:partBodyStart])

		if strings.HasPrefix(mt, "multipart/") && params["boundary"] != "" {
			collect(raw, partBodyStart, bodyEnd, params["boundary"], minSize, out)
			continue
		}
		name := dispParams["filename"]
		if name == "" {
			name = params["name"]
		}
		isAttachment := strings.EqualFold(disp, "attachment") || name != ""
		if isAttachment && bodyEnd-partBodyStart >= minSize {
			*out = append(*out, span{start: partBodyStart, end: bodyEnd, ct: mt, name: name})
		}
	}
}

func topHeaders(raw []byte) (int, []byte) {
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	if i < 0 {
		return -1, nil
	}
	return i + 4, raw[:i+4]
}

func parseHeader(hdr []byte) textproto.MIMEHeader {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(hdr)))
	h, err := r.ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return textproto.MIMEHeader{}
	}
	return h
}

func parseContentType(hdr []byte) (string, map[string]string) {
	mt, params, err := mime.ParseMediaType(parseHeader(hdr).Get("Content-Type"))
	if err != nil {
		return "", map[string]string{}
	}
	return mt, params
}

func parsePartHeaders(hdr []byte) (mt string, params map[string]string, disp string, dispParams map[string]string) {
	h := parseHeader(hdr)
	mt, params, _ = mime.ParseMediaType(h.Get("Content-Type"))
	if params == nil {
		params = map[string]string{}
	}
	disp, dispParams, _ = mime.ParseMediaType(h.Get("Content-Disposition"))
	if dispParams == nil {
		dispParams = map[string]string{}
	}
	return mt, params, disp, dispParams
}
