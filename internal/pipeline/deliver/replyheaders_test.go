package deliver

import (
	"context"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestStoredHeader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "list@example.org", "list@example.org"},
		{"trimmed", "  list@example.org \t", "list@example.org"},
		{"CRLF removed", "a@example.org\r\nBcc: b@example.org", "a@example.orgBcc: b@example.org"},
		{"at the cap", strings.Repeat("a", maxStoredReplyHeaderBytes), strings.Repeat("a", maxStoredReplyHeaderBytes)},
		{"over the cap dropped", strings.Repeat("a", maxStoredReplyHeaderBytes+1), ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storedHeader(tt.in); got != tt.want {
				t.Errorf("storedHeader = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeliverStoresReplyAndListHeaders(t *testing.T) {
	tests := []struct {
		name                        string
		extra                       string
		wantReply, wantID, wantPost string
	}{
		{
			name:      "all three",
			extra:     "Reply-To: Team <team@example.org>\r\nList-Id: <dev.lists.example.org>\r\nList-Post: <mailto:dev@lists.example.org>\r\n",
			wantReply: "Team <team@example.org>", wantID: "<dev.lists.example.org>", wantPost: "<mailto:dev@lists.example.org>",
		},
		{name: "none"},
		{
			// Stored raw so the reply handler's net/mail parse sees the
			// encoded-word, not "Müller, Hans", which it would reject.
			name:      "encoded Reply-To kept raw",
			extra:     "Reply-To: =?UTF-8?Q?M=C3=BCller=2C_Hans?= <hans@example.org>\r\n",
			wantReply: "=?UTF-8?Q?M=C3=BCller=2C_Hans?= <hans@example.org>",
		},
		{
			name:      "oversize dropped",
			extra:     "Reply-To: " + strings.Repeat("a", 2000) + "@example.org\r\nList-Post: NO\r\n",
			wantReply: "", wantPost: "NO",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applier := &mockApplier{}
			resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
			proc := NewDeliverProcessor(applier, resolver, newTestLogger())

			raw := "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: S\r\n" + tt.extra +
				"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <t@example.com>\r\n\r\nBody\r\n"
			env := &bongov1.QueueEnvelope{
				Id: "env-rl", Sender: "bob@example.com", Recipients: []string{"alice@example.com"},
				ReceivedAt: time.Now().Unix(), BlobHash: "abc123",
			}
			if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
				t.Fatal(err)
			}
			if len(applier.commands) != 1 {
				t.Fatalf("commands = %d", len(applier.commands))
			}
			meta := unmarshalMailMeta(t, applier.commands[0])
			if meta.ReplyTo != tt.wantReply || meta.ListId != tt.wantID || meta.ListPost != tt.wantPost {
				t.Errorf("reply_to=%q list_id=%q list_post=%q; want %q %q %q",
					meta.ReplyTo, meta.ListId, meta.ListPost, tt.wantReply, tt.wantID, tt.wantPost)
			}
		})
	}
}
