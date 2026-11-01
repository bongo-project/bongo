package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// The startup report is one warning naming the hosted, enabled domains with no
// postmaster@/abuse@ target, and a second for a configured account that does
// not exist yet. Disabled domains and domain sub-keys are not listed.
func TestReportPostmasterTargets(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	for k, v := range map[string]string{
		"/system/domain/bare.pmreport.test":      "enabled",
		"/system/domain/bare.pmreport.test/dkim": "x",
		"/system/domain/off.pmreport.test":       "disabled",
	} {
		if err := a.Store.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	r := store.NewRecipientResolver(a.AddressStore, a.Store, a.Metadata.System)
	r.SetPostmasterAccount("ghost@pmreport.test")

	logPostmasterReport(r, logger)
	out := buf.String()
	if !strings.Contains(out, "bare.pmreport.test") {
		t.Errorf("warning does not name the bare domain:\n%s", out)
	}
	if strings.Contains(out, "off.pmreport.test") {
		t.Errorf("warning names a disabled domain:\n%s", out)
	}
	if strings.Count(out, "postmaster@ or abuse@ does not resolve") != 1 {
		t.Errorf("want exactly one domain warning:\n%s", out)
	}
	if !strings.Contains(out, "ghost@pmreport.test") {
		t.Errorf("no warning for the missing configured account:\n%s", out)
	}
}
