// Package unsubscribe reads the List-Unsubscribe (RFC 2369) and
// List-Unsubscribe-Post (RFC 8058) headers of a stored message and decides
// which unsubscribe methods the message offers.
//
// Everything here is judged from header text the sender controls. Parse only
// classifies; whether the server may act on a one-click offer is decided by
// VerifyOneClick (a DKIM check) and by the safe HTTP client in internal/netx.
package unsubscribe

import (
	"bytes"
	"context"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-msgauth/dkim"
)

// Limits on what a sender can make the parser look at.
const (
	// maxHeaderBytes is the longest header value read. A longer one is ignored
	// whole: no real list puts more than a few URIs in it.
	maxHeaderBytes = 2048
	// maxEntries is the number of bracketed URIs read from one header.
	maxEntries = 8
	// maxVerifications caps DKIM signatures checked, as the antispam stage does.
	maxVerifications = 5
	// dnsTimeout bounds every DKIM key lookup of one verification.
	dnsTimeout = 5 * time.Second
)

// OneClickValue is the only List-Unsubscribe-Post value that offers one-click.
const OneClickValue = "List-Unsubscribe=One-Click"

// Options is what a message offers.
type Options struct {
	// HTTPS is the first usable https URI, or nil. It has no user information
	// and a host, and its port is empty, 443 or 8443.
	HTTPS *url.URL
	// Mailto is the first mailto: URI as written, or "".
	Mailto string
	// OneClick is true when HTTPS is set, there is exactly one
	// List-Unsubscribe header and exactly one List-Unsubscribe-Post header,
	// and the latter says List-Unsubscribe=One-Click. It does not say the
	// signature is valid: that is VerifyOneClick, on click.
	OneClick bool
}

// Any reports whether the message offers any method.
func (o Options) Any() bool { return o.HTTPS != nil || o.Mailto != "" }

// Parse reads the values of every List-Unsubscribe header and every
// List-Unsubscribe-Post header of a message, in order.
//
// The one-click offer needs exactly one of each header. DKIM picks header
// instances from the bottom up, so with two headers an attacker could put an
// unsigned one above the signed one and the signature would still verify.
func Parse(unsub, post []string) Options {
	var o Options
	for _, v := range unsub {
		for _, entry := range entries(v) {
			switch {
			case o.HTTPS == nil && isScheme(entry, "https"):
				if u := validHTTPS(entry); u != nil {
					o.HTTPS = u
				}
			case o.Mailto == "" && isScheme(entry, "mailto"):
				o.Mailto = entry
			}
		}
	}
	if o.HTTPS != nil && len(unsub) == 1 && len(post) == 1 &&
		strings.EqualFold(strings.TrimSpace(post[0]), OneClickValue) {
		o.OneClick = true
	}
	return o
}

// entries returns the bracketed URIs of one header value. Text outside the
// brackets is ignored, and so is a parenthesised comment. Whitespace inside a
// URI is a fold and is removed; a control or non-ASCII character voids that
// entry.
func entries(v string) []string {
	if len(v) > maxHeaderBytes {
		return nil
	}
	var out []string
	for i := 0; i < len(v) && len(out) < maxEntries; i++ {
		switch v[i] {
		case '(':
			depth := 1
			for i++; i < len(v) && depth > 0; i++ {
				switch v[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			i--
		case '<':
			end := strings.IndexByte(v[i+1:], '>')
			if end < 0 {
				return out
			}
			raw := v[i+1 : i+1+end]
			i += end + 1
			if e, ok := cleanEntry(raw); ok {
				out = append(out, e)
			}
		}
	}
	return out
}

func cleanEntry(raw string) (string, bool) {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			continue
		case unicode.IsControl(r) || r > unicode.MaxASCII:
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), b.Len() > 0
}

func isScheme(entry, scheme string) bool {
	return len(entry) > len(scheme) && strings.EqualFold(entry[:len(scheme)+1], scheme+":")
}

func validHTTPS(entry string) *url.URL {
	u, err := url.Parse(entry)
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	u.Scheme = "https"
	if u.User != nil || u.Hostname() == "" || u.Opaque != "" {
		return nil
	}
	switch u.Port() {
	case "", "443", "8443":
	default:
		return nil
	}
	// Re-parse the serialised form so what is shown and fetched is one string.
	re, err := url.Parse(u.String())
	if err != nil || re.Hostname() != u.Hostname() {
		return nil
	}
	return re
}

// Lookup returns the DNS TXT records of a domain.
type Lookup func(ctx context.Context, domain string) ([]string, error)

// VerifyOneClick reports whether raw carries a valid DKIM signature whose h=
// list covers both List-Unsubscribe and List-Unsubscribe-Post (RFC 8058
// section 4). A nil lookup uses the system resolver. Every DNS query is bound
// by ctx and a five second limit, because the library's own lookup has none.
// Any error counts as false.
func VerifyOneClick(ctx context.Context, raw []byte, lookup Lookup) bool {
	if lookup == nil {
		lookup = net.DefaultResolver.LookupTXT
	}
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	vs, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{
		LookupTXT:        func(domain string) ([]string, error) { return lookup(ctx, domain) },
		MaxVerifications: maxVerifications,
	})
	if err != nil && len(vs) == 0 {
		return false
	}
	for _, v := range vs {
		if v == nil || v.Err != nil {
			continue
		}
		var unsub, post bool
		for _, k := range v.HeaderKeys {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "list-unsubscribe":
				unsub = true
			case "list-unsubscribe-post":
				post = true
			}
		}
		if unsub && post {
			return true
		}
	}
	return false
}

// Mailto is the part of a mailto: URI that may seed a message.
type Mailto struct {
	// To is the decoded address part. It may hold several addresses; the caller
	// keeps it only when it is exactly one.
	To      string
	Subject string
	Body    string
}

// ParseMailto splits a mailto: URI (RFC 6068) and percent-decodes the address,
// subject and body. Every other header field (cc, bcc, to, in-reply-to and so
// on) is dropped, so the URI cannot add recipients. A plus sign is literal.
func ParseMailto(raw string) (Mailto, bool) {
	if !isScheme(raw, "mailto") {
		return Mailto{}, false
	}
	rest := raw[len("mailto:"):]
	addr, query, _ := strings.Cut(rest, "?")
	to, err := url.PathUnescape(addr)
	if err != nil || to == "" {
		return Mailto{}, false
	}
	m := Mailto{To: to}
	for _, pair := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(pair, "=")
		val, err := url.PathUnescape(v)
		if err != nil {
			continue
		}
		switch strings.ToLower(k) {
		case "subject":
			m.Subject = val
		case "body":
			m.Body = val
		}
	}
	return m, true
}
