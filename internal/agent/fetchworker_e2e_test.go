package agent

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/fetch/fetchtest"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A full run with the real client against a real IMAP server: nothing secret
// reaches the log, the state or the error text.
func TestFetchLogsNoSecrets(t *testing.T) {
	e := newFWEnv(t)
	ca := fetchtest.NewCA(t)
	srv := fetchtest.StartIMAP(t, fetchtest.IMAPOptions{Implicit: true, TLS: ca.ServerConfig(t, "imap.test"), User: "alice", Pass: fwPassword})
	srv.Add(t, "INBOX", "From: bob@remote.test\r\nSubject: topsecret subject\r\n\r\nBODYMARKER-12345\r\n")
	client := fetch.NewClient(&fetchtest.LoopbackDialer{Addr: srv.Addr})
	client.RootCAs = ca.Pool()
	e.w.dial = fetchClientDialer{c: client}

	a := e.addAcct(func(a *bongov1.FetchAccount) { a.Host = "imap.test"; a.Username = "alice" })
	e.pass()

	if e.deliver.count() != 1 {
		t.Fatalf("imported %d: state %+v", e.deliver.count(), e.state(a))
	}
	if blob := e.deliver.blobs[e.deliver.envs[0].hash]; !strings.Contains(blob, "BODYMARKER-12345") {
		t.Fatal("body lost")
	}
	if srv.Count("UID EXPUNGE") != 1 {
		t.Fatalf("expected a UID EXPUNGE, commands %v", srv.Commands())
	}
	out := e.log.String()
	ct := base64.StdEncoding.EncodeToString(a.Credential.Ciphertext)
	for _, secret := range []string{fwPassword, "alice", "BODYMARKER", "topsecret", "INBOX", ct, "bob@remote.test"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log leaks %q:\n%s", secret, out)
		}
	}
	st := e.mustState(a)
	if st.LastError != "" || st.LastRunImported != 1 {
		t.Fatalf("state = %+v", st)
	}
}
