package config

import (
	"fmt"
	"math"
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
	if n > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("parseSize: size %q overflows int64", s)
	}
	return n * multiplier, nil
}

// CacheBytes returns the configured Pebble block cache size in bytes. An
// empty value returns (0, nil), meaning unset: Pebble keeps its default.
// Zero, negative, unparseable and overflowing values are errors.
func (p *PebbleConfig) CacheBytes() (int64, error) {
	if p.CacheSize == "" {
		return 0, nil
	}
	n, err := ParseSize(p.CacheSize)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("cache size must be positive, got %q", p.CacheSize)
	}
	return n, nil
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
