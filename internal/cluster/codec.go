package cluster

import (
	"bytes"
	"fmt"

	"github.com/hashicorp/go-msgpack/v2/codec"
)

// encodeMsgPack encodes a value using MsgPack, consistent with hashicorp/raft's
// internal encoding format.
func encodeMsgPack(val interface{}) ([]byte, error) {
	var buf bytes.Buffer
	h := &codec.MsgpackHandle{}
	enc := codec.NewEncoder(&buf, h)
	if err := enc.Encode(val); err != nil {
		return nil, fmt.Errorf("msgpack encode: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeMsgPack decodes a MsgPack-encoded value.
func decodeMsgPack(data []byte, out interface{}) error {
	h := &codec.MsgpackHandle{}
	dec := codec.NewDecoder(bytes.NewReader(data), h)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("msgpack decode: %w", err)
	}
	return nil
}
