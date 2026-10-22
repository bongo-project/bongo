package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCreateUser_WritesPrimaryAddressRecord(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}

	id, err := CreateUser(app, mm, "New@Example.com", "New", "secret123", false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	raw, ok := kv.data["/system/address/new@example.com"]
	if !ok {
		t.Fatal("no /system/address/ record written")
	}
	var rec bongov1.AddressRecord
	if err := proto.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.UserId != id || rec.Kind != "primary" || !rec.Verified || !rec.CanSend || rec.Domain != "example.com" {
		t.Errorf("record = %+v, want primary verified can_send for %s", &rec, id)
	}
	var n int
	if err := mm.System.DB().QueryRow(`SELECT count(*) FROM addresses WHERE address = 'new@example.com' AND kind = 'primary'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("addresses table rows = %d, err = %v", n, err)
	}
}

type failAddressApplier struct{ *recordingApplier }

func (f *failAddressApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && strings.HasPrefix(string(cmd.Key), "/system/address/") {
		f.ops = append(f.ops, "FAILED "+cmd.Op.String()+" "+string(cmd.Key))
		return errors.New("injected raft failure")
	}
	return f.recordingApplier.ApplyCommand(cmd, d)
}

func TestCreateUser_AddressWriteFailureCompensates(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &failAddressApplier{&recordingApplier{kv: kv}}

	_, err := CreateUser(app, mm, "fail@example.com", "F", "secret123", false)
	if err == nil || !strings.Contains(err.Error(), "injected raft failure") {
		t.Fatalf("err = %v, want injected failure", err)
	}
	var deletedProfile bool
	for _, op := range app.ops {
		if strings.HasPrefix(op, "OP_DELETE /user/") && strings.HasSuffix(op, "/profile") {
			deletedProfile = true
		}
	}
	if !deletedProfile {
		t.Errorf("profile delete not attempted; ops = %v", app.ops)
	}
	for k := range kv.data {
		if strings.HasSuffix(k, "/profile") {
			t.Errorf("profile %s left behind", k)
		}
	}
	var n int
	if err := mm.System.DB().QueryRow(`SELECT count(*) FROM users WHERE username = 'fail@example.com'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("users row left behind: n=%d err=%v", n, err)
	}
}

func TestCheckAddressFreeFor_OwnerExemption(t *testing.T) {
	_, mm, kv := setupTestAuth(t)
	app := &recordingApplier{kv: kv}
	put := func(addr, uid string) {
		raw, err := proto.Marshal(&bongov1.AddressRecord{UserId: uid, Kind: "alias", Verified: true})
		if err != nil {
			t.Fatal(err)
		}
		kv.data["/system/address/"+addr] = raw
	}
	put("mine@example.com", "me")
	put("theirs@example.com", "them")

	tests := []struct {
		name    string
		addr    string
		owner   string
		wantErr bool
	}{
		{"own record is exempt", "mine@example.com", "me", false},
		{"other owner's record counts", "theirs@example.com", "me", true},
		{"no owner counts every record", "mine@example.com", "", true},
		{"unknown address is free", "free@example.com", "me", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckAddressFreeFor(app, mm, tt.addr, tt.owner)
			if tt.wantErr && !errors.Is(err, ErrAccountExists) {
				t.Errorf("err = %v, want ErrAccountExists", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}
