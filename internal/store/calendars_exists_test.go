package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCalendarStoreExists(t *testing.T) {
	cs := newCalendarTestEnv(t)
	if err := cs.Update("u1", "work", &bongov1.Calendar{Guid: "work", Name: "Work"}); err != nil {
		t.Fatal(err)
	}
	ioErr := errors.New("pebble get: io failure")
	tests := []struct {
		name    string
		user    string
		guid    string
		get     func([]byte) ([]byte, error)
		want    bool
		wantErr error
	}{
		{name: "found", user: "u1", guid: "work", want: true},
		{name: "missing guid", user: "u1", guid: "nope"},
		{name: "another user's guid", user: "u2", guid: "work"},
		{name: "not found from the read", user: "u1", guid: "work",
			get: func([]byte) ([]byte, error) { return nil, ErrNotFound }},
		{name: "read error is returned", user: "u1", guid: "work",
			get: func([]byte) ([]byte, error) { return nil, ioErr }, wantErr: ioErr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cs.get = tc.get
			t.Cleanup(func() { cs.get = nil })
			got, err := cs.Exists(tc.user, tc.guid)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Exists err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("Exists = %v, want %v", got, tc.want)
			}
		})
	}
}
