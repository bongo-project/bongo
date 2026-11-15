package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	xferSrc  = "alice"
	xferDst  = "bob"
	xferHash = "abc123blobhash"
)

// newXferEnv holds alice's INBOX with two messages (the first tagged and
// flagged) and bob's empty Box, whose uid_next is already 7 so that a copied uid
// can only come from the destination's counter.
func newXferEnv(t *testing.T) (*MailboxStore, *PebbleStore) {
	t.Helper()
	ms := newMailboxTestEnv(t)
	for _, c := range [][2]string{{xferSrc, "INBOX"}, {xferDst, "Box"}} {
		if err := ms.CreateMailbox(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		if _, err := ms.DeliverMessage(xferDst, "Box", &bongov1.MailMeta{Guid: fmt.Sprintf("old%d", i)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2 {
		_, err := ms.DeliverMessage(xferSrc, "INBOX", &bongov1.MailMeta{
			Guid:       fmt.Sprintf("src%d", i),
			Subject:    "hello",
			ReceivedAt: 1000,
			Tags:       []string{"private"},
			FolderGuid: "src-folder-guid", TargetMailbox: "SourceOnly",
			RawMessage: &bongov1.BlobRef{Hash: xferHash, Size: 42},
		}, FlagSeen|FlagFlagged|FlagRecent)
		if err != nil {
			t.Fatal(err)
		}
	}
	return ms, ms.store
}

func xferKeyCount(t *testing.T, ps *PebbleStore) int {
	t.Helper()
	iter, err := ps.PrefixScan([]byte("/"))
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	n := 0
	for iter.Next() {
		n++
	}
	return n
}

func xferGUID(n int) string { return fmt.Sprintf("%032x", n) }

func xferCmd(t *testing.T, dst string, p mailboxXferPayload) *bongov1.RaftCommand {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: mailboxTransferXKey(dst), Value: b}
}

func xferPayload(move bool, mask uint32, uids ...uint32) mailboxXferPayload {
	p := mailboxXferPayload{SrcUser: xferSrc, SrcMailbox: "INBOX", DstMailbox: "Box", Move: move, FlagMask: mask}
	for i, u := range uids {
		p.Items = append(p.Items, mailboxXferItem{SrcUID: u, NewGUID: xferGUID(i + 1)})
	}
	return p
}

func TestMailboxTransferXCopyAndMove(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(fmt.Sprintf("move=%v", move), func(t *testing.T) {
			ms, ps := newXferEnv(t)
			res, err := ApplyMailboxCommand(ps, xferCmd(t, xferDst, xferPayload(move, FlagSeen, 1, 2)))
			if err != nil {
				t.Fatal(err)
			}

			if len(res.Arrivals) != 2 || len(res.NewMails) != 2 {
				t.Fatalf("arrivals = %d, new mails = %d, want 2 and 2", len(res.Arrivals), len(res.NewMails))
			}
			if res.NewMail != nil {
				t.Fatal("NewMail set: Emit would announce twice")
			}
			if res.Arrivals[0] != (MailboxMessageRef{UserID: xferDst, Mailbox: "Box", UID: 7}) ||
				res.Arrivals[1].UID != 8 {
				t.Fatalf("arrivals = %+v, want uids 7 and 8 from the destination's uid_next", res.Arrivals)
			}
			wantExpunged := 0
			if move {
				wantExpunged = 2
			}
			if len(res.Expunged) != wantExpunged {
				t.Fatalf("expunged = %d, want %d", len(res.Expunged), wantExpunged)
			}

			// The destination record is a new guid on the same blob, with no tags.
			data, err := ps.Get(mailMetaKey(xferDst, xferGUID(1)))
			if err != nil {
				t.Fatalf("destination MailMeta: %v", err)
			}
			var got bongov1.MailMeta
			if err := proto.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Guid != xferGUID(1) || got.RawMessage.GetHash() != xferHash || len(got.Tags) != 0 || got.Subject != "hello" {
				t.Fatalf("destination meta = guid %q hash %q tags %v", got.Guid, got.RawMessage.GetHash(), got.Tags)
			}

			if got.FolderGuid != "" || got.TargetMailbox != "" {
				t.Fatalf("destination meta kept the source namespace: folder_guid %q target_mailbox %q", got.FolderGuid, got.TargetMailbox)
			}

			// Flags are masked and \Recent cleared; the date is kept.
			msg, err := ms.GetMessage(xferDst, "Box", 7)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Flags != FlagSeen || msg.MailGuid != xferGUID(1) || msg.InternalDate != 1000 || len(msg.Tags) != 0 {
				t.Fatalf("destination message = flags %d guid %q date %d tags %v", msg.Flags, msg.MailGuid, msg.InternalDate, msg.Tags)
			}
			if mb, _ := ms.GetMailbox(xferDst, "Box"); mb.UidNext != 9 {
				t.Fatalf("uid_next = %d, want 9", mb.UidNext)
			}

			// The source MailMeta, with its tags, is untouched either way; only
			// the MailboxMessage of a MOVE goes.
			if _, err := ps.Get(mailMetaKey(xferSrc, "src0")); err != nil {
				t.Fatalf("source MailMeta: %v", err)
			}
			_, srcErr := ms.GetMessage(xferSrc, "INBOX", 1)
			if move != (srcErr != nil) {
				t.Fatalf("move=%v but source message error = %v", move, srcErr)
			}
		})
	}
}

func TestMailboxTransferXRefusalsWriteNothing(t *testing.T) {
	tests := []struct {
		name  string
		dst   string
		patch func(p *mailboxXferPayload)
		want  string
	}{
		{"missing destination mailbox", xferDst, func(p *mailboxXferPayload) { p.DstMailbox = "Nope" }, "dest mailbox"},
		{"missing source uid", xferDst, func(p *mailboxXferPayload) { p.Items[0].SrcUID = 99 }, "source message"},
		{"existing destination guid", xferDst, func(p *mailboxXferPayload) {}, "already exists"},
		{"bad guid", xferDst, func(p *mailboxXferPayload) { p.Items[0].NewGUID = "../x" }, "invalid"},
		{"short guid", xferDst, func(p *mailboxXferPayload) { p.Items[0].NewGUID = "abc" }, "invalid"},
		{"duplicate guid", xferDst, func(p *mailboxXferPayload) { p.Items[1].NewGUID = p.Items[0].NewGUID }, "invalid"},
		{"duplicate source uid", xferDst, func(p *mailboxXferPayload) { p.Items[1].SrcUID = p.Items[0].SrcUID }, "invalid"},
		{"same owner", xferSrc, func(p *mailboxXferPayload) {}, "two different owners"},
		{"slash in destination owner", "bo/b", func(p *mailboxXferPayload) {}, "invalid mailbox transfer owner"},
		{"empty source owner", xferDst, func(p *mailboxXferPayload) { p.SrcUser = "" }, "invalid mailbox transfer owner"},
		{"no items", xferDst, func(p *mailboxXferPayload) { p.Items = nil }, "items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, ps := newXferEnv(t)
			if tt.name == "existing destination guid" {
				if err := ps.Set(mailMetaKey(xferDst, xferGUID(1)), mustMarshalMeta(t, &bongov1.MailMeta{Guid: xferGUID(1)})); err != nil {
					t.Fatal(err)
				}
			}
			before := xferKeyCount(t, ps)

			p := xferPayload(true, FlagSeen, 1, 2)
			tt.patch(&p)
			_, err := ApplyMailboxCommand(ps, xferCmd(t, tt.dst, p))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
			if after := xferKeyCount(t, ps); after != before {
				t.Fatalf("a refused transfer changed the key count: %d -> %d", before, after)
			}
			if mb, _ := ms.GetMailbox(xferDst, "Box"); mb.UidNext != 7 {
				t.Fatalf("uid_next = %d, want 7", mb.UidNext)
			}
			if _, err := ms.GetMessage(xferSrc, "INBOX", 1); err != nil {
				t.Fatalf("a refused MOVE removed the source: %v", err)
			}
		})
	}
}

func mustMarshalMeta(t *testing.T, m *bongov1.MailMeta) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A source whose MailMeta is gone cannot be copied: the destination record is
// built from it, so there is nothing to build from.
func TestMailboxTransferXMissingSourceMetaWritesNothing(t *testing.T) {
	_, ps := newXferEnv(t)
	if err := ps.Delete(mailMetaKey(xferSrc, "src1")); err != nil {
		t.Fatal(err)
	}
	before := xferKeyCount(t, ps)
	if _, err := ApplyMailboxCommand(ps, xferCmd(t, xferDst, xferPayload(false, FlagSeen, 1, 2))); err == nil {
		t.Fatal("expected a refusal")
	}
	if after := xferKeyCount(t, ps); after != before {
		t.Fatalf("key count %d -> %d", before, after)
	}
}

func TestMailboxTransferXItemCap(t *testing.T) {
	_, ps := newXferEnv(t)
	p := mailboxXferPayload{SrcUser: xferSrc, SrcMailbox: "INBOX", DstMailbox: "Box"}
	for i := range MaxMailboxTransferItems + 1 {
		p.Items = append(p.Items, mailboxXferItem{SrcUID: uint32(i + 1), NewGUID: xferGUID(i + 1)})
	}
	before := xferKeyCount(t, ps)
	if _, err := ApplyMailboxCommand(ps, xferCmd(t, xferDst, p)); err == nil || !strings.Contains(err.Error(), "items") {
		t.Fatalf("err = %v, want an item cap refusal", err)
	}
	if after := xferKeyCount(t, ps); after != before {
		t.Fatalf("key count %d -> %d", before, after)
	}
}

func TestIsMailboxSyntheticCommandCoversTransferX(t *testing.T) {
	if !IsMailboxSyntheticCommand(mailboxTransferXKey("bob")) {
		t.Fatal("the transfer prefix is not dispatched with the mailbox family")
	}
}

func TestTransferMessagesMapsUIDsByGUID(t *testing.T) {
	ms, ps := newXferEnv(t)
	pairs, err := ms.TransferMessages(xferSrc, "INBOX", xferDst, "Box", []uint32{2, 1}, false, FlagSeen)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0] != (TransferredUID{SrcUID: 2, DstUID: 7}) || pairs[1] != (TransferredUID{SrcUID: 1, DstUID: 8}) {
		t.Fatalf("pairs = %+v", pairs)
	}
	m, err := ms.GetMessage(xferDst, "Box", 7)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ps.Get(mailMetaKey(xferDst, m.MailGuid))
	if err != nil {
		t.Fatal(err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil || meta.Guid != m.MailGuid {
		t.Fatalf("meta = %v, %v", meta.Guid, err)
	}
}

func TestTransferMessagesSplitsAtTheItemCap(t *testing.T) {
	ms := newMailboxTestEnv(t)
	for _, c := range [][2]string{{xferSrc, "INBOX"}, {xferDst, "Box"}} {
		if err := ms.CreateMailbox(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	n := MaxMailboxTransferItems + 3
	var uids []uint32
	for i := range n {
		uid, err := ms.DeliverMessage(xferSrc, "INBOX", &bongov1.MailMeta{Guid: fmt.Sprintf("s%d", i), RawMessage: &bongov1.BlobRef{Hash: xferHash}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		uids = append(uids, uid)
	}
	pairs, err := ms.TransferMessages(xferSrc, "INBOX", xferDst, "Box", uids, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != n {
		t.Fatalf("pairs = %d, want %d", len(pairs), n)
	}
	if msgs, _ := ms.ListMessages(xferDst, "Box"); len(msgs) != n {
		t.Fatalf("destination holds %d messages, want %d", len(msgs), n)
	}
}
