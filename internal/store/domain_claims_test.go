package store

import (
	"slices"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestDomainClaimKeys(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	ap := &directApplier{store: ps}

	all := []string{
		"/user/u1/addrverify/x@d.test",
		"/user/u2/addrverify/x@d.test",
		"/user/u1/addrverify/y@other.test",
		"/user/u1/addrverify/z@sub.d.test",
		"/user/u1/addrverify/x@notd.test",
		// Different segment: forwarding verification is not a send-as claim.
		"/user/u1/fwdverify/x@d.test",
		// A mail key whose guid mentions addrverify must not be parsed as a claim.
		"/user/u1/mail/addrverify/x@d.test",
		"/user/u1/mailbox/addrverify/msg/x@d.test",
		// A key ending in the suffix but not under a user.
		"/system/address/x@d.test",
	}
	for _, k := range all {
		if err := ap.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(k), Value: []byte("v")}, time.Second); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}

	got, err := domainClaimKeys(ps, "@d.test")
	if err != nil {
		t.Fatalf("domainClaimKeys: %v", err)
	}
	var gotS []string
	for _, k := range got {
		gotS = append(gotS, string(k))
	}
	slices.Sort(gotS)
	want := []string{"/user/u1/addrverify/x@d.test", "/user/u2/addrverify/x@d.test"}
	if !slices.Equal(gotS, want) {
		t.Errorf("domainClaimKeys = %v, want %v", gotS, want)
	}
}
