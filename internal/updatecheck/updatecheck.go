// Package updatecheck holds the pieces of the version check that the leader
// worker (internal/agent) and the admin pages (internal/web/handlers) share:
// the stored record, semantic version parsing and comparison, and the
// validation of the endpoint's answer. Everything that arrives from the
// network is hostile; nothing here trusts it.
package updatecheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Pebble keys. Both are plain PUTs, not protobufs.
const (
	// KeyInstanceID holds 32 lowercase hex characters, written once.
	KeyInstanceID = "/system/instance_id"
	// KeyResult holds a JSON Record.
	KeyResult = "/system/update_check"
)

// Result codes stored in Record.Result.
const (
	ResultOK       = "ok"
	ResultError    = "error"
	ResultInvalid  = "invalid"
	ResultRedirect = "redirect"
	ResultTooLarge = "too_large"
)

const (
	maxLatestLen = 64
	maxURLLen    = 2048
	// MaxResponseBytes is the cap on the endpoint's answer.
	MaxResponseBytes = 16 << 10
)

// semverPattern is the one the update Worker uses (deploy/update-worker/src/index.js).
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// describeSuffix matches what git describe appends to a build that is not on
// a tag: "-<commits>-g<hash>" and/or "-dirty".
var describeSuffix = regexp.MustCompile(`(-\d+-g[0-9a-f]+|-dirty)(-dirty)?$`)

var instanceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidInstanceID reports whether s is 32 lowercase hex characters.
func ValidInstanceID(s string) bool { return instanceIDPattern.MatchString(s) }

// Record is the value stored at KeyResult. CheckedAt and Result describe the
// last attempt; Latest, ReleaseNotesURL, Security and LatestAt describe the
// last valid answer and are replaced only by one.
type Record struct {
	CheckedAt       int64  `json:"checked_at"`
	Result          string `json:"result"`
	Latest          string `json:"latest,omitempty"`
	ReleaseNotesURL string `json:"release_notes_url,omitempty"`
	Security        bool   `json:"security,omitempty"`
	LatestAt        int64  `json:"latest_at,omitempty"`
}

// Request is the body of the check. It is a fixed struct on purpose: the
// request can never grow a field by accident.
type Request struct {
	Version    string `json:"version"`
	InstanceID string `json:"instance_id"`
}

// Response is a validated answer.
type Response struct {
	Latest          string
	ReleaseNotesURL string
	Security        bool
}

// ErrInvalid is wrapped by every validation failure.
var ErrInvalid = errors.New("invalid update response")

// RunningVersion turns the build version ("v0.17.0") into the form the check
// sends and compares ("0.17.0"). ok is false for a build that is not a tagged
// release: not strict semver ("dev"), or a build from an untagged commit,
// whose git describe suffix ("-3-gabcdef", "-dirty") marks it. A real
// pre-release tag ("0.17.0-rc.1") is ok.
func RunningVersion(v string) (string, bool) {
	v = strings.TrimPrefix(v, "v")
	if len(v) > maxLatestLen || !semverPattern.MatchString(v) || describeSuffix.MatchString(v) {
		return "", false
	}
	return v, true
}

// Semver is a parsed semantic version.
type Semver struct {
	Major, Minor, Patch uint64
	Pre                 []string
}

// ParseSemver parses strict semver without a leading "v" and without build
// metadata.
func ParseSemver(s string) (Semver, bool) {
	if len(s) > maxLatestLen || !semverPattern.MatchString(s) {
		return Semver{}, false
	}
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	var n [3]uint64
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Semver{}, false
		}
		n[i] = v
	}
	v := Semver{Major: n[0], Minor: n[1], Patch: n[2]}
	if pre != "" {
		v.Pre = strings.Split(pre, ".")
	}
	return v, true
}

// Compare orders two versions by semver section 11: -1, 0 or 1.
func (a Semver) Compare(b Semver) int {
	for _, p := range [][2]uint64{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := comparePre(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.Pre) < len(b.Pre):
		return -1
	case len(a.Pre) > len(b.Pre):
		return 1
	}
	return 0
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func comparePre(a, b string) int {
	an, bn := isNumeric(a), isNumeric(b)
	switch {
	case an && bn:
		// Compare as numbers without overflow: shorter is smaller, then text.
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if len(a) != len(b) {
			if len(a) < len(b) {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// Newer reports whether latest is a strictly newer version than running.
// Either failing to parse gives false.
func Newer(latest, running string) bool {
	l, ok := ParseSemver(latest)
	if !ok {
		return false
	}
	r, ok := ParseSemver(running)
	if !ok {
		return false
	}
	return l.Compare(r) > 0
}

// ValidNotesURL checks that raw is an https URL, without user information or
// control characters, on exactly the host (and port) of checkURL, and returns
// it parsed. checkURL is the configured check endpoint.
func ValidNotesURL(raw string, checkURL *url.URL) (*url.URL, error) {
	if raw == "" || len(raw) > maxURLLen {
		return nil, fmt.Errorf("%w: release notes URL length", ErrInvalid)
	}
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return nil, fmt.Errorf("%w: release notes URL characters", ErrInvalid)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: release notes URL syntax", ErrInvalid)
	}
	// Only the canonical spelling is accepted, so a value with raw quotes,
	// angle brackets or backslashes never reaches a page, escaped or not.
	if u.String() != raw {
		return nil, fmt.Errorf("%w: release notes URL is not canonical", ErrInvalid)
	}
	if u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("%w: release notes URL must be plain https", ErrInvalid)
	}
	if checkURL == nil || u.Host == "" || !strings.EqualFold(u.Host, checkURL.Host) {
		return nil, fmt.Errorf("%w: release notes URL is on another host", ErrInvalid)
	}
	return u, nil
}

// ParseResponse validates the endpoint's answer. The error never carries the
// body. Unknown fields are ignored; trailing data is refused.
func ParseResponse(body []byte, checkURL *url.URL) (Response, error) {
	if len(body) > MaxResponseBytes {
		return Response{}, fmt.Errorf("%w: too large", ErrInvalid)
	}
	var raw struct {
		Latest          *string `json:"latest"`
		ReleaseNotesURL *string `json:"release_notes_url"`
		Security        *bool   `json:"security"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&raw); err != nil {
		return Response{}, fmt.Errorf("%w: not the expected JSON", ErrInvalid)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Response{}, fmt.Errorf("%w: trailing data", ErrInvalid)
	}
	if raw.Latest == nil {
		return Response{}, fmt.Errorf("%w: no latest version", ErrInvalid)
	}
	if _, ok := ParseSemver(*raw.Latest); !ok {
		return Response{}, fmt.Errorf("%w: latest is not a semantic version", ErrInvalid)
	}
	if raw.ReleaseNotesURL == nil {
		return Response{}, fmt.Errorf("%w: no release notes URL", ErrInvalid)
	}
	u, err := ValidNotesURL(*raw.ReleaseNotesURL, checkURL)
	if err != nil {
		return Response{}, err
	}
	resp := Response{Latest: *raw.Latest, ReleaseNotesURL: u.String()}
	if raw.Security != nil {
		resp.Security = *raw.Security
	}
	return resp, nil
}

// DecodeRecord parses a stored record. A value that does not parse is treated
// as absent by callers.
func DecodeRecord(b []byte) (Record, bool) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, false
	}
	return r, true
}

// Banner is what the dashboard shows.
type Banner struct {
	Latest   string
	URL      string
	Security bool
}

// BannerFor re-validates a stored record against the current check URL and
// the running version and returns the banner to show, if any. A stored value
// is not trusted: it is checked again exactly as a fresh answer would be.
func BannerFor(rec Record, checkURL *url.URL, running string) (Banner, bool) {
	run, ok := RunningVersion(running)
	if !ok || rec.Latest == "" {
		return Banner{}, false
	}
	if !Newer(rec.Latest, run) {
		return Banner{}, false
	}
	u, err := ValidNotesURL(rec.ReleaseNotesURL, checkURL)
	if err != nil {
		return Banner{}, false
	}
	return Banner{Latest: rec.Latest, URL: u.String(), Security: rec.Security}, true
}
