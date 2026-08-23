package deliver

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/bongo-go/bongo-go/internal/mail"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// antispamStage returns the antispam StageResult from stages, or nil if the
// message was never scanned (e.g. the antispam stage is disabled). Callers
// must use this rather than assuming a StageResult always exists: a message
// that was never scanned must not be stamped with a fabricated score.
func antispamStage(stages []*bongov1.StageResult) *bongov1.StageResult {
	for _, s := range stages {
		if s.Stage == "antispam" {
			return s
		}
	}
	return nil
}

// spamScoreFromStages extracts the spam score from the antispam stage result,
// returning 0.0 if no antispam stage was found. Thin wrapper over
// antispamStage kept for existing callers/tests.
func spamScoreFromStages(stages []*bongov1.StageResult) float32 {
	if s := antispamStage(stages); s != nil {
		return s.Score
	}
	return 0.0
}

// validAuthResultTokens is the RFC 7601 result token set accepted in the
// Authentication-Results header. Any value outside this set (including one
// crafted to inject a ';' and forge an extra method result) is dropped
// rather than interpolated verbatim.
var validAuthResultTokens = map[string]bool{
	"pass": true, "fail": true, "softfail": true, "neutral": true,
	"none": true, "temperror": true, "permerror": true,
}

// normalizeAuthResult lowercases v and returns it only if it is one of the
// RFC 7601 result tokens; otherwise it returns "" so the caller omits the
// method entirely instead of emitting an unsubstantiated or forged value.
func normalizeAuthResult(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if validAuthResultTokens[v] {
		return v
	}
	return ""
}

// domainPattern restricts the DKIM d= signing domain interpolated into
// Authentication-Results to characters valid in a DNS domain name. dkimDomain
// comes from the sender's DKIM-Signature d= tag and is attacker-controlled;
// CR/LF stripping alone is not enough because an injected ';' would forge an
// additional method result, so any value that does not match this pattern is
// dropped rather than sanitized-and-kept.
var domainPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// validAuthDomain reports whether d is safe to interpolate as a
// header.d= value.
func validAuthDomain(d string) bool {
	return len(d) > 0 && len(d) <= 253 && domainPattern.MatchString(d)
}

// buildAuthenticationResults constructs the Authentication-Results header
// line (including the "Authentication-Results:" field name, excluding the
// line terminator) recording the SPF result, the DKIM result and signing
// domain, and the DMARC result. It returns "" when hostname is empty: the
// authserv-id is the configured SMTP hostname, and a message must not be
// stamped with a blank authserv-id when it is unset (graceful degradation
// instead of a malformed header).
func buildAuthenticationResults(hostname, spfResult, dkimResult, dkimDomain, dmarcResult string) string {
	hostname = strings.TrimSpace(mail.SanitizeHeaderValue(hostname))
	if hostname == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("Authentication-Results: ")
	b.WriteString(hostname)

	if spf := normalizeAuthResult(spfResult); spf != "" {
		b.WriteString("; spf=")
		b.WriteString(spf)
	}

	if dkim := normalizeAuthResult(dkimResult); dkim != "" {
		b.WriteString("; dkim=")
		b.WriteString(dkim)
		if validAuthDomain(dkimDomain) {
			b.WriteString(" header.d=")
			b.WriteString(dkimDomain)
		}
	}

	if dmarc := normalizeAuthResult(dmarcResult); dmarc != "" {
		b.WriteString("; dmarc=")
		b.WriteString(dmarc)
	}

	return b.String()
}

// buildSpamHeaders constructs the X-Spam-Score, X-Spam-Status and (when
// reasons were recorded) X-Spam-Reasons header lines from the antispam
// StageResult. It returns nil when stage is nil: a message that was never
// scanned (antispam stage disabled) must not be stamped X-Spam-Score: 0.00,
// which would misreport it as having been scored and passed.
func buildSpamHeaders(stage *bongov1.StageResult, threshold float32) []string {
	if stage == nil {
		return nil
	}

	status := "No"
	if stage.Score >= threshold {
		status = "Yes"
	}

	hdrs := []string{
		"X-Spam-Score: " + strconv.FormatFloat(float64(stage.Score), 'f', 2, 32),
		"X-Spam-Status: " + status,
	}

	if len(stage.Reasons) > 0 {
		sanitized := make([]string, len(stage.Reasons))
		for i, r := range stage.Reasons {
			sanitized[i] = mail.SanitizeHeaderValue(r)
		}
		hdrs = append(hdrs, "X-Spam-Reasons: "+strings.Join(sanitized, ", "))
	}

	return hdrs
}

// maxHeaderLineLen bounds a single physical header line so no generated line
// approaches the RFC 5322 998-octet limit. Lines longer than this are folded
// using the message's own line ending plus a tab continuation.
const maxHeaderLineLen = 900

// foldHeaderLine folds line into multiple physical lines (joined by ending
// plus a tab) if it exceeds maxHeaderLineLen, breaking at the last space
// before the limit when one exists.
func foldHeaderLine(line, ending string) string {
	if len(line) <= maxHeaderLineLen {
		return line
	}
	var b strings.Builder
	remaining := line
	for len(remaining) > maxHeaderLineLen {
		cut := maxHeaderLineLen
		if idx := strings.LastIndexByte(remaining[:cut], ' '); idx > 0 {
			cut = idx
		}
		b.WriteString(remaining[:cut])
		b.WriteString(ending)
		b.WriteByte('\t')
		remaining = strings.TrimLeft(remaining[cut:], " ")
	}
	b.WriteString(remaining)
	return b.String()
}

// detectLineEnding reports the line ending used by raw's HEADER BLOCK.
//
// It compares the positions of the two candidate separators rather than
// testing for CRLF first: a header block using bare LF followed by a body
// that contains a CRLF blank line (common for MIME parts that survived an
// LF-normalising hop) would otherwise be misreported as CRLF, and the
// stamped headers would be terminated with the wrong sequence.
//
// A message with no blank-line separator at all is a bodyless message, which
// RFC 5322 permits — it is all header block. Returning ok=false for it would
// make stripTraceHeaders and prependHeaders no-op, letting a forged
// X-Spam-Status or Authentication-Results survive into storage on exactly the
// message shape an attacker controls. So the ending is inferred from the
// first line terminator instead, and ok stays true.
func detectLineEnding(raw []byte) (ending string, ok bool) {
	crlfSep := bytes.Index(raw, []byte("\r\n\r\n"))
	lfSep := bytes.Index(raw, []byte("\n\n"))
	switch {
	case crlfSep >= 0 && (lfSep < 0 || crlfSep <= lfSep):
		return "\r\n", true
	case lfSep >= 0:
		return "\n", true
	}

	// Bodyless: infer from the first terminator.
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		if i > 0 && raw[i-1] == '\r' {
			return "\r\n", true
		}
		return "\n", true
	}
	// A single header line with no terminator at all.
	return "\r\n", true
}

// isTraceHeaderName reports whether name is a header field this stage owns
// and therefore strips from inbound messages before stamping its own value:
// Authentication-Results (exact, case-insensitive) and any X-Spam-* field
// (prefix, case-insensitive).
func isTraceHeaderName(name string) bool {
	if strings.EqualFold(name, "Authentication-Results") {
		return true
	}
	return len(name) >= len("X-Spam-") && strings.EqualFold(name[:len("X-Spam-")], "X-Spam-")
}

// stripTraceHeaders removes any inbound Authentication-Results or X-Spam-*
// header field, including its folded continuation lines, from raw's header
// block: without this, a remote sender could include a forged
// "X-Spam-Status: No" or "Authentication-Results: ...dkim=pass" and have it
// survive into the stored message. The body (everything at and after the
// first blank line) is never touched, so a header-looking string inside the
// message body is left alone. Stripping is unconditional and runs after our
// own DKIM verification of the original bytes: a signature whose h= tag
// covers a stripped header will no longer verify on the stored copy, but the
// arrival-time verdict is already durable on MailMeta.DkimResult/DkimDomain
// and relayed/forwarded copies use the untouched original blob (see
// deliver.go), so nothing that depends on stored-message DKIM verification
// is affected today.
//
// If raw has no parseable header block (no blank-line separator), it is
// returned unchanged rather than risk corrupting it.
func stripTraceHeaders(raw []byte) []byte {
	if _, ok := detectLineEnding(raw); !ok {
		return raw
	}

	lines := bytes.Split(raw, []byte("\n"))
	kept := make([][]byte, 0, len(lines))
	inHeaders := true
	skipping := false
	for _, line := range lines {
		if !inHeaders {
			kept = append(kept, line)
			continue
		}
		trimmed := bytes.TrimRight(line, "\r")
		if len(trimmed) == 0 {
			// Blank line: end of header block.
			inHeaders = false
			skipping = false
			kept = append(kept, line)
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			// Folded continuation of the previous header.
			if skipping {
				continue
			}
			kept = append(kept, line)
			continue
		}
		name := trimmed
		if idx := bytes.IndexByte(trimmed, ':'); idx > 0 {
			name = trimmed[:idx]
		}
		if isTraceHeaderName(string(bytes.TrimSpace(name))) {
			skipping = true
			continue
		}
		skipping = false
		kept = append(kept, line)
	}
	return bytes.Join(kept, []byte("\n"))
}

// prependHeaders inserts hdrs (each a logical header line such as
// "X-Spam-Score: 0.62", without a line terminator) at the absolute top of
// raw, ahead of every existing header, so a DKIM signature computed
// over the original header set stays valid. The line ending is auto-detected
// from raw's own header block (CRLF if present, else LF) rather than
// hardcoded, and long lines are folded to stay well under the 998-octet
// limit. If raw has no parseable header block, it is returned unchanged.
func prependHeaders(raw []byte, hdrs []string) []byte {
	if len(hdrs) == 0 {
		return raw
	}
	ending, ok := detectLineEnding(raw)
	if !ok {
		return raw
	}

	var buf bytes.Buffer
	for _, h := range hdrs {
		buf.WriteString(foldHeaderLine(h, ending))
		buf.WriteString(ending)
	}
	buf.Write(raw)
	return buf.Bytes()
}
