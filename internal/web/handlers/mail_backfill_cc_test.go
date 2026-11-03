package handlers

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type captureApplier struct{ cmds []*bongov1.RaftCommand }

func (a *captureApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.cmds = append(a.cmds, cmd)
	return nil
}

func TestBackfillThreadingFieldsQuotedCommaCc(t *testing.T) {
	tests := []struct {
		name string
		cc   string
		want []string
	}{
		{"quoted comma", `"Smith, John" <j@x.com>, b@y.com`, []string{`"Smith, John" <j@x.com>`, "b@y.com"}},
		{"encoded comma", "=?UTF-8?Q?M=C3=BCller=2C_J=C3=B6rg?= <j@x.com>, b@y.com", []string{`"Müller, Jörg" <j@x.com>`, "b@y.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ps, err := store.NewPebbleStore(dir + "/pebble")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ps.Close() })
			bs, err := store.NewBlobStore(dir + "/blobs")
			if err != nil {
				t.Fatal(err)
			}
			// In-Reply-To is required, or the backfill returns early.
			raw := "From: s@ext.test\r\nTo: a@example.com\r\nCc: " + tt.cc +
				"\r\nIn-Reply-To: <p@ext.test>\r\nSubject: s\r\n\r\nhi\r\n"
			hash, err := bs.Put([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			meta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: hash}}

			h := NewMailHandler(ps, bs, nil, discardLogger())
			ap := &captureApplier{}
			h.SetApplier(ap)
			h.backfillThreadingFields("u1", "g1", meta)

			if len(ap.cmds) != 1 {
				t.Fatalf("applied %d commands, want 1", len(ap.cmds))
			}
			var got bongov1.MailMeta
			if err := proto.Unmarshal(ap.cmds[0].Value, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Cc, tt.want) {
				t.Errorf("Cc = %q, want %q", got.Cc, tt.want)
			}
		})
	}
}
