package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// newRunID returns 64 random bits in hex. It tells one run's messages from
// another's when a later step measures delivery delay.
func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// fillerBody returns size bytes of CRLF-terminated lines. The content is
// fixed so that message size does not vary the cost of a send.
func fillerBody(size int) []byte {
	const line = "loadgen filler line, nothing here is meaningful, ignore it.....\r\n"
	var sb strings.Builder
	for sb.Len() < size {
		sb.WriteString(line)
	}
	return []byte(sb.String()[:size])
}

// messageIDs returns the two identifiers a message carries. Both derive from
// the run ID and sequence number; the Message-ID domain is the test domain,
// never the target host.
func messageIDs(runID string, seq int64, testDomain string) (loadgenID, messageID string) {
	s := strconv.FormatInt(seq, 10)
	return runID + "-" + s, "<" + runID + "." + s + "@" + testDomain + ">"
}

// buildMessage renders one RFC 5322 message with CRLF line endings.
// X-Loadgen-Sent is the dispatch time, so delivery delay can be measured from
// the header alone.
func buildMessage(runID string, seq int64, testDomain, from, to string, sent time.Time, body []byte) []byte {
	id, mid := messageIDs(runID, seq, testDomain)
	var sb strings.Builder
	sb.Grow(400 + len(body))
	sb.WriteString("From: <" + from + ">\r\n")
	sb.WriteString("To: <" + to + ">\r\n")
	sb.WriteString("Subject: loadgen " + id + "\r\n")
	sb.WriteString("Date: " + sent.UTC().Format(time.RFC1123Z) + "\r\n")
	sb.WriteString("Message-ID: " + mid + "\r\n")
	sb.WriteString("X-Loadgen-Id: " + id + "\r\n")
	sb.WriteString("X-Loadgen-Sent: " + sent.UTC().Format(time.RFC3339Nano) + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=us-ascii\r\n")
	sb.WriteString("\r\n")
	sb.Write(body)
	sb.WriteString("\r\n")
	return []byte(sb.String())
}
