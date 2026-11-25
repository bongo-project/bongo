package smtp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const fiftyMembers = 50

// newFiftyEnv builds a real GroupStore holding one group of fifty local users.
// The test stops at the spool: the deliver stage that writes mailboxes does not
// run here.
func newFiftyEnv(t *testing.T) (*groupEnv, string, []string) {
	t.Helper()
	e := newGroupEnv(t, fiftyMembers)
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	if err := ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	var addrs, ids []string
	users := map[string]bool{}
	for i := range fiftyMembers {
		a := fmt.Sprintf("m%02d@example.com", i)
		id := "uid-" + a
		data, err := proto.Marshal(&bongov1.UserProfile{Email: a})
		if err != nil {
			t.Fatal(err)
		}
		if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
		addrs = append(addrs, a)
		ids = append(ids, id)
		users[a] = true
	}
	gs := store.NewGroupStore(ps, &groupApplier{ps: ps})
	g, err := gs.CreateGroup(&bongov1.Group{
		Address:       "team@example.com",
		MemberIds:     ids,
		PostingPolicy: "anyone",
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	e.b.SetGroups(gs)
	e.b.resolver = &addrResolver{users: users, groups: map[string]string{"team@example.com": g.Id}}
	slices.Sort(addrs)
	return e, g.Id, addrs
}

func assertFiftyOnce(t *testing.T, e *groupEnv, gid string, want []string, headers int) {
	t.Helper()
	envs := e.envelopes(t)
	if len(envs) != fiftyMembers {
		t.Fatalf("envelopes = %d, want %d", len(envs), fiftyMembers)
	}
	for _, env := range envs {
		if len(env.Recipients) != 1 {
			t.Fatalf("envelope %s has recipients %v, want exactly one", env.Id, env.Recipients)
		}
	}
	if got := recipientsOf(envs); !slices.Equal(got, want) {
		t.Fatalf("recipients = %v, want each member once %v", got, want)
	}
	blobs := distinctBlobs(envs)
	if len(blobs) != 1 {
		t.Fatalf("distinct blob hashes = %d, want 1", len(blobs))
	}
	var files int
	_ = filepath.WalkDir(filepath.Join(e.dir, "blobs"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files++
		}
		return nil
	})
	if files != 1 {
		t.Fatalf("blob files on disk = %d, want 1", files)
	}
	var hash string
	for h := range blobs {
		hash = h
	}
	if n := strings.Count(e.blob(t, hash), "X-Bongo-Group: "+gid); n != headers {
		t.Fatalf("X-Bongo-Group header count = %d, want %d", n, headers)
	}
}

func TestGroupFiftyMembersOneCopyEachOneBlob(t *testing.T) {
	tests := []struct {
		name    string
		headers int // one X-Bongo-Group line per group RCPT
		send    func(t *testing.T, e *groupEnv)
	}{
		{"over the wire", 1, func(t *testing.T, e *groupEnv) {
			addr := startSizedServer(t, e.b)
			c, err := gosmtp.Dial(addr)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			if err := c.Mail("sender@remote.example", nil); err != nil {
				t.Fatal(err)
			}
			if err := c.Rcpt("team@example.com", nil); err != nil {
				t.Fatalf("RCPT team: %v", err)
			}
			w, err := c.Data()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, mxMsg); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("DATA: %v", err)
			}
		}},
		{"group plus overlapping direct", 1, func(t *testing.T, e *groupEnv) {
			s := e.mx()
			rcptAll(t, s, "team@example.com", "m07@example.com")
			if err := s.Data(strings.NewReader(mxMsg)); err != nil {
				t.Fatal(err)
			}
		}},
		{"group addressed twice", 2, func(t *testing.T, e *groupEnv) {
			s := e.mx()
			rcptAll(t, s, "team@example.com", "team@example.com")
			if err := s.Data(strings.NewReader(mxMsg)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, gid, want := newFiftyEnv(t)
			tc.send(t, e)
			assertFiftyOnce(t, e, gid, want, tc.headers)
		})
	}
}
