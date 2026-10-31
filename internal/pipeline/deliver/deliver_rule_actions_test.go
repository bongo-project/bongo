package deliver

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// delivery is one decoded mailbox delivery command.
type delivery struct {
	mailbox string
	flags   uint32
	meta    *bongov1.MailMeta
}

func decodeDeliveries(t *testing.T, applier *mockApplier) []delivery {
	t.Helper()
	var out []delivery
	for _, cmd := range applier.commands {
		key := string(cmd.Key)
		if !strings.HasPrefix(key, "/__mailbox_deliver__/") {
			continue
		}
		var payload struct {
			Flags uint32 `json:"flags"`
		}
		if err := json.Unmarshal(cmd.Value, &payload); err != nil {
			t.Fatalf("payload: %v", err)
		}
		parts := strings.SplitN(strings.TrimPrefix(key, "/__mailbox_deliver__/"), "/", 2)
		out = append(out, delivery{mailbox: parts[1], flags: payload.Flags, meta: unmarshalMailMeta(t, cmd)})
	}
	return out
}

func blobFileCount(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeliverRuleActions(t *testing.T) {
	tests := []struct {
		name      string
		env       *bongov1.QueueEnvelope
		wantBoxes []string // mailbox of each delivery, in order
		wantFlags uint32
	}{
		{
			name:      "no actions",
			env:       &bongov1.QueueEnvelope{},
			wantBoxes: []string{"INBOX"},
		},
		{
			name:      "flag and seen reach the delivery",
			env:       &bongov1.QueueEnvelope{PendingFlags: store.FlagSeen | store.FlagFlagged},
			wantBoxes: []string{"INBOX"},
			wantFlags: store.FlagSeen | store.FlagFlagged,
		},
		{
			name:      "copy delivers to both",
			env:       &bongov1.QueueEnvelope{CopyMailboxes: []string{"Backup"}},
			wantBoxes: []string{"INBOX", "Backup"},
		},
		{
			name:      "copy to the final destination is skipped",
			env:       &bongov1.QueueEnvelope{TargetMailbox: "Work", CopyMailboxes: []string{"work"}},
			wantBoxes: []string{"Work"},
		},
		{
			name:      "copy to inbox when moved elsewhere",
			env:       &bongov1.QueueEnvelope{TargetMailbox: "Work", CopyMailboxes: []string{"INBOX"}},
			wantBoxes: []string{"Work", "INBOX"},
		},
		{
			name:      "duplicate copies write once",
			env:       &bongov1.QueueEnvelope{CopyMailboxes: []string{"Backup", "BACKUP"}},
			wantBoxes: []string{"INBOX", "Backup"},
		},
		{
			name:      "delete with a copy keeps only the copy",
			env:       &bongov1.QueueEnvelope{DiscardNormal: true, CopyMailboxes: []string{"Backup"}},
			wantBoxes: []string{"Backup"},
		},
		{
			name:      "delete with a copy to the would-be destination still copies",
			env:       &bongov1.QueueEnvelope{DiscardNormal: true, CopyMailboxes: []string{"INBOX"}},
			wantBoxes: []string{"INBOX"},
		},
		{
			name:      "delete with nothing to copy delivers nothing",
			env:       &bongov1.QueueEnvelope{DiscardNormal: true},
			wantBoxes: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bs, err := store.NewBlobStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			applier := &mockApplier{}
			resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
			proc := NewDeliverProcessor(applier, resolver, newTestLogger())
			proc.SetAttachmentDedup(bs, 1<<20) // blob store set; message stays whole

			tc.env.Id = "env-" + strings.ReplaceAll(tc.name, " ", "-")
			tc.env.Sender = "bob@example.com"
			tc.env.Recipients = []string{"alice@example.com"}
			tc.env.ReceivedAt = time.Now().Unix()
			tc.env.BlobHash = "h"
			raw := minimalMessage("bob@example.com", "alice@example.com", "Hi", "Body\r\n")
			if _, err := proc.Process(context.Background(), tc.env, strings.NewReader(raw)); err != nil {
				t.Fatalf("Process: %v", err)
			}

			got := decodeDeliveries(t, applier)
			var boxes []string
			for _, d := range got {
				boxes = append(boxes, d.mailbox)
				if d.flags != tc.wantFlags {
					t.Errorf("%s: flags = %d, want %d", d.mailbox, d.flags, tc.wantFlags)
				}
			}
			if strings.Join(boxes, ",") != strings.Join(tc.wantBoxes, ",") {
				t.Fatalf("mailboxes = %v, want %v", boxes, tc.wantBoxes)
			}

			// Every delivery points at the same blob but has its own GUID.
			guids := map[string]bool{}
			for _, d := range got {
				if d.meta.RawMessage.Hash != got[0].meta.RawMessage.Hash {
					t.Errorf("%s: blob hash %q differs from %q", d.mailbox, d.meta.RawMessage.Hash, got[0].meta.RawMessage.Hash)
				}
				if guids[d.meta.Guid] {
					t.Errorf("%s: GUID %s reused", d.mailbox, d.meta.Guid)
				}
				guids[d.meta.Guid] = true
			}
			if n := blobFileCount(t, filepath.Join(dir, "blobs")); n > 1 {
				t.Errorf("blob files = %d, want at most 1 (copies share the blob)", n)
			}
		})
	}
}

// TestDeliverCopyDoesNotWriteBlobAgain compares the blob count with and
// without a copy for the same message.
func TestDeliverCopyDoesNotWriteBlobAgain(t *testing.T) {
	count := func(copies []string) int {
		dir := t.TempDir()
		bs, err := store.NewBlobStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		proc := NewDeliverProcessor(&mockApplier{}, &mockResolver{mapping: map[string]string{"alice@example.com": "u"}}, newTestLogger())
		proc.SetAttachmentDedup(bs, 1<<20)
		env := &bongov1.QueueEnvelope{
			Id: "e", Sender: "bob@example.com", Recipients: []string{"alice@example.com"},
			ReceivedAt: time.Now().Unix(), BlobHash: "h", CopyMailboxes: copies,
		}
		raw := minimalMessage("bob@example.com", "alice@example.com", "Hi", "Body\r\n")
		if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		return blobFileCount(t, filepath.Join(dir, "blobs"))
	}
	without, with := count(nil), count([]string{"A", "B"})
	if without == 0 {
		t.Fatal("expected the message blob to be written")
	}
	if with != without {
		t.Errorf("blob files with copies = %d, without = %d", with, without)
	}
}

func TestDeliverCopiesCarryTags(t *testing.T) {
	applier := &mockApplier{}
	proc := NewDeliverProcessor(applier, &mockResolver{mapping: map[string]string{"alice@example.com": "u"}}, newTestLogger())
	env := &bongov1.QueueEnvelope{
		Id: "e", Sender: "bob@example.com", Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(), BlobHash: "h",
		PendingTags: []string{"news"}, CopyMailboxes: []string{"Backup"},
	}
	raw := minimalMessage("bob@example.com", "alice@example.com", "Hi", "Body\r\n")
	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	got := decodeDeliveries(t, applier)
	if len(got) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(got))
	}
	for _, d := range got {
		if len(d.meta.Tags) != 1 || d.meta.Tags[0] != "news" {
			t.Errorf("%s: tags = %v, want [news]", d.mailbox, d.meta.Tags)
		}
	}
}
