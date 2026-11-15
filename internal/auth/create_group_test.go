package auth

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A group's address record has no user_id, so the owner-exemption branches
// must not treat it as anyone's: it is taken for every caller.
func TestAddressFreeChecksTreatGroupAsTaken(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	const addr = "team@example.com"
	raw, err := proto.Marshal(&bongov1.AddressRecord{Kind: "group", GroupId: "g1", Domain: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	kv.data["/system/address/"+addr] = raw

	if err := CheckAddressFree(app, mm, addr); !errors.Is(err, ErrAccountExists) {
		t.Errorf("CheckAddressFree = %v, want ErrAccountExists", err)
	}
	for _, owner := range []string{"", "me", "g1"} {
		if err := CheckAddressFreeFor(app, mm, addr, owner); !errors.Is(err, ErrAccountExists) {
			t.Errorf("CheckAddressFreeFor(%q) = %v, want ErrAccountExists", owner, err)
		}
	}
	if _, err := CreateUser(app, mm, addr, "Team", "secret123", false); !errors.Is(err, ErrAccountExists) {
		t.Errorf("CreateUser = %v, want ErrAccountExists", err)
	}
	if got := kv.data["/system/address/"+addr]; string(got) != string(raw) {
		t.Error("CreateUser changed the group's address record")
	}
}
