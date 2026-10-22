package testutil

import (
	"fmt"
	"strings"
)

// DeepUnclosedMultipart returns an RFC 5322 message whose multipart bodies are
// nested depth levels deep with no closing boundaries. enmime's parse time
// doubles per level on this shape (depth 22 takes about a second).
func DeepUnclosedMultipart(depth int) []byte {
	var b strings.Builder
	b.WriteString("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: deep nesting\r\n")
	b.WriteString("Message-ID: <deep@example.com>\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"b0\"\r\n\r\n")
	for i := range depth {
		fmt.Fprintf(&b, "--b%d\r\nContent-Type: multipart/mixed; boundary=\"b%d\"\r\n\r\n", i, i+1)
	}
	fmt.Fprintf(&b, "--b%d\r\nContent-Type: text/plain\r\n\r\nhello\r\n", depth)
	return []byte(b.String())
}

// FlatMultipart returns a closed multipart/mixed message with n text parts.
func FlatMultipart(n int) []byte {
	var b strings.Builder
	b.WriteString("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: many parts\r\n")
	b.WriteString("Message-ID: <flat@example.com>\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"flat\"\r\n\r\n")
	for i := range n {
		fmt.Fprintf(&b, "--flat\r\nContent-Type: text/plain\r\n\r\npart %d\r\n", i)
	}
	b.WriteString("--flat--\r\n")
	return []byte(b.String())
}
