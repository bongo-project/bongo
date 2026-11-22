// Package contactio reads and writes address-book interchange formats: vCard,
// CSV (the Google Contacts and Outlook header sets) and LDIF (Thunderbird).
//
// Reading is two steps with no side effects: Detect decodes the bytes to UTF-8
// and names the format from the content, and Parse turns the text into records
// the caller can validate before it writes anything. It depends only on go-vcard
// and x/text; it knows nothing about storage.
package contactio

import (
	"errors"
	"fmt"
)

// Format is an interchange format.
type Format string

// The formats Detect recognises.
const (
	FormatVCard Format = "vcard"
	FormatCSV   Format = "csv"
	FormatLDIF  Format = "ldif"
)

var (
	// ErrFormat means the content is none of the supported formats, including
	// a CSV whose header row is not the Google or English Outlook set.
	ErrFormat = errors.New("contactio: unrecognised format")
	// ErrEncoding means the text encoding cannot be read (a NUL byte with no
	// UTF-16 byte order mark).
	ErrEncoding = errors.New("contactio: unsupported text encoding")
	// ErrTooMany means the file holds more records than the caller allows, or
	// so many skipped rows that reading on would only burn CPU.
	ErrTooMany = errors.New("contactio: too many records")
)

// Reason says what is wrong with a record.
type Reason string

// The reasons a record is refused.
const (
	ReasonSyntax     Reason = "syntax"
	ReasonFields     Reason = "fields"
	ReasonBirthday   Reason = "birthday"
	ReasonChange     Reason = "change"
	ReasonURL        Reason = "url"
	ReasonBase64     Reason = "base64"
	ReasonIncomplete Reason = "incomplete"
)

// ParseError names the record that made an import fail. N is the spreadsheet
// row (the header is row 1) for CSV, the card number for vCard and the entry
// number for LDIF, where Line is the first line of the entry.
type ParseError struct {
	Format Format
	N      int
	Line   int
	Reason Reason
}

func (e *ParseError) Error() string {
	if e.Format == FormatLDIF {
		return fmt.Sprintf("contactio: ldif entry %d (line %d): %s", e.N, e.Line, e.Reason)
	}
	return fmt.Sprintf("contactio: %s record %d: %s", e.Format, e.N, e.Reason)
}

// Ref locates a record in the file it came from.
type Ref struct {
	N    int
	Line int
}

// Group is a contact group read from an LDIF groupOfNames entry. Members index
// into Result.Records and name person records only.
type Group struct {
	Name    string
	Members []int
}

// Record is one importable item. A person carries Raw, a vCard; a group carries
// Group and no Raw.
type Record struct {
	Ref   Ref
	Raw   []byte
	Group *Group
}

// Result is a whole file read without a write.
type Result struct {
	Format  Format
	Records []Record
	// Skipped counts items with nothing worth keeping (no name, address, phone
	// or organisation) and entries that are not contacts (organisational units).
	Skipped int
}

// Parse decodes raw, detects its format and reads every record. The first
// malformed record fails the whole parse with a *ParseError.
func Parse(raw []byte) (*Result, error) { return ParseLimit(raw, 0) }

// skipFactor bounds the rows read per allowed record, so a file of millions of
// blank or skipped rows is refused instead of spun through.
const skipFactor = 4

// ParseLimit is Parse that stops with ErrTooMany as soon as the file holds more
// than max records (or more than max*skipFactor rows, skipped ones included),
// before building the rest. max <= 0 means no limit.
func ParseLimit(raw []byte, max int) (*Result, error) {
	format, text, err := Detect(raw)
	if err != nil {
		return nil, err
	}
	switch format {
	case FormatVCard:
		return parseVCard(text, max)
	case FormatCSV:
		return parseCSV(text, max)
	default:
		return parseLDIF(text, max)
	}
}
