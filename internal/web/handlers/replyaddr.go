package handlers

import (
	"net/mail"
	"net/url"
	"strings"

	"github.com/jhillyerd/enmime"
)

const (
	// maxReplyToAddresses caps the Reply-To list a reply will address.
	maxReplyToAddresses = 20
	// maxListPostEntries caps how many <...> entries of a List-Post header are
	// scanned for a mailto: URI.
	maxListPostEntries = 5
	// maxReplyAllAddresses caps the To+Cc addresses a reply-all considers, which
	// bounds the resolver and address-store lookups made for one request.
	maxReplyAllAddresses = 50
)

// envelopeHeader returns the decoded header from env, or "" when env is nil.
func envelopeHeader(env *enmime.Envelope, name string) string {
	if env == nil {
		return ""
	}
	return env.GetHeader(name)
}

// rawAddressHeader returns an address header as it appears in the message,
// without decoding encoded-words. net/mail decodes them itself; decoding
// first turns an encoded display name holding a comma ("Müller, Hans")
// into text the address-list parser rejects.
func rawAddressHeader(env *enmime.Envelope, name string) string {
	if env == nil || env.Root == nil {
		return ""
	}
	return env.Root.Header.Get(name)
}

// listPostAddress extracts the posting address from a List-Post header (RFC
// 2369). It returns a bare address when the header holds a mailto: URI naming
// exactly one valid address, and "" for "NO", other URI schemes, several
// addresses, or anything invalid. The header is untrusted.
func listPostAddress(h string) string {
	h = strings.TrimSpace(h)
	if h == "" || len(h) > 2048 || strings.ContainsAny(h, "\r\n") {
		return ""
	}
	if len(h) >= 2 && strings.EqualFold(h[:2], "NO") {
		return ""
	}
	rest := h
	for range maxListPostEntries {
		open := strings.IndexByte(rest, '<')
		if open < 0 {
			return ""
		}
		end := strings.IndexByte(rest[open:], '>')
		if end < 0 {
			return ""
		}
		entry := strings.TrimSpace(rest[open+1 : open+end])
		rest = rest[open+end+1:]
		if len(entry) < len("mailto:") || !strings.EqualFold(entry[:len("mailto:")], "mailto:") {
			continue
		}
		addr := entry[len("mailto:"):]
		if q := strings.IndexByte(addr, '?'); q >= 0 {
			addr = addr[:q]
		}
		addr, err := url.PathUnescape(addr)
		if err != nil || strings.ContainsRune(addr, ',') {
			return ""
		}
		return composeToPrefill(addr)
	}
	return ""
}

// replyToAddresses parses a Reply-To header into bare addresses. Any parse
// error, line break, oversize list or empty result gives nil, and the caller
// falls back to From.
func replyToAddresses(h string) []string {
	list, ok := parseAddressHeader(h)
	if !ok || len(list) == 0 || len(list) > maxReplyToAddresses {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		if composeToPrefill(a.Address) == "" {
			return nil
		}
		out = append(out, a.Address)
	}
	return out
}

// parseAddressHeader parses one raw address header. ok is false when the
// header is oversize, holds a line break or does not parse; an empty header
// is ok with no addresses.
func parseAddressHeader(h string) ([]*mail.Address, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, true
	}
	if len(h) > 16384 || strings.ContainsAny(h, "\r\n") {
		return nil, false
	}
	list, err := mail.ParseAddressList(h)
	if err != nil {
		return nil, false
	}
	return list, true
}

// replyAllCandidates returns the original To and Cc addresses. The envelope's
// raw headers are preferred, because the stored To and Cc are naive comma
// splits that cut "Doe, John" <j@x> in two; the stored entries, each parsed on
// its own, are the fallback. The result is capped at maxReplyAllAddresses.
func replyAllCandidates(env *enmime.Envelope, storedTo, storedCc []string) []string {
	var out []string
	add := func(a string) {
		if len(out) < maxReplyAllAddresses && a != "" {
			out = append(out, a)
		}
	}
	for _, f := range []struct {
		name   string
		stored []string
	}{{"To", storedTo}, {"Cc", storedCc}} {
		if list, ok := parseAddressHeader(rawAddressHeader(env, f.name)); ok && len(list) > 0 {
			for _, a := range list {
				add(a.Address)
			}
			continue
		}
		for _, s := range f.stored {
			if a, err := mail.ParseAddress(strings.TrimSpace(s)); err == nil {
				add(a.Address)
			}
		}
	}
	return out
}
