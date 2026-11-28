package deliver

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/attpolicy"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A message the attachment policy stripped no longer matches its DKIM body
// hash. The verdict recorded before stripping is what deliver stamps.
func TestDeliverUsesDKIMResultRecordedBeforeStripping(t *testing.T) {
	key := newTestRSAKey(t)
	stubDKIMPass(t, key, "example.test", "sel")

	raw := minimalMessage("bob@example.test", "alice@example.com", "Hello", "Hi Alice!\r\n")
	signed := signedTestMessage(t, key, "example.test", "sel",
		[]string{"from", "to", "subject", "date", "message-id"}, raw)

	// Build the stripped form the stage would store: same headers, new body.
	hi := bytes.Index(signed, []byte("\r\n\r\n"))
	multi := string(signed[:hi]) + "\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nHi\r\n--B\r\nContent-Disposition: attachment; filename=\"a.exe\"\r\n\r\nQUJD\r\n--B--\r\n"
	res := attpolicy.Evaluate([]byte(multi), attpolicy.NewPolicy([]string{"exe"}, nil), true, nil)
	if res.Out == nil {
		t.Fatalf("fixture not stripped: %+v", res)
	}

	cases := []struct {
		name       string
		stages     []*bongov1.StageResult
		wantResult string
	}{
		{"recorded verdict is used", []*bongov1.StageResult{{Stage: "attachment_policy", Action: "strip", DkimResult: "pass", DkimDomain: "example.test"}}, "pass"},
		{"no strip result: verified on the stored bytes", nil, "fail"},
		{"a pass result is not a strip", []*bongov1.StageResult{{Stage: "attachment_policy", Action: "pass", DkimResult: "pass", DkimDomain: "example.test"}}, "fail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proc, _, bs := traceProc(t, "mail.example.com", 0.5)
			env := &bongov1.QueueEnvelope{
				Id: "env-strip", Sender: "bob@example.test", Recipients: []string{"alice@example.com"},
				ReceivedAt: time.Now().Unix(), BlobHash: "strippedhash", SpfResult: "pass", Stages: tc.stages,
			}
			meta := deliverTrace(t, proc, env, string(res.Out))
			if meta.DkimResult != tc.wantResult {
				t.Fatalf("meta.DkimResult = %q, want %q", meta.DkimResult, tc.wantResult)
			}
			stored, err := store.RawMessageBytes(bs, meta)
			if err != nil {
				t.Fatal(err)
			}
			first := strings.SplitN(string(stored), "\r\n", 2)[0]
			if !strings.Contains(first, "dkim="+tc.wantResult) {
				t.Errorf("Authentication-Results = %q, want dkim=%s", first, tc.wantResult)
			}
			if tc.wantResult == "pass" && meta.DkimDomain != "example.test" {
				t.Errorf("DkimDomain = %q", meta.DkimDomain)
			}
		})
	}
}
