package store

import (
	"errors"
	"strings"
	"testing"
)

func TestBlobStoreStat(t *testing.T) {
	bs := newTestBlobStore(t)
	hash, err := bs.Put(make([]byte, 3001))
	if err != nil {
		t.Fatal(err)
	}
	mt, size, err := bs.Stat(hash)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	wantMT, err := bs.ModTime(hash)
	if err != nil {
		t.Fatal(err)
	}
	if size != 3001 || !mt.Equal(wantMT) {
		t.Errorf("Stat = %v, %d; want %v, 3001", mt, size, wantMT)
	}
	if _, _, err := bs.Stat("../etc/passwd"); !errors.Is(err, ErrInvalidHash) {
		t.Errorf("invalid hash err = %v, want ErrInvalidHash", err)
	}
	if _, _, err := bs.Stat(strings.Repeat("a", 64)); err == nil {
		t.Error("Stat of a missing blob succeeded")
	}
}
