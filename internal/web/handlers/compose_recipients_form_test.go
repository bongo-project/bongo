package handlers

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"regexp"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestComposeSendRecipientFieldsAreUnchangedByPills posts a multipart body
// built from the field names the rendered compose form actually carries,
// with a named and a bare address. The recipient pills are a
// view of the To/Cc/Bcc inputs, so the posted bytes are the typed bytes and
// the server must parse them as before.
func TestComposeSendRecipientFieldsAreUnchangedByPills(t *testing.T) {
	var page bytes.Buffer
	if err := templates.ComposePage("new", "", "", "", "", "", 0, nil, "", "").Render(context.Background(), &page); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"to", "cc", "bcc", "subject", "send_id"} {
		if !regexp.MustCompile(`name="` + name + `"`).Match(page.Bytes()) {
			t.Fatalf("rendered compose form has no %q field", name)
		}
	}

	tests := []struct {
		name string
		to   string
		want []string // bare addresses the To header must yield, in order
	}{
		{"named and bare", `Jane Doe <jane@example.com>, bob@example.com`, []string{"jane@example.com", "bob@example.com"}},
		{"quoted comma", `"Doe, Jane" <jane@example.com>, bob@example.com`, []string{"jane@example.com", "bob@example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newComposeRichTestEnv(t)
			sender := "sender@example.com"
			env.registerLocalUser(t, "u-sender", sender)
			env.registerLocalUser(t, "u-jane", "jane@example.com")
			env.registerLocalUser(t, "u-bob", "bob@example.com")

			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			for _, f := range [][2]string{
				{"send_id", sendID()}, {"to", tt.to}, {"cc", ""}, {"bcc", ""},
				{"subject", "Pills"}, {"body_mode", "plain"}, {"body", "hi"},
			} {
				_ = mw.WriteField(f[0], f[1])
			}
			_ = mw.Close()

			req := httptest.NewRequest(http.MethodPost, "/compose/send", &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req = withUserContext(req, "u-sender", sender)
			w := httptest.NewRecorder()
			env.handler.Send(w, req)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}

			// The posted bytes reach the message header untouched.
			msg, err := netmail.ReadMessage(bytes.NewReader(env.rawSentMessage(t, "u-sender")))
			if err != nil {
				t.Fatal(err)
			}
			addrs, err := msg.Header.AddressList("To")
			if err != nil {
				t.Fatalf("To header unparseable: %v", err)
			}
			if len(addrs) != len(tt.want) {
				t.Fatalf("To = %+v, want %v", addrs, tt.want)
			}
			for i, a := range addrs {
				if a.Address != tt.want[i] {
					t.Errorf("To[%d] = %q, want %q", i, a.Address, tt.want[i])
				}
			}

			// The stored recipient list is one entry per recipient.
			msgs, err := env.mailboxes.ListMessages("u-sender", "Sent")
			if err != nil || len(msgs) == 0 {
				t.Fatalf("Sent: %v, %d messages", err, len(msgs))
			}
			data, err := env.ps.Get([]byte(fmt.Sprintf("/user/u-sender/mail/%s", msgs[0].MailGuid)))
			if err != nil {
				t.Fatal(err)
			}
			var meta bongov1.MailMeta
			if err := proto.Unmarshal(data, &meta); err != nil {
				t.Fatal(err)
			}
			if len(meta.To) != len(tt.want) {
				t.Errorf("MailMeta.To = %q, want %d recipients", meta.To, len(tt.want))
			}
		})
	}
}
