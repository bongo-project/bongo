package store

import (
	"errors"
	"testing"
)

func TestEventStore_GetMissingMatchesBothSentinels(t *testing.T) {
	es := newEventTestEnv(t)
	_, err := es.Get("test-user-1", "missing")
	if !errors.Is(err, ErrEventNotFound) {
		t.Errorf("Get missing = %v, want ErrEventNotFound", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}
