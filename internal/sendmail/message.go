package sendmail

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
)

// maxMessageBytes bounds what is read from stdin; the server enforces its own,
// lower, limit.
const maxMessageBytes = 64 << 20

// Prepared is a message ready for submission.
type Prepared struct {
	Sender string   // envelope sender
	Rcpts  []string // envelope recipients, deduplicated
	Data   []byte   // CRLF line endings, NOT dot-stuffed (the SMTP data writer does that)
}

// headerField is one header field with its folded continuation lines.
type headerField struct {
	name  string
	raw   string // as read, each line ending in CRLF
	value string // everything after the colon, continuation lines joined
}

// Prepare reads the message from in and applies the sendmail rules: LF to
// CRLF, optional dot terminator, Bcc removal, and Date, Message-ID and From
// additions. idSrc supplies the Message-ID randomness (crypto/rand if nil).
func Prepare(in io.Reader, o *Options, cfg *Config, now time.Time, idSrc io.Reader) (*Prepared, error) {
	if idSrc == nil {
		idSrc = rand.Reader
	}
	raw, err := readInput(in, o.IgnoreDots)
	if err != nil {
		return nil, err
	}
	fields, body, err := splitMessage(raw)
	if err != nil {
		return nil, err
	}

	rcpts := append([]string(nil), o.Recipients...)
	if o.ReadRecipients {
		for _, f := range fields {
			switch strings.ToLower(f.name) {
			case "to", "cc", "bcc":
				if strings.TrimSpace(f.value) == "" {
					continue
				}
				list, err := mail.ParseAddressList(f.value)
				if err != nil {
					return nil, dataErr("cannot parse %s header", f.name)
				}
				for _, a := range list {
					addr, err := qualifiedAddress(a.Address)
					if err != nil {
						return nil, err
					}
					rcpts = append(rcpts, addr)
				}
			}
		}
	}
	rcpts = dedupe(rcpts)
	if len(rcpts) == 0 {
		return nil, usageErr("no recipients; give addresses or use -t with a To, Cc or Bcc header")
	}

	sender := cfg.Username
	if o.Sender != "" && strings.Contains(o.Sender, "@") {
		if sender, err = qualifiedAddress(o.Sender); err != nil {
			return nil, err
		}
	}

	var out strings.Builder
	var fromCount int
	var hasDate, hasID bool
	for _, f := range fields {
		switch strings.ToLower(f.name) {
		case "from":
			fromCount++
		case "date":
			hasDate = true
		case "message-id":
			hasID = true
		}
	}
	if fromCount == 0 {
		out.WriteString("From: " + (&mail.Address{Name: o.FullName, Address: sender}).String() + "\r\n")
	}
	if !hasDate {
		out.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	}
	if !hasID {
		id := make([]byte, 16)
		if _, err := io.ReadFull(idSrc, id); err != nil {
			return nil, fmt.Errorf("generating Message-ID: %w", err)
		}
		out.WriteString("Message-ID: <" + hex.EncodeToString(id) + "@" + cfg.domain() + ">\r\n")
	}
	for _, f := range fields {
		switch strings.ToLower(f.name) {
		case "bcc":
			continue // never submitted; the recipients were taken above
		case "from":
			if fromCount == 1 {
				if nf, ok := rewriteLocalFrom(f.value, cfg.Username); ok {
					out.WriteString("From: " + nf + "\r\n")
					continue
				}
			}
		}
		out.WriteString(f.raw)
	}
	out.WriteString("\r\n")
	out.WriteString(body)
	return &Prepared{Sender: sender, Rcpts: rcpts, Data: []byte(out.String())}, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, a := range in {
		k := strings.ToLower(a)
		if !seen[k] {
			seen[k] = true
			out = append(out, a)
		}
	}
	return out
}

// readInput reads stdin line by line, converting LF to CRLF. Unless ignoreDots
// is set, a line holding only "." ends the input.
func readInput(r io.Reader, ignoreDots bool) (string, error) {
	br := bufio.NewReader(io.LimitReader(r, maxMessageBytes+1))
	var out strings.Builder
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			l := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if !ignoreDots && l == "." {
				break
			}
			out.WriteString(l)
			out.WriteString("\r\n")
			if out.Len() > maxMessageBytes {
				return "", dataErr("message exceeds %d bytes", maxMessageBytes)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading message: %w", err)
		}
	}
	return out.String(), nil
}

// splitMessage separates the header fields from the body.
func splitMessage(text string) ([]headerField, string, error) {
	var hdr, body string
	switch {
	case strings.HasPrefix(text, "\r\n"):
		body = text[2:]
	default:
		if i := strings.Index(text, "\r\n\r\n"); i >= 0 {
			hdr, body = text[:i+2], text[i+4:]
		} else {
			hdr = text
		}
	}
	var fields []headerField
	for hdr != "" {
		i := strings.Index(hdr, "\r\n")
		line := hdr[:i+2]
		hdr = hdr[i+2:]
		if line[0] == ' ' || line[0] == '\t' {
			if len(fields) == 0 {
				return nil, "", dataErr("malformed header: continuation line without a field")
			}
			f := &fields[len(fields)-1]
			f.raw += line
			f.value += strings.TrimSuffix(line, "\r\n")
			continue
		}
		c := strings.IndexByte(line, ':')
		if c <= 0 || strings.ContainsAny(line[:c], " \t\x00") {
			return nil, "", dataErr("malformed header line")
		}
		fields = append(fields, headerField{
			name:  line[:c],
			raw:   line,
			value: strings.TrimSuffix(line[c+1:], "\r\n"),
		})
	}
	return fields, body, nil
}

// rewriteLocalFrom handles a From header whose address has no domain, such as
// cronie's "root (Cron Daemon)". The server would refuse it as not owned by the
// account, so it becomes the account address with the display name kept. A
// qualified address, even one the account cannot send as, is left alone for
// the server to judge.
func rewriteLocalFrom(value, account string) (string, bool) {
	if list, err := mail.ParseAddressList(value); err == nil && len(list) > 0 {
		return "", false
	}
	v := strings.TrimSpace(value)
	var name, local string
	if i := strings.LastIndex(v, "<"); i >= 0 && strings.HasSuffix(v, ">") {
		local = strings.TrimSpace(v[i+1 : len(v)-1])
		name = strings.Trim(strings.TrimSpace(v[:i]), `"`)
	} else if o := strings.Index(v, "("); o >= 0 {
		c := strings.LastIndex(v, ")")
		if c < o {
			return "", false
		}
		name = strings.TrimSpace(v[o+1 : c])
		local = strings.TrimSpace(v[:o] + v[c+1:])
	} else {
		local = v
	}
	if local == "" || strings.ContainsAny(local, "@ \t<>()[],;:\"\\") {
		return "", false
	}
	return (&mail.Address{Name: name, Address: account}).String(), true
}
