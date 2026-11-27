package greylist

import (
	"encoding/binary"
	"math"
)

// Wire format of one gossip message: 'G', version, kind, 16-byte key, 8-byte
// big-endian unix seconds. One message carries exactly one entry, so the
// decoder never reads a length from the peer and never allocates from input.
const (
	wirePrefix  byte = 'G'
	wireVersion byte = 1

	// KindTriplet is "this triplet was first seen at T".
	KindTriplet byte = 1
	// KindNetwork is "this client network is known until T".
	KindNetwork byte = 2

	// KeyLen is the size of every key (triplet hash or network).
	KeyLen = 16

	// MessageLen is the exact length of an encoded entry.
	MessageLen = 3 + KeyLen + 8
)

// Key is a fixed-size table key. No address appears in it in the clear.
type Key [KeyLen]byte

// Encode builds the gossip message for one entry.
func Encode(kind byte, key Key, t int64) []byte {
	b := make([]byte, MessageLen)
	b[0], b[1], b[2] = wirePrefix, wireVersion, kind
	copy(b[3:3+KeyLen], key[:])
	if t < 0 {
		t = 0 // no valid entry predates the epoch
	}
	binary.BigEndian.PutUint64(b[3+KeyLen:], uint64(t))
	return b
}

// Decode parses one gossip message. ok is false for anything that is not
// exactly a version 1 entry of a known kind: unknown prefixes and versions are
// dropped silently so that newer formats stay compatible.
func Decode(b []byte) (kind byte, key Key, t int64, ok bool) {
	if len(b) != MessageLen || b[0] != wirePrefix || b[1] != wireVersion {
		return 0, key, 0, false
	}
	kind = b[2]
	if kind != KindTriplet && kind != KindNetwork {
		return 0, key, 0, false
	}
	copy(key[:], b[3:3+KeyLen])
	u := binary.BigEndian.Uint64(b[3+KeyLen:])
	if u > math.MaxInt64 {
		return 0, Key{}, 0, false
	}
	return kind, key, int64(u), true
}

// IsMessage reports whether b carries the greylist prefix, so a handler can
// tell its messages from anything else sent over the same transport.
func IsMessage(b []byte) bool { return len(b) > 0 && b[0] == wirePrefix }
