package imap

import (
	"fmt"
	"testing"

	"github.com/emersion/go-imap/v2/imapclient"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func (e *revokeEnv) setDisabledFeatures(t *testing.T, features ...string) {
	t.Helper()
	key := []byte(fmt.Sprintf("/user/%s/profile", pollTestUser))
	data, err := e.ps.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	p.DisabledFeatures = features
	if data, err = proto.Marshal(&p); err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set(key, data); err != nil {
		t.Fatal(err)
	}
}

// Switching imap off ends a live session on the next command; switching
// another protocol off does not.
func TestIMAPSessionEndsWhenFeatureDisabled(t *testing.T) {
	e := newRevokeEnv(t)
	addr := startRevokeServer(t, e)
	c := dialPollTestClient(t, addr, nil)
	if err := c.Noop().Wait(); err != nil {
		t.Fatal(err)
	}
	e.setDisabledFeatures(t, store.FeaturePOP3)
	if err := c.Noop().Wait(); err != nil {
		t.Fatalf("NOOP after an unrelated switch: %v", err)
	}
	e.setDisabledFeatures(t, store.FeaturePOP3, store.FeatureIMAP)
	if err := c.Noop().Wait(); err == nil {
		t.Fatal("NOOP succeeded after imap was switched off")
	}
	if err := c.Noop().Wait(); err == nil {
		t.Fatal("connection still usable")
	}
}

// A login to a disabled protocol fails with the same error text as a wrong
// password.
func TestIMAPLoginFeatureDisabledMatchesWrongPassword(t *testing.T) {
	e := newRevokeEnv(t)
	e.setDisabledFeatures(t, store.FeatureIMAP)
	addr := startRevokeServer(t, e)
	login := func(pw string) string {
		c, err := imapclient.DialInsecure(addr, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		err = c.Login(pollTestUsername, pw).Wait()
		if err == nil {
			t.Fatal("login succeeded")
		}
		return err.Error()
	}
	if a, b := login(pollTestPassword), login("not-the-password"); a != b {
		t.Fatalf("disabled-feature reply %q differs from wrong-password reply %q", a, b)
	}
}
