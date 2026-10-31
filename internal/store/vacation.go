package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Vacation reply tracking: one record per (user, sender) saying when the last
// vacation reply to that sender went out.
//
//	/user/{uid}/vacation/sent/{hex(sha256(lower(sender)))}
//
// The value is 8 bytes of big-endian unix seconds, not a protobuf. Every
// scanner of the /user/ tree filters by the type segment, so a non-protobuf
// value under "vacation" is never decoded. The sender is hashed because an
// address may contain '/'. The leader's orphan GC deletes entries older than
// VacationSentTTL, and disabling vacation deletes the user's entries.

// VacationSentTTL is how long a tracking record is kept. The longest reply
// interval is VacationMaxIntervalDays, so a record older than this can no
// longer suppress anything.
const VacationSentTTL = 30 * 24 * time.Hour

// Vacation settings limits.
const (
	// VacationDefaultIntervalDays is read when the stored interval is 0.
	VacationDefaultIntervalDays = 7
	// VacationMaxIntervalDays is the longest configurable reply interval.
	VacationMaxIntervalDays = 30
	// VacationMaxSubjectRunes caps the configured subject.
	VacationMaxSubjectRunes = 200
	// VacationMaxBodyBytes caps the configured body.
	VacationMaxBodyBytes = 4096
	// VacationRepliesPerHour caps replies per user per hour on one node.
	VacationRepliesPerHour = 100
)

// Vacation validation errors.
var (
	ErrVacationRecord   = errors.New("malformed vacation record")
	ErrVacationSubject  = errors.New("vacation subject invalid")
	ErrVacationBody     = errors.New("vacation body invalid")
	ErrVacationInterval = errors.New("vacation interval out of range")
	ErrVacationWindow   = errors.New("vacation end is before its start")
)

// VacationSentPrefix is the key prefix under which one user's tracking
// records live.
func VacationSentPrefix(userID string) []byte {
	return []byte("/user/" + userID + "/vacation/sent/")
}

// VacationSentKey is the tracking key for replies to sender. The sender is
// lowercased, trimmed and stripped of angle brackets before hashing.
func VacationSentKey(userID, sender string) []byte {
	sender = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(sender), "<"), ">")
	sum := sha256.Sum256([]byte(NormaliseAddress(sender)))
	return append(VacationSentPrefix(userID), hex.EncodeToString(sum[:])...)
}

// EncodeVacationSent encodes the time of a reply.
func EncodeVacationSent(at time.Time) []byte {
	out := make([]byte, 8)
	secs := max(at.Unix(), 0)
	binary.BigEndian.PutUint64(out, uint64(secs)) // #nosec G115 -- clamped non-negative above
	return out
}

// DecodeVacationSent is the inverse of EncodeVacationSent.
func DecodeVacationSent(v []byte) (time.Time, error) {
	if len(v) != 8 {
		return time.Time{}, ErrVacationRecord
	}
	secs := binary.BigEndian.Uint64(v)
	if secs > math.MaxInt64 {
		return time.Time{}, ErrVacationRecord
	}
	return time.Unix(int64(secs), 0), nil // #nosec G115 -- bounded by the check above
}

// VacationIntervalDays returns the reply interval in days. A stored 0 (a
// record written before the field existed, or never set) means the default;
// an out-of-range value is clamped so a corrupt record cannot silence a
// sender for ever.
func VacationIntervalDays(prefs *bongov1.UserPreferences) int {
	d := int(prefs.GetVacationIntervalDays())
	if d <= 0 {
		return VacationDefaultIntervalDays
	}
	return min(d, VacationMaxIntervalDays)
}

// VacationActive reports whether vacation replies are on at now: enabled and
// inside the window (0 on either side means open).
func VacationActive(prefs *bongov1.UserPreferences, now time.Time) bool {
	if !prefs.GetVacationEnabled() {
		return false
	}
	t := now.Unix()
	if s := prefs.GetVacationStart(); s != 0 && t < s {
		return false
	}
	if e := prefs.GetVacationEnd(); e != 0 && t > e {
		return false
	}
	return true
}

// ValidateVacationSubject checks a configured subject: no control characters
// (so no CR or LF) and at most VacationMaxSubjectRunes runes. Empty is valid
// and means "Auto: " plus the original subject.
func ValidateVacationSubject(s string) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > VacationMaxSubjectRunes {
		return ErrVacationSubject
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			return ErrVacationSubject
		}
	}
	return nil
}

// ValidateVacationBody checks a configured body: valid UTF-8 and at most
// VacationMaxBodyBytes bytes.
func ValidateVacationBody(s string) error {
	if !utf8.ValidString(s) || len(s) > VacationMaxBodyBytes || strings.ContainsRune(s, 0) {
		return ErrVacationBody
	}
	return nil
}

// ValidateVacationInterval checks the interval is 1 to VacationMaxIntervalDays.
func ValidateVacationInterval(days int) error {
	if days < 1 || days > VacationMaxIntervalDays {
		return ErrVacationInterval
	}
	return nil
}

// ValidateVacationWindow refuses an end before the start. A zero on either
// side is open and always valid.
func ValidateVacationWindow(start, end int64) error {
	if start != 0 && end != 0 && end < start {
		return ErrVacationWindow
	}
	return nil
}
