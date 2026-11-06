package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ticketApplier applies as the FSM does: ticket consumes are conditional and
// every apply is serialised, as Raft's single goroutine does.
type ticketApplier struct {
	mu sync.Mutex
	ps *PebbleStore
}

func (a *ticketApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cmd.Op == bongov1.Op_OP_PUT && IsAppPasswordTicketCommand(cmd.Key) {
		return ApplyAppPasswordTicketConsume(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

func newTicketStore(t *testing.T) (*DeviceLinkStore, *PebbleStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewDeviceLinkStore(ps, &ticketApplier{ps: ps}), ps
}

func TestDeviceTokenShape(t *testing.T) {
	tok, hash, err := NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 43 || !IsValidHash(hash) {
		t.Fatalf("token %d chars, hash %q", len(tok), hash)
	}
	if strings.Contains(hash, tok) {
		t.Error("hash contains the token")
	}
	if got, ok := DeviceTokenHash(tok); !ok || got != hash {
		t.Errorf("DeviceTokenHash = %q, %v", got, ok)
	}
	for name, bad := range map[string]string{
		"empty":       "",
		"short":       tok[:42],
		"long":        tok + "A",
		"slash":       tok[:42] + "/",
		"dot dot":     strings.Repeat("A", 41) + "..",
		"space":       tok[:42] + " ",
		"padding":     tok[:42] + "=",
		"non-ascii":   tok[:41] + "é",
		"upper hash":  strings.ToUpper(hash),
		"hash as tok": hash,
	} {
		if name == "upper hash" {
			if IsValidHash(bad) {
				t.Errorf("%s accepted as a hash", name)
			}
			continue
		}
		if _, ok := DeviceTokenHash(bad); ok {
			t.Errorf("%s accepted as a token", name)
		}
	}
}

func TestDeviceLinkKeyValidation(t *testing.T) {
	_, hash, _ := NewDeviceToken()
	for name, user := range map[string]string{"empty": "", "slash": "a/b", "traversal": "../x"} {
		if _, err := DeviceLinkKey(user, hash); !errors.Is(err, ErrInvalidDeviceLink) {
			t.Errorf("%s user: err = %v", name, err)
		}
	}
	if _, err := DeviceLinkKey("u1", "zz"); !errors.Is(err, ErrInvalidDeviceLink) {
		t.Errorf("bad hash: err = %v", err)
	}
	k, err := DeviceLinkKey("u1", hash)
	if err != nil || string(k) != "/user/u1/devicelink/"+hash {
		t.Errorf("key = %q, %v", k, err)
	}
	if strings.HasPrefix(string(k), string(appPasswordPrefix("u1"))) {
		t.Error("a link key is under the app password prefix")
	}
}

func TestPlanAppPasswordTicketConsume(t *testing.T) {
	const expires = 2_000_000
	_, hash, _ := NewDeviceToken()
	tests := []struct {
		name    string
		rec     *bongov1.AppPasswordTicket // nil means absent
		at      int64
		refused bool
	}{
		{"unused, one second before expiry", &bongov1.AppPasswordTicket{ExpiresAt: expires}, expires - 1, false},
		{"unused, exactly at expiry", &bongov1.AppPasswordTicket{ExpiresAt: expires}, expires, true},
		{"unused, after expiry", &bongov1.AppPasswordTicket{ExpiresAt: expires}, expires + 100, true},
		{"already used", &bongov1.AppPasswordTicket{ExpiresAt: expires, UsedAt: expires - 50}, expires - 1, true},
		{"absent", nil, expires - 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links, ps := newTicketStore(t)
			key, _ := DeviceLinkKey("u1", hash)
			if tt.rec != nil {
				raw, _ := proto.Marshal(tt.rec)
				if err := ps.Set(key, raw); err != nil {
					t.Fatal(err)
				}
			}
			cmd, err := BuildAppPasswordTicketConsumeCommand("u1", hash, tt.at)
			if err != nil {
				t.Fatal(err)
			}
			k, v, err := PlanAppPasswordTicketConsume(ps.Get, cmd)
			if tt.refused {
				if !errors.Is(err, ErrTicketRefused) || k != nil || v != nil {
					t.Fatalf("plan = %q %q %v, want a refusal that writes nothing", k, v, err)
				}
				return
			}
			if err != nil || string(k) != string(key) {
				t.Fatalf("plan = %q %v", k, err)
			}
			var got bongov1.AppPasswordTicket
			if err := proto.Unmarshal(v, &got); err != nil || got.UsedAt != tt.at {
				t.Errorf("used_at = %d (%v), want %d", got.UsedAt, err, tt.at)
			}
			// The winner changes nothing else, and the second consume loses.
			if err := ps.Set(k, v); err != nil {
				t.Fatal(err)
			}
			if _, _, err := PlanAppPasswordTicketConsume(ps.Get, cmd); !errors.Is(err, ErrTicketRefused) {
				t.Errorf("second plan = %v, want a refusal", err)
			}
			_ = links
		})
	}
}

func TestPlanAppPasswordTicketConsumeRejectsBadPayload(t *testing.T) {
	_, ps := newTicketStore(t)
	_, hash, _ := NewDeviceToken()
	if _, err := BuildAppPasswordTicketConsumeCommand("a/b", hash, 5); err == nil {
		t.Error("slash user built")
	}
	if _, err := BuildAppPasswordTicketConsumeCommand("u1", "short", 5); err == nil {
		t.Error("short hash built")
	}
	if _, err := BuildAppPasswordTicketConsumeCommand("u1", hash, 0); err == nil {
		t.Error("zero time built")
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(appPasswordTicketPrefix + "consume"), Value: []byte(`{"user":"u1","hash":"x","at":5}`)}
	if _, _, err := PlanAppPasswordTicketConsume(ps.Get, cmd); err == nil || errors.Is(err, ErrTicketRefused) {
		t.Errorf("bad payload err = %v, want a validation error", err)
	}
	if IsAppPasswordSyntheticCommand(cmd.Key) {
		t.Error("the touch predicate matches a ticket command; appliers would double-dispatch")
	}
}

// Sixteen concurrent consumes through an applier that serialises as Raft does:
// exactly one wins.
func TestDeviceLinkConsumeOnce(t *testing.T) {
	links, _ := newTicketStore(t)
	_, hash, _ := NewDeviceToken()
	now := time.Unix(1_700_000_000, 0)
	if err := links.Put("u1", hash, &bongov1.AppPasswordTicket{ExpiresAt: now.Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	var wins, refused, other int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := links.Consume("u1", hash, now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrTicketRefused):
				refused++
			default:
				other++
			}
		}()
	}
	wg.Wait()
	if wins != 1 || refused != 15 || other != 0 {
		t.Errorf("wins %d refused %d other %d, want 1 15 0", wins, refused, other)
	}
}

func TestDeviceLinkDeleteAllIsScopedToTheUser(t *testing.T) {
	links, _ := newTicketStore(t)
	rec := &bongov1.AppPasswordTicket{ExpiresAt: 99}
	var mine, theirs string
	for range 3 {
		_, h, _ := NewDeviceToken()
		mine = h
		if err := links.Put("u1", h, rec); err != nil {
			t.Fatal(err)
		}
	}
	_, theirs, _ = NewDeviceToken()
	if err := links.Put("u1x", theirs, rec); err != nil {
		t.Fatal(err)
	}
	if err := links.DeleteAll("u1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := links.Get("u1", mine); found {
		t.Error("a link survived DeleteAll")
	}
	if _, found, _ := links.Get("u1x", theirs); !found {
		t.Error("DeleteAll for u1 removed a link of u1x")
	}
}
