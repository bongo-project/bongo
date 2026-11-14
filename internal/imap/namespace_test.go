package imap

import (
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// startNamespaceTestServer is startPollTestServer with namespace prefixes. With
// setter false SetNamespacePrefixes is never called, which is the unset path.
func startNamespaceTestServer(t *testing.T, ms *store.MailboxStore, ps *store.PebbleStore, setter bool, other, shared string) string {
	t.Helper()

	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	notifier := NewNotifier()
	ms.SetNotifier(NewStoreNotifier(notifier))
	srv := NewServer(stubAuthenticator{}, ps, blobs, ms, nil, nil, nil, notifier, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if setter {
		srv.SetNamespacePrefixes(other, shared)
	}
	if err := srv.Listen("127.0.0.1:0", "", nil, false, nil); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv.PlainAddr()
}

// execStatus runs one command and returns the tagged status line, so a test can
// assert a refusal instead of failing on it like exec does.
func (c *rawIMAPConn) execStatus(format string, args ...any) string {
	c.t.Helper()

	c.seq++
	tag := fmt.Sprintf("s%d", c.seq)
	cmd := fmt.Sprintf(format, args...)
	if _, err := fmt.Fprintf(c.conn, "%s %s\r\n", tag, cmd); err != nil {
		c.t.Fatalf("writing %q: %v", cmd, err)
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.t.Fatalf("reading response to %q: %v", cmd, err)
		}
		if rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), tag+" "); ok {
			return rest
		}
	}
}

func TestNamespaceAdvertisedAndReported(t *testing.T) {
	tests := []struct {
		name          string
		setter        bool
		other, shared string
		want          string
	}{
		{"defaults when setter not called", false, "", "", `* NAMESPACE (("" "/")) (("Other Users/" "/")) (("Shared/" "/"))`},
		{"empty falls back", true, "", "", `* NAMESPACE (("" "/")) (("Other Users/" "/")) (("Shared/" "/"))`},
		{"custom", true, "People", "Team", `* NAMESPACE (("" "/")) (("People/" "/")) (("Team/" "/"))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms, ps := newSubscriptionTestStore(t, "INBOX")
			raw := dialRawIMAP(t, startNamespaceTestServer(t, ms, ps, tt.setter, tt.other, tt.shared))

			caps := strings.Join(raw.exec("CAPABILITY"), "\n")
			if !strings.Contains(caps, "NAMESPACE") {
				t.Errorf("CAPABILITY after LOGIN = %q, want NAMESPACE", caps)
			}
			if got := raw.exec("NAMESPACE"); !slices.Contains(got, tt.want) {
				t.Errorf("NAMESPACE = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateAndRenameRefuseReservedNames(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX", "Work", "Shared")
	raw := dialRawIMAP(t, startNamespaceTestServer(t, ms, ps, false, "", ""))

	for _, name := range []string{`"Shared"`, `"shared/x"`, `"Shared/"`, `"Other Users/bob"`, `"OTHER USERS"`} {
		if got := raw.execStatus("CREATE %s", name); !strings.HasPrefix(got, "NO") {
			t.Errorf("CREATE %s = %q, want NO", name, got)
		}
	}
	if got := raw.execStatus(`CREATE "SharedStuff"`); !strings.HasPrefix(got, "OK") {
		t.Errorf("CREATE SharedStuff = %q, want OK", got)
	}
	if _, err := ms.GetMailbox(pollTestUser, "shared/x"); err == nil {
		t.Error("a refused CREATE still made the mailbox")
	}

	if got := raw.execStatus(`RENAME Work "Shared/Work"`); !strings.HasPrefix(got, "NO") {
		t.Errorf("RENAME Work to Shared/Work = %q, want NO", got)
	}
	if _, err := ms.GetMailbox(pollTestUser, "Work"); err != nil {
		t.Errorf("Work gone after a refused RENAME: %v", err)
	}

	// A legacy folder that already carries a reserved name can be renamed away
	// and still be used.
	if got := raw.execStatus(`SELECT Shared`); !strings.HasPrefix(got, "OK") {
		t.Errorf("SELECT legacy Shared = %q, want OK", got)
	}
	if got := raw.execStatus(`RENAME Shared Team`); !strings.HasPrefix(got, "OK") {
		t.Errorf("RENAME legacy Shared to Team = %q, want OK", got)
	}
}

func TestCreateRefusesCustomPrefixes(t *testing.T) {
	ms, ps := newSubscriptionTestStore(t, "INBOX")
	raw := dialRawIMAP(t, startNamespaceTestServer(t, ms, ps, true, "People", "Team"))

	for _, name := range []string{"people", "Team/x"} {
		if got := raw.execStatus(`CREATE "%s"`, name); !strings.HasPrefix(got, "NO") {
			t.Errorf("CREATE %s = %q, want NO", name, got)
		}
	}
	// "Shared" is an ordinary name once the prefix is configured away.
	if got := raw.execStatus(`CREATE Shared`); !strings.HasPrefix(got, "OK") {
		t.Errorf("CREATE Shared = %q, want OK", got)
	}
}
