package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize parses a human-readable byte-size string and returns the value in
// bytes. Supported suffixes (case-insensitive): B, KB, MB, GB. A bare integer
// with no suffix is interpreted as bytes. Returns an error for empty strings or
// unrecognised formats.
//
// Examples: "25MB" -> 26214400, "512kb" -> 524288, "1GB" -> 1073741824.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("parseSize: empty string")
	}

	upper := strings.ToUpper(s)

	var multiplier int64
	var digits string

	switch {
	case strings.HasSuffix(upper, "GB"):
		multiplier = 1024 * 1024 * 1024
		digits = s[:len(s)-2]
	case strings.HasSuffix(upper, "MB"):
		multiplier = 1024 * 1024
		digits = s[:len(s)-2]
	case strings.HasSuffix(upper, "KB"):
		multiplier = 1024
		digits = s[:len(s)-2]
	case strings.HasSuffix(upper, "B"):
		multiplier = 1
		digits = s[:len(s)-1]
	default:
		// No suffix: treat as raw bytes.
		multiplier = 1
		digits = s
	}

	n, err := strconv.ParseInt(strings.TrimSpace(digits), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parseSize: invalid size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("parseSize: negative size %q", s)
	}
	return n * multiplier, nil
}

const defaultMaxMessageSize = 25 * 1024 * 1024 // 25 MB

// MaxMessageSizeBytes parses MaxMessageSize and returns the value in bytes.
// Returns 25 MB when the field is empty, zero, or unparseable.
func (l *SMTPLimits) MaxMessageSizeBytes() int64 {
	if l.MaxMessageSize == "" {
		return defaultMaxMessageSize
	}
	n, err := ParseSize(l.MaxMessageSize)
	if err != nil || n <= 0 {
		return defaultMaxMessageSize
	}
	return n
}
