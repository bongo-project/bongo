package rules

import (
	"slices"
	"strings"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/mimesafe"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// MaxRuleMIMEParts bounds how many MIME tree nodes ParseMessage visits when
// collecting content types, containers included. Parts beyond it are not
// inspected, so a mime_type condition cannot match them.
const MaxRuleMIMEParts = 100

// maxRuleBodyText bounds the body text a body condition sees.
const maxRuleBodyText = 8192

// Message is a parsed message as the rule engine sees it. Delivery and the
// rule tester both build it with ParseMessage, so they cannot disagree.
type Message struct {
	Meta *bongov1.MailMeta
	// Headers maps lowercased header names to the first value present.
	Headers  map[string]string
	BodyText string
	// MIMETypes holds the lowercased type/subtype of the leaf parts, without
	// duplicates. It is empty when the message could not be parsed or was
	// parsed in degraded mode.
	MIMETypes []string
}

// ParseMessage extracts the fields rule conditions use from raw message bytes.
// It uses enmime for full MIME parsing with a plain-text header fallback. It
// never fails: a message that cannot be parsed yields fewer fields, so
// conditions on them are false.
func ParseMessage(raw []byte) *Message {
	meta := &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Size: int64(len(raw))},
	}

	env, degraded, err := mimesafe.ReadEnvelope(raw)
	if err != nil || env == nil {
		var headers map[string]string
		meta.Subject, headers = extractBasicHeaders(raw)
		return &Message{Meta: meta, Headers: headers}
	}

	meta.From = env.GetHeader("From")
	meta.Subject = env.GetHeader("Subject")
	meta.To = splitAddresses(env.GetHeader("To"))
	meta.Cc = splitAddresses(env.GetHeader("Cc"))

	// Collect all headers for header:Name conditions. GetHeader returns the
	// first value and keys are lowercased, matching extractBasicHeaders.
	headers := make(map[string]string)
	for _, key := range env.GetHeaderKeys() {
		if v := env.GetHeader(key); v != "" {
			headers[strings.ToLower(key)] = v
		}
	}

	bodyText := env.Text
	if len(bodyText) > maxRuleBodyText {
		bodyText = bodyText[:maxRuleBodyText]
	}

	// Record attachment count so has_attachment rules work.
	for range env.Attachments {
		meta.Attachments = append(meta.Attachments, &bongov1.BlobRef{})
	}

	msg := &Message{Meta: meta, Headers: headers, BodyText: bodyText}
	if !degraded {
		msg.MIMETypes = leafMIMETypes(env.Root)
	}
	return msg
}

// leafMIMETypes walks the part tree depth first and returns the distinct
// lowercased type/subtype of every leaf part, inline ones included. At most
// MaxRuleMIMEParts nodes are visited, containers counted. A message/rfc822
// part counts as a leaf: enmime does not descend into it. A part without a
// Content-Type is text/plain (RFC 2045).
func leafMIMETypes(root *enmime.Part) []string {
	var types []string
	visited := 0
	var walk func(p *enmime.Part)
	walk = func(p *enmime.Part) {
		for ; p != nil && visited < MaxRuleMIMEParts; p = p.NextSibling {
			visited++
			if p.FirstChild != nil {
				walk(p.FirstChild)
				continue
			}
			ct := p.ContentType
			if i := strings.IndexByte(ct, ';'); i >= 0 {
				ct = ct[:i]
			}
			ct = strings.ToLower(strings.TrimSpace(ct))
			if ct == "" {
				ct = "text/plain"
			}
			if !slices.Contains(types, ct) {
				types = append(types, ct)
			}
		}
	}
	walk(root)
	return types
}

// splitAddresses splits a comma-separated address list into individual entries.
func splitAddresses(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// extractBasicHeaders scans raw RFC 5322 bytes and returns the Subject and a
// map of lowercased header names to their values; the first value of a
// repeated header wins, as with enmime. Used as a fallback when enmime fails.
func extractBasicHeaders(raw []byte) (subject string, headers map[string]string) {
	headers = make(map[string]string)
	lines := strings.Split(string(raw), "\n")
	var currentKey, currentVal string

	flush := func() {
		if currentKey == "" {
			return
		}
		v := strings.TrimSpace(currentVal)
		key := strings.ToLower(currentKey)
		if _, seen := headers[key]; !seen {
			headers[key] = v
			if key == "subject" {
				subject = v
			}
		}
	}

	for _, line := range lines {
		if strings.TrimRight(line, "\r") == "" {
			break
		}
		// Folded header continuation.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			currentVal += " " + strings.TrimSpace(line)
			continue
		}
		flush()
		if idx := strings.IndexByte(line, ':'); idx > 0 {
			currentKey = strings.TrimSpace(line[:idx])
			currentVal = strings.TrimSpace(line[idx+1:])
		} else {
			currentKey = ""
			currentVal = ""
		}
	}
	flush()
	return subject, headers
}
