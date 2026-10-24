package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSendLogRecordRoundTrip(t *testing.T) {
	var fp [32]byte
	for i := range fp {
		fp[i] = byte(i*7 + 3)
	}
	at := time.Date(2030, 5, 6, 13, 7, 41, 0, time.UTC)
	gotAt, gotFP, err := DecodeSendLogRecord(EncodeSendLogRecord(at, fp))
	if err != nil {
		t.Fatal(err)
	}
	if !gotAt.Equal(at) || gotFP != fp {
		t.Errorf("round trip = %v %x, want %v %x", gotAt, gotFP, at, fp)
	}
}

func TestDecodeSendLogRecordRejectsBadLength(t *testing.T) {
	for _, n := range []int{0, 1, SendLogRecordSize - 1, SendLogRecordSize + 1} {
		if _, _, err := DecodeSendLogRecord(make([]byte, n)); !errors.Is(err, ErrSendLogRecord) {
			t.Errorf("len %d: err = %v, want ErrSendLogRecord", n, err)
		}
	}
}

func TestSendLogKeys(t *testing.T) {
	id := strings.Repeat("c", 32)
	if got, want := string(SendLogKey("u1", id, "sent")), "/user/u1/sendlog/"+id+"/sent"; got != want {
		t.Errorf("key = %q, want %q", got, want)
	}
	if !strings.HasPrefix(string(SendLogKey("u1", id, "external")), string(SendLogPrefix("u1"))) {
		t.Error("key is not under the user's send log prefix")
	}
	// Case-insensitive, slash-safe, and distinct per address.
	a, b := SendLogLocalStep("Bob@Example.com"), SendLogLocalStep("bob@example.com")
	if a != b {
		t.Errorf("case changed the step: %q vs %q", a, b)
	}
	if SendLogLocalStep("a/b@example.com") == SendLogLocalStep("a@example.com") {
		t.Error("distinct addresses share a step")
	}
	if strings.Count(SendLogLocalStep("a/b@example.com"), "/") != 1 {
		t.Errorf("step %q leaks the address's slash", SendLogLocalStep("a/b@example.com"))
	}
}
