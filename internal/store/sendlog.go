package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Send log: a short-lived record of which steps of one web compose send have
// completed, so a retry of the same form submission does not write them again.
//
//	/user/{uid}/sendlog/{sendID}/local/{hex16(sha256(lower(rcpt)))}
//	/user/{uid}/sendlog/{sendID}/external
//	/user/{uid}/sendlog/{sendID}/sent
//
// The value is a fixed 40-byte record, not a protobuf: 8 bytes of big-endian
// unix seconds, then the 32-byte content fingerprint of the submission. Every
// scanner of the /user/ tree filters by the type segment, so a non-protobuf
// value under "sendlog" is never decoded. Entries are reclaimed by the
// leader's orphan GC once older than SendLogTTL.

// SendLogTTL is how long a send log entry is kept. Retries happen within
// minutes; a week is generous and the entries are tiny.
const SendLogTTL = 7 * 24 * time.Hour

// SendLogRecordSize is the encoded record length.
const SendLogRecordSize = 8 + sha256.Size

// ErrSendLogRecord means a stored send log value is not a valid record.
var ErrSendLogRecord = errors.New("malformed send log record")

// SendLogPrefix is the key prefix under which one user's send log lives.
func SendLogPrefix(userID string) []byte {
	return []byte("/user/" + userID + "/sendlog/")
}

// SendLogLocalStep is the step name for one local recipient. The address is
// hashed because it may contain '/'.
func SendLogLocalStep(rcpt string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(rcpt)))
	return "local/" + hex.EncodeToString(sum[:8])
}

// SendLogKey is the key recording that step of the send sendID is done.
func SendLogKey(userID, sendID, step string) []byte {
	return []byte(fmt.Sprintf("/user/%s/sendlog/%s/%s", userID, sendID, step))
}

// EncodeSendLogRecord encodes a completion time and content fingerprint.
func EncodeSendLogRecord(at time.Time, fingerprint [sha256.Size]byte) []byte {
	out := make([]byte, SendLogRecordSize)
	secs := max(at.Unix(), 0)
	binary.BigEndian.PutUint64(out, uint64(secs)) // #nosec G115 -- clamped non-negative above
	copy(out[8:], fingerprint[:])
	return out
}

// DecodeSendLogRecord is the inverse of EncodeSendLogRecord.
func DecodeSendLogRecord(v []byte) (time.Time, [sha256.Size]byte, error) {
	var fp [sha256.Size]byte
	if len(v) != SendLogRecordSize {
		return time.Time{}, fp, ErrSendLogRecord
	}
	secs := binary.BigEndian.Uint64(v)
	if secs > math.MaxInt64 {
		return time.Time{}, fp, ErrSendLogRecord
	}
	copy(fp[:], v[8:])
	return time.Unix(int64(secs), 0), fp, nil // #nosec G115 -- bounded by the check above
}
