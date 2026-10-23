package auth

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

func captureAuthLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := logger
	logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { logger = old })
	return &buf
}

func seedAddressRecord(t *testing.T, app RaftApplier, mm *store.MetadataManager, address string, rec *bongov1.AddressRecord) {
	t.Helper()
	if err := store.NewAddressStore(nil, app, mm.System).Put(address, rec); err != nil {
		t.Fatalf("seed %s: %v", address, err)
	}
}

func TestCreateUser_ReplacesUnverifiedIdentityClaim(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	logs := captureAuthLog(t)
	const addr = "new@future.example"
	seedAddressRecord(t, app, mm, addr, &bongov1.AddressRecord{
		UserId: "user-A", Domain: "future.example", Kind: "identity",
	})

	id, err := CreateUser(app, mm, addr, "New", "secret123", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(kv.data["/system/address/"+addr], &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Kind != "primary" || rec.UserId != id || !rec.Verified || !rec.CanSend {
		t.Errorf("pebble record = %+v, want verified primary for %s", &rec, id)
	}
	var kind, owner string
	if err := mm.System.DB().QueryRow(`SELECT kind, user_id FROM addresses WHERE address = ?`, addr).Scan(&kind, &owner); err != nil {
		t.Fatal(err)
	}
	if kind != "primary" || owner != id {
		t.Errorf("sqlite row = (%s, %s), want (primary, %s)", kind, owner, id)
	}
	if list, _ := store.NewAddressStore(nil, app, mm.System).ListByUser("user-A"); len(list) != 0 {
		t.Errorf("user-A still lists %v", list)
	}

	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, addr) || !strings.Contains(out, "user-A") {
		t.Errorf("log %q does not warn with the address and previous owner", out)
	}
}

func TestCreateUser_ReplacesUnverifiedClaimKnownOnlyToSQLite(t *testing.T) {
	// A follower-style view: no Pebble reader, only the derived index.
	_, mm, _ := setupTestAuth(t)
	logs := captureAuthLog(t)
	if _, err := mm.System.DB().Exec(`INSERT INTO addresses (address, user_id, domain, kind, can_send, verified, created_at)
		VALUES ('only@future.example', 'user-A', 'future.example', 'identity', 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateUser(noReadApplier{&recordingApplier{kv: &mockKeyGetter{data: map[string][]byte{}}}}, mm, "only@future.example", "N", "secret123", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !strings.Contains(logs.String(), "user-A") {
		t.Errorf("log %q lacks the previous owner", logs.String())
	}
}

func TestCreateUser_KeepsProtectedAddressRecords(t *testing.T) {
	tests := []struct {
		name string
		rec  *bongov1.AddressRecord
	}{
		{"verified identity", &bongov1.AddressRecord{UserId: "user-A", Kind: "identity", Verified: true, CanSend: true}},
		{"unverified alias", &bongov1.AddressRecord{UserId: "user-A", Kind: "alias"}},
		{"unverified primary", &bongov1.AddressRecord{UserId: "user-A", Kind: "primary"}},
	}
	for _, tt := range tests {
		for _, pebbleOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pebbleOnly=%v", tt.name, pebbleOnly), func(t *testing.T) {
				_, mm, kv := setupTestAuth(t)
				app := &recordingApplier{kv: kv}
				tt.rec.Domain = "future.example"
				if pebbleOnly {
					raw, err := proto.Marshal(tt.rec)
					if err != nil {
						t.Fatal(err)
					}
					kv.data["/system/address/new@future.example"] = raw
				} else {
					seedAddressRecord(t, app, mm, "new@future.example", tt.rec)
				}
				before := bytes.Clone(kv.data["/system/address/new@future.example"])
				app.ops = nil

				_, err := CreateUser(app, mm, "new@future.example", "N", "secret123", false)
				if !errors.Is(err, ErrAccountExists) {
					t.Fatalf("err = %v, want ErrAccountExists", err)
				}
				if len(app.ops) != 0 {
					t.Errorf("ops = %v, want none", app.ops)
				}
				if !bytes.Equal(before, kv.data["/system/address/new@future.example"]) {
					t.Error("record changed")
				}
			})
		}
	}
}

// TestCreateUser_KeepsProtectedAddressRecordsKnownOnlyToSQLite covers the
// follower-style view: with no Pebble reader the SQLite query alone must still
// refuse every record that is not an unverified identity.
func TestCreateUser_KeepsProtectedAddressRecordsKnownOnlyToSQLite(t *testing.T) {
	tests := []struct {
		name, kind string
		verified   int
	}{
		{"verified identity", "identity", 1},
		{"unverified alias", "alias", 0},
		{"unverified primary", "primary", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mm, _ := setupTestAuth(t)
			if _, err := mm.System.DB().Exec(`INSERT INTO addresses (address, user_id, domain, kind, can_send, verified, created_at)
				VALUES ('new@future.example', 'user-A', 'future.example', ?, ?, ?, 0)`, tt.kind, tt.verified, tt.verified); err != nil {
				t.Fatal(err)
			}
			app := &recordingApplier{kv: &mockKeyGetter{data: map[string][]byte{}}}

			_, err := CreateUser(noReadApplier{app}, mm, "new@future.example", "N", "secret123", false)
			if !errors.Is(err, ErrAccountExists) {
				t.Fatalf("err = %v, want ErrAccountExists", err)
			}
			if len(app.ops) != 0 {
				t.Errorf("ops = %v, want none", app.ops)
			}
		})
	}
}

// noReadApplier hides GetKey so only the SQLite checks run.
type noReadApplier struct{ a *recordingApplier }

func (n noReadApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	return n.a.ApplyCommand(cmd, d)
}

func TestCreateUser_ReplacesUnverifiedClaimKnownOnlyToPebble(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	raw, err := proto.Marshal(&bongov1.AddressRecord{UserId: "user-A", Kind: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	kv.data["/system/address/p@future.example"] = raw
	if _, err := CreateUser(app, mm, "p@future.example", "P", "secret123", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
}

// CheckAddressFreeFor serves the send-as gate, admin add-address and alias
// creation; only CreateUser may treat another user's unverified identity
// record as free.
func TestCheckAddressFreeFor_StillRefusesOthersUnverifiedIdentity(t *testing.T) {
	for _, pebbleOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("pebbleOnly=%v", pebbleOnly), func(t *testing.T) {
			_, mm, kv := setupTestAuth(t)
			app := &recordingApplier{kv: kv}
			rec := &bongov1.AddressRecord{UserId: "user-A", Domain: "future.example", Kind: "identity"}
			const addr = "held@future.example"
			if pebbleOnly {
				raw, err := proto.Marshal(rec)
				if err != nil {
					t.Fatal(err)
				}
				kv.data["/system/address/"+addr] = raw
			} else {
				seedAddressRecord(t, app, mm, addr, rec)
			}
			if err := CheckAddressFreeFor(app, mm, addr, "user-B"); !errors.Is(err, ErrAccountExists) {
				t.Errorf("CheckAddressFreeFor err = %v, want ErrAccountExists", err)
			}
			if err := CheckAddressFree(app, mm, addr); !errors.Is(err, ErrAccountExists) {
				t.Errorf("CheckAddressFree err = %v, want ErrAccountExists", err)
			}
		})
	}
}

func TestCreateUser_DeletesReplacedOwnersClaim(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	const addr = "new@future.example"
	seedAddressRecord(t, app, mm, addr, &bongov1.AddressRecord{UserId: "user-A", Domain: "future.example", Kind: "identity"})
	kv.data["/user/user-A/addrverify/"+addr] = []byte("challenge")
	if _, err := CreateUser(app, mm, addr, "N", "secret123", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := kv.data["/user/user-A/addrverify/"+addr]; ok {
		t.Error("replaced owner's challenge key survives")
	}
}
