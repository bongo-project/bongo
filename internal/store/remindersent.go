package store

import (
	"net/url"
	"strconv"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Email reminder tracking: one record per (event, occurrence) saying that its
// reminder email has been scheduled.
//
//	/user/{uid}/remindersent/{pathescape(event_guid)}/{occurrence_unix}
//
// The value is 8 bytes of big-endian unix seconds (when the record was
// written), not a protobuf; it is encoded like a vacation record. The record is
// written through Raft BEFORE the mail is enqueued and a failed write skips the
// send, so a restart or a leader change never repeats a reminder. The price is
// that delivery is at-most-once: a crash between the write and the enqueue, or
// a refused delivery (a full mailbox), loses that one reminder. Do not reorder
// the two steps to "fix" that. The GUID is path-escaped because CalDAV-derived
// GUIDs are chosen by the client. The leader's orphan GC deletes records older
// than ReminderSentTTL.

// ReminderSentTTL is how long a record is kept. A reminder is due at most an
// hour before its occurrence starts and never after it, so a record older than
// this can no longer suppress anything.
const ReminderSentTTL = 48 * time.Hour

// ReminderSentPrefix is the key prefix under which one user's records live.
func ReminderSentPrefix(userID string) []byte {
	return []byte("/user/" + userID + "/remindersent/")
}

// ReminderSentKey is the record key for the occurrence of event guid that
// starts at occurrenceUnix.
func ReminderSentKey(userID, guid string, occurrenceUnix int64) []byte {
	return append(ReminderSentPrefix(userID),
		url.PathEscape(guid)+"/"+strconv.FormatInt(occurrenceUnix, 10)...)
}

// EncodeReminderSent encodes the time a record was written.
func EncodeReminderSent(at time.Time) []byte { return EncodeVacationSent(at) }

// DecodeReminderSent is the inverse of EncodeReminderSent.
func DecodeReminderSent(v []byte) (time.Time, error) { return DecodeVacationSent(v) }

// OwnerPartstat returns the PARTSTAT of the first attendee of e whose address
// is in mine (lower-cased, see NormaliseAddress), or "" when none is.
func OwnerPartstat(e *bongov1.Event, mine map[string]bool) string {
	for _, a := range e.GetAttendees() {
		if mine[NormaliseAddress(a.Address)] {
			return a.Partstat
		}
	}
	return ""
}
