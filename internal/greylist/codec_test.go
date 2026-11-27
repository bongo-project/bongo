package greylist

import (
	"bytes"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	for _, kind := range []byte{KindTriplet, KindNetwork} {
		key := Key{1, 2, 3, 15: 9}
		b := Encode(kind, key, 1_800_000_123)
		if len(b) != MessageLen || MessageLen != 27 {
			t.Fatalf("len = %d, want 27", len(b))
		}
		k, gotKey, ts, ok := Decode(b)
		if !ok || k != kind || gotKey != key || ts != 1_800_000_123 {
			t.Fatalf("round trip = %v %v %v %v", k, gotKey, ts, ok)
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	good := Encode(KindTriplet, Key{1}, 5)
	mut := func(f func(b []byte) []byte) []byte { return f(bytes.Clone(good)) }
	tests := []struct {
		name string
		in   []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"short", good[:MessageLen-1]},
		{"long", append(bytes.Clone(good), 0)},
		{"wrong prefix", mut(func(b []byte) []byte { b[0] = 'X'; return b })},
		{"future version", mut(func(b []byte) []byte { b[1] = 2; return b })},
		{"version zero", mut(func(b []byte) []byte { b[1] = 0; return b })},
		{"kind zero", mut(func(b []byte) []byte { b[2] = 0; return b })},
		{"unknown kind", mut(func(b []byte) []byte { b[2] = 3; return b })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, ok := Decode(tt.in); ok {
				t.Fatal("decoded")
			}
		})
	}
}

func TestCodecTimestampBounds(t *testing.T) {
	// A negative time is clamped to zero on the way out.
	if _, _, ts, ok := Decode(Encode(KindNetwork, Key{1}, -5)); !ok || ts != 0 {
		t.Fatalf("negative encode = %d %v, want 0 true", ts, ok)
	}
	// A peer sending a time with the top bit set is refused, not wrapped negative.
	b := Encode(KindNetwork, Key{1}, 5)
	b[MessageLen-8] = 0x80
	if _, _, _, ok := Decode(b); ok {
		t.Fatal("decoded a timestamp above MaxInt64")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(Encode(KindTriplet, Key{1}, 5))
	f.Add(Encode(KindNetwork, Key{2}, -1))
	f.Add([]byte{})
	f.Add([]byte("G\x01"))
	f.Fuzz(func(t *testing.T, b []byte) {
		kind, key, ts, ok := Decode(b)
		if !ok {
			return
		}
		// Anything accepted must re-encode to the same bytes.
		if !bytes.Equal(Encode(kind, key, ts), b) {
			t.Fatalf("decode of %x is not canonical", b)
		}
	})
}

func TestDecodeDoesNotAllocate(t *testing.T) {
	b := Encode(KindNetwork, Key{7}, 99)
	if n := testing.AllocsPerRun(100, func() { Decode(b) }); n != 0 {
		t.Fatalf("Decode allocated %v times", n)
	}
}
