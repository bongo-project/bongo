package store

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestVacationSentKey(t *testing.T) {
	base := string(VacationSentKey("u1", "alice@example.com"))
	if !strings.HasPrefix(base, "/user/u1/vacation/sent/") {
		t.Fatalf("key %q lacks prefix", base)
	}
	for _, same := range []string{" Alice@Example.COM ", "<alice@example.com>"} {
		if got := string(VacationSentKey("u1", same)); got != base {
			t.Errorf("key for %q = %q, want %q", same, got, base)
		}
	}
	if string(VacationSentKey("u1", "bob@example.com")) == base {
		t.Error("different senders share a key")
	}
	if string(VacationSentKey("u2", "alice@example.com")) == base {
		t.Error("different users share a key")
	}
	// A '/' in the address must not add a path segment.
	k := string(VacationSentKey("u1", "a/b@example.com"))
	if strings.Count(k, "/") != strings.Count(base, "/") {
		t.Errorf("key %q gained a segment", k)
	}
}

func TestVacationSentRecord(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	got, err := DecodeVacationSent(EncodeVacationSent(at))
	if err != nil || !got.Equal(at) {
		t.Fatalf("round trip = %v, %v", got, err)
	}
	if got, _ := DecodeVacationSent(EncodeVacationSent(time.Unix(-5, 0))); got.Unix() != 0 {
		t.Errorf("negative time not clamped: %v", got)
	}
	for _, bad := range [][]byte{nil, {1, 2, 3}, make([]byte, 9), {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}} {
		if _, err := DecodeVacationSent(bad); err == nil {
			t.Errorf("DecodeVacationSent(%v) accepted a malformed record", bad)
		}
	}
}

func TestVacationIntervalDays(t *testing.T) {
	tests := []struct{ in, want int32 }{{0, 7}, {-3, 7}, {1, 1}, {14, 14}, {30, 30}, {31, 30}, {9999, 30}}
	for _, tt := range tests {
		got := VacationIntervalDays(&bongov1.UserPreferences{VacationIntervalDays: tt.in})
		if got != int(tt.want) {
			t.Errorf("interval %d = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestVacationActive(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		name string
		p    *bongov1.UserPreferences
		want bool
	}{
		{"disabled", &bongov1.UserPreferences{}, false},
		{"disabled inside window", &bongov1.UserPreferences{VacationStart: 500, VacationEnd: 2000}, false},
		{"open window", &bongov1.UserPreferences{VacationEnabled: true}, true},
		{"inside", &bongov1.UserPreferences{VacationEnabled: true, VacationStart: 500, VacationEnd: 2000}, true},
		{"before start", &bongov1.UserPreferences{VacationEnabled: true, VacationStart: 1001}, false},
		{"at start", &bongov1.UserPreferences{VacationEnabled: true, VacationStart: 1000}, true},
		{"after end", &bongov1.UserPreferences{VacationEnabled: true, VacationEnd: 999}, false},
		{"at end", &bongov1.UserPreferences{VacationEnabled: true, VacationEnd: 1000}, true},
		{"open start", &bongov1.UserPreferences{VacationEnabled: true, VacationEnd: 2000}, true},
	}
	for _, tt := range tests {
		if got := VacationActive(tt.p, now); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestVacationValidation(t *testing.T) {
	long := strings.Repeat("é", VacationMaxSubjectRunes+1)
	subjects := []struct {
		in   string
		okay bool
	}{
		{"", true}, {"Away", true}, {"Away\r\nBcc: x@y", false}, {"a\nb", false}, {"a\x00b", false},
		{strings.Repeat("é", VacationMaxSubjectRunes), true}, {long, false}, {"\xff\xfe", false},
	}
	for _, tt := range subjects {
		if err := ValidateVacationSubject(tt.in); (err == nil) != tt.okay {
			t.Errorf("subject %q: err=%v, want ok=%v", tt.in, err, tt.okay)
		}
	}
	bodies := []struct {
		in   string
		okay bool
	}{
		{"", true}, {"Back soon\r\nBye", true}, {strings.Repeat("a", VacationMaxBodyBytes), true},
		{strings.Repeat("a", VacationMaxBodyBytes+1), false}, {"\xff", false}, {"a\x00b", false},
	}
	for _, tt := range bodies {
		if err := ValidateVacationBody(tt.in); (err == nil) != tt.okay {
			t.Errorf("body len %d: err=%v, want ok=%v", len(tt.in), err, tt.okay)
		}
	}
	for d, okay := range map[int]bool{0: false, 1: true, 30: true, 31: false, -1: false} {
		if err := ValidateVacationInterval(d); (err == nil) != okay {
			t.Errorf("interval %d: err=%v, want ok=%v", d, err, okay)
		}
	}
	windows := []struct {
		s, e int64
		okay bool
	}{{0, 0, true}, {100, 0, true}, {0, 100, true}, {100, 100, true}, {100, 200, true}, {200, 100, false}}
	for _, tt := range windows {
		if err := ValidateVacationWindow(tt.s, tt.e); (err == nil) != tt.okay {
			t.Errorf("window %d..%d: err=%v, want ok=%v", tt.s, tt.e, err, tt.okay)
		}
	}
}
