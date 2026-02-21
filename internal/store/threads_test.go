package store

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func storeTestMail(t *testing.T, ps *PebbleStore, userID string, meta *bongov1.MailMeta) {
	t.Helper()
	data, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	key := fmt.Sprintf("/user/%s/mail/%s", userID, meta.Guid)
	if err := ps.Set([]byte(key), data); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func TestBuildThread_ThreeMessageChain(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	userID := "u1"

	// M1 -> M2 -> M3 (chain)
	m1 := &bongov1.MailMeta{Guid: "m1", MessageId: "<m1@test>", Subject: "Thread Test", From: "alice", ReceivedAt: 100}
	m2 := &bongov1.MailMeta{Guid: "m2", MessageId: "<m2@test>", Subject: "Re: Thread Test", From: "bob", ReceivedAt: 200, InReplyTo: "<m1@test>", References: "<m1@test>"}
	m3 := &bongov1.MailMeta{Guid: "m3", MessageId: "<m3@test>", Subject: "Re: Thread Test", From: "alice", ReceivedAt: 300, InReplyTo: "<m2@test>", References: "<m1@test> <m2@test>"}

	storeTestMail(t, ps, userID, m1)
	storeTestMail(t, ps, userID, m2)
	storeTestMail(t, ps, userID, m3)

	ts := NewThreadStore(ps, nil)
	summary, err := ts.BuildThread(userID, "m3")
	if err != nil {
		t.Fatalf("BuildThread: %v", err)
	}

	if summary.Count != 3 {
		t.Errorf("count: got %d, want 3", summary.Count)
	}
	if summary.Root.GUID != "m1" {
		t.Errorf("root GUID: got %s, want m1", summary.Root.GUID)
	}
	if summary.Root.Depth != 0 {
		t.Errorf("root depth: got %d, want 0", summary.Root.Depth)
	}
	if len(summary.Root.Children) != 1 || summary.Root.Children[0].GUID != "m2" {
		t.Errorf("root children: expected [m2], got %v", summary.Root.Children)
	}
	if summary.Root.Children[0].Depth != 1 {
		t.Errorf("m2 depth: got %d, want 1", summary.Root.Children[0].Depth)
	}
	if len(summary.Root.Children[0].Children) != 1 || summary.Root.Children[0].Children[0].GUID != "m3" {
		t.Errorf("m2 children: expected [m3]")
	}
	if summary.Root.Children[0].Children[0].Depth != 2 {
		t.Errorf("m3 depth: got %d, want 2", summary.Root.Children[0].Children[0].Depth)
	}
}

func TestBuildThread_SingleMessage(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	userID := "u1"
	m1 := &bongov1.MailMeta{Guid: "single", MessageId: "<single@test>", Subject: "Solo", From: "alice", ReceivedAt: 100}
	storeTestMail(t, ps, userID, m1)

	ts := NewThreadStore(ps, nil)
	summary, err := ts.BuildThread(userID, "single")
	if err != nil {
		t.Fatalf("BuildThread: %v", err)
	}

	if summary.Count != 1 {
		t.Errorf("count: got %d, want 1", summary.Count)
	}
	if len(summary.Root.Children) != 0 {
		t.Errorf("expected no children, got %d", len(summary.Root.Children))
	}
}

func TestBuildThread_BranchingReplies(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	userID := "u1"

	// M1 is root, M2a and M2b both reply to M1.
	m1 := &bongov1.MailMeta{Guid: "m1", MessageId: "<m1@test>", Subject: "Discussion", From: "alice", ReceivedAt: 100}
	m2a := &bongov1.MailMeta{Guid: "m2a", MessageId: "<m2a@test>", Subject: "Re: Discussion", From: "bob", ReceivedAt: 200, InReplyTo: "<m1@test>"}
	m2b := &bongov1.MailMeta{Guid: "m2b", MessageId: "<m2b@test>", Subject: "Re: Discussion", From: "carol", ReceivedAt: 250, InReplyTo: "<m1@test>"}

	storeTestMail(t, ps, userID, m1)
	storeTestMail(t, ps, userID, m2a)
	storeTestMail(t, ps, userID, m2b)

	ts := NewThreadStore(ps, nil)
	summary, err := ts.BuildThread(userID, "m2b")
	if err != nil {
		t.Fatalf("BuildThread: %v", err)
	}

	if summary.Count != 3 {
		t.Errorf("count: got %d, want 3", summary.Count)
	}
	if summary.Root.GUID != "m1" {
		t.Errorf("root: got %s, want m1", summary.Root.GUID)
	}
	if len(summary.Root.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(summary.Root.Children))
	}
	// Should be sorted by ReceivedAt: m2a (200) before m2b (250).
	if summary.Root.Children[0].GUID != "m2a" || summary.Root.Children[1].GUID != "m2b" {
		t.Errorf("children order: got [%s, %s], want [m2a, m2b]",
			summary.Root.Children[0].GUID, summary.Root.Children[1].GUID)
	}
}

func TestGetThreadRoot(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	userID := "u1"
	m1 := &bongov1.MailMeta{Guid: "m1", MessageId: "<m1@test>", Subject: "Thread", From: "alice", ReceivedAt: 100}
	m2 := &bongov1.MailMeta{Guid: "m2", MessageId: "<m2@test>", Subject: "Re: Thread", From: "bob", ReceivedAt: 200, InReplyTo: "<m1@test>"}
	storeTestMail(t, ps, userID, m1)
	storeTestMail(t, ps, userID, m2)

	ts := NewThreadStore(ps, nil)

	root1, _ := ts.GetThreadRoot(userID, "m1")
	if root1 != "m1" {
		t.Errorf("root of m1: got %s, want m1", root1)
	}

	root2, _ := ts.GetThreadRoot(userID, "m2")
	if root2 != "m1" {
		t.Errorf("root of m2: got %s, want m1", root2)
	}
}
