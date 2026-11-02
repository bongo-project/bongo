package store

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestReminderSentKey(t *testing.T) {
	cases := []struct {
		name, guid string
		occ        int64
		want       string
	}{
		{"plain", "abc", 1893488400, "/user/u1/remindersent/abc/1893488400"},
		{"slash is escaped", "a/b", 5, "/user/u1/remindersent/a%2Fb/5"},
		{"dot dot is not a path", "../x", 5, "/user/u1/remindersent/..%2Fx/5"},
		{"uid from a client", "u@example.com", 7, "/user/u1/remindersent/u@example.com/7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ReminderSentKey("u1", tc.guid, tc.occ))
			if got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
			if !strings.HasPrefix(got, string(ReminderSentPrefix("u1"))) {
				t.Errorf("key is outside the user prefix")
			}
			// Exactly one separator after the escaped GUID, so two events can
			// never share a key through their GUIDs.
			if rest := strings.TrimPrefix(got, string(ReminderSentPrefix("u1"))); strings.Count(rest, "/") != 1 {
				t.Errorf("suffix %q has %d separators", rest, strings.Count(rest, "/"))
			}
		})
	}
	if string(ReminderSentKey("u1", "a", 1)) == string(ReminderSentKey("u1", "a", 2)) {
		t.Error("occurrences share a key")
	}
}

func TestReminderSentRecord(t *testing.T) {
	at := time.Unix(1893488400, 0)
	got, err := DecodeReminderSent(EncodeReminderSent(at))
	if err != nil || !got.Equal(at) {
		t.Fatalf("round trip = %v, %v", got, err)
	}
	for _, bad := range [][]byte{nil, []byte("xyz"), make([]byte, 9)} {
		if _, err := DecodeReminderSent(bad); err == nil {
			t.Errorf("decoded malformed %v", bad)
		}
	}
}

func TestOwnerPartstat(t *testing.T) {
	ev := &bongov1.Event{Attendees: []*bongov1.Attendee{
		{Address: "Other@Example.com", Partstat: "DECLINED"},
		{Address: "Me@Example.com", Partstat: "ACCEPTED"},
	}}
	if got := OwnerPartstat(ev, map[string]bool{"me@example.com": true}); got != "ACCEPTED" {
		t.Errorf("owner partstat = %q", got)
	}
	if got := OwnerPartstat(ev, map[string]bool{"nobody@example.com": true}); got != "" {
		t.Errorf("unmatched = %q", got)
	}
	if got := OwnerPartstat(ev, nil); got != "" {
		t.Errorf("nil set = %q", got)
	}
}
