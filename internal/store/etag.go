package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ContentETag returns a strong HTTP ETag derived from a resource's stored
// bytes, for the CalDAV and CardDAV backends.
//
// It replaces an ETag built from the record's UpdatedAt timestamp in whole
// seconds. That was not merely imprecise: two edits landing in the same second
// produced identical ETags, so a client could not detect the second change and
// -- more importantly -- an If-Match precondition built on it could be
// satisfied by a version the client had never seen. Conditional requests are
// only as sound as the ETag underneath them, so this had to change before
// If-Match was worth honouring at all.
//
// Hashing the content rather than bumping a counter means an ETag changes
// exactly when the resource changes. Re-PUTting byte-identical data keeps the
// ETag, which is correct: the representation the client holds is still current.
// It also needs no new proto field and no migration of existing records.
//
// fallback covers a record with no stored raw form (older rows written before
// the raw bytes were retained). It is prefixed so it can never collide with a
// hash, and it is weaker -- second-resolution again -- so the caller should
// treat those records as the legacy case they are rather than assume the
// guarantee above.
func ContentETag(content []byte, fallback int64) string {
	return `"` + ContentETagValue(content, fallback) + `"`
}

// ContentETagValue is ContentETag without the surrounding quotes.
//
// Both forms are needed and they are not interchangeable. The ETag *header*
// carries the quoted form, while go-webdav's ConditionalMatch.MatchETag
// compares against the bare value it has already unquoted from the client's
// If-Match header. Passing the quoted form to MatchETag makes every comparison
// fail, which presents as a server rejecting the very ETag it just issued.
func ContentETagValue(content []byte, fallback int64) string {
	if len(content) == 0 {
		return fmt.Sprintf("t%d", fallback)
	}
	sum := sha256.Sum256(content)
	// 128 bits is far past what is needed to make an accidental collision
	// between two versions of one resource impossible in practice, and keeps
	// the header short.
	return hex.EncodeToString(sum[:16])
}
