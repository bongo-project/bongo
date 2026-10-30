package store

import (
	"errors"
	"testing"
)

type fakeGetter struct {
	keys map[string]bool
	vals map[string]string
	err  error
}

func (f fakeGetter) Get(key []byte) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.vals[string(key)]; ok {
		return []byte(v), nil
	}
	if f.keys[string(key)] {
		return []byte("x"), nil
	}
	return nil, ErrNotFound
}

func TestDomainHosted(t *testing.T) {
	g := fakeGetter{keys: map[string]bool{"/system/domain/hosted.example": true}}
	tests := []struct {
		name    string
		g       fakeGetter
		domain  string
		want    bool
		wantErr bool
	}{
		{"hosted", g, "hosted.example", true, false},
		{"case-insensitive", g, "Hosted.Example", true, false},
		{"not hosted", g, "gmail.com", false, false},
		{"read error", fakeGetter{err: errors.New("boom")}, "hosted.example", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DomainHosted(tt.g, tt.domain)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("DomainHosted = %v, %v; want %v, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestDomainDisabled(t *testing.T) {
	g := fakeGetter{vals: map[string]string{
		"/system/domain/on.example":  "enabled",
		"/system/domain/off.example": "disabled",
	}}
	tests := []struct {
		name    string
		g       fakeGetter
		domain  string
		want    bool
		wantErr bool
	}{
		{"enabled", g, "on.example", false, false},
		{"disabled", g, "off.example", true, false},
		{"case-insensitive", g, "Off.Example", true, false},
		{"not hosted", g, "gmail.com", false, false},
		{"read error", fakeGetter{err: errors.New("boom")}, "off.example", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DomainDisabled(tt.g, tt.domain)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("DomainDisabled = %v, %v; want %v, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
