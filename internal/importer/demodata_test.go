package importer

import (
	"bytes"
	"context"
	"net/mail"
	"path/filepath"
	"testing"
)

// TestDemoMailParses runs the mbox and EML sources over testdata/demo/mail and
// checks every message parses as RFC 5322 and the set is big enough for the UI.
func TestDemoMailParses(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "demo", "mail")
	mboxes, _ := filepath.Glob(filepath.Join(dir, "*.mbox"))
	emls, _ := filepath.Glob(filepath.Join(dir, "eml", "*.eml"))
	if len(mboxes) != 3 || len(emls) < 10 {
		t.Fatalf("mboxes=%d emls=%d, want 3 and >=10", len(mboxes), len(emls))
	}

	count := func(src MessageSource) int {
		folders, err := src.Folders()
		if err != nil {
			t.Fatalf("Folders: %v", err)
		}
		n := 0
		for _, f := range folders {
			msgs, errs := src.Messages(context.Background(), f)
			for m := range msgs {
				if _, err := mail.ReadMessage(bytes.NewReader(m.Raw)); err != nil {
					t.Errorf("%s: unparseable message: %v", m.SourceID, err)
				}
				n++
			}
			if err := <-errs; err != nil {
				t.Errorf("source error: %v", err)
			}
		}
		return n
	}

	total := count(NewMboxSource(mboxes, "Demo")) + count(NewEMLSource(emls, "Demo"))
	if total < 40 {
		t.Errorf("demo mail messages = %d, want >= 40", total)
	}
}
