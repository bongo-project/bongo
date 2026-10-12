package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestApplyMailboxRenameRefusesExistingTarget drives the FSM-level apply
// directly so the RenameMailbox path cannot mask a missing guard. Each refused
// rename must leave both mailboxes and their messages untouched.
func TestApplyMailboxRenameRefusesExistingTarget(t *testing.T) {
	tests := []struct {
		name string
		oldN string
		newN string
	}{
		{"existing target", "A", "B"},
		{"same name", "A", "A"},
		{"inbox upper", "A", "INBOX"},
		{"inbox lower", "A", "inbox"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := newMailboxTestEnv(t)
			const user = "user1"
			for _, name := range []string{"INBOX", "A", "B"} {
				if err := ms.CreateMailbox(user, name); err != nil {
					t.Fatalf("CreateMailbox %s: %v", name, err)
				}
			}
			for i, name := range []string{"A", "B"} {
				uid := uint32(i + 1)
				msgBytes, _ := proto.Marshal(&bongov1.MailboxMessage{Uid: uid, MailGuid: "g-" + name})
				if err := ms.store.Set(mailboxMsgKey(user, name, uid), msgBytes); err != nil {
					t.Fatalf("Set message: %v", err)
				}
				meta, _ := ms.GetMailbox(user, name)
				meta.UidNext = uid + 1
				mb, _ := proto.Marshal(meta)
				if err := ms.store.Set(mailboxMetaKey(user, name), mb); err != nil {
					t.Fatalf("Set meta: %v", err)
				}
			}
			snapshot := func() string {
				var out strings.Builder
				for _, name := range []string{"INBOX", "A", "B", "inbox"} {
					meta, err := ms.GetMailbox(user, name)
					if err != nil {
						fmt.Fprintf(&out, "%s: absent;", name)
						continue
					}
					msgs, _ := ms.ListMessages(user, name)
					fmt.Fprintf(&out, "%s: next=%d val=%d msgs=%d;", name, meta.UidNext, meta.UidValidity, len(msgs))
					for _, m := range msgs {
						fmt.Fprintf(&out, "%d=%s,", m.Uid, m.MailGuid)
					}
				}
				return out.String()
			}
			before := snapshot()

			payload, _ := json.Marshal(mailboxRenamePayload{OldName: tt.oldN, NewName: tt.newN})
			cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: mailboxRenameKey(user), Value: payload}
			if _, err := ApplyMailboxCommand(ms.store, cmd); err == nil {
				t.Fatal("expected rename to be refused")
			}
			if after := snapshot(); after != before {
				t.Errorf("state changed by refused rename:\nbefore %s\nafter  %s", before, after)
			}

			if err := ms.RenameMailbox(user, tt.oldN, tt.newN); err == nil {
				t.Error("RenameMailbox: expected error")
			}
			if after := snapshot(); after != before {
				t.Errorf("state changed by RenameMailbox:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}
