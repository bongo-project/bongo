package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func runBulkCheckCmd(t *testing.T, a, b string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run([]string{"bulk", "--target-allow", "127.0.0.1", "--test-domain", "bulk.test", "--bulk-check-a", a, "--bulk-check-b", b}, &out, &errb)
	return code, out.String() + errb.String()
}

// dropIndexRow deletes one SQLite mail row from a stopped store, which is
// what a loader that skipped or lost an index write leaves behind.
func dropIndexRow(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "metadata", "users", "*.db"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no user index files in %s: %v", dir, err)
	}
	meta, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Close()
	id := strings.TrimSuffix(filepath.Base(files[0]), ".db")
	objs, err := meta.QueryByType(id, store.DocTypeMail)
	if err != nil || len(objs) == 0 {
		t.Fatalf("user %s has no mail rows: %v", id, err)
	}
	if err := meta.DeleteMail(id, objs[0].GUID); err != nil {
		t.Fatal(err)
	}
}

// addProfile writes a bare user profile into a stopped store.
func addProfile(t *testing.T, dir, id, email string, admin bool) {
	t.Helper()
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	val, err := proto.Marshal(&bongov1.UserProfile{Email: email, IsAdmin: admin})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), val); err != nil {
		t.Fatal(err)
	}
}

func TestBulkCheckComparesIndexRows(t *testing.T) {
	o := bulkTestOptions()
	o.Users, o.MessagesPerUser = 2, 6
	spec := bulkTestSpec(t, o)
	a, _ := loadBulk(t, spec)
	b, _ := loadBulk(t, spec)
	if code, out := runBulkCheckCmd(t, a, b); code != exitOK {
		t.Fatalf("equal stores: code %d: %s", code, out)
	}
	dropIndexRow(t, b)
	code, out := runBulkCheckCmd(t, a, b)
	if code != exitFail || !strings.Contains(out, "SQLite mail rows") {
		t.Fatalf("a missing index row: code %d: %s", code, out)
	}
}

func TestDumpWithoutIndexIsNotAMatch(t *testing.T) {
	d := &storeDump{Users: map[string]*userDump{"a@bulk.test": {IndexRows: -1}}}
	e := &storeDump{Users: map[string]*userDump{"a@bulk.test": {IndexRows: -1}}}
	if diffs := compareDumps(d, e); len(diffs) == 0 {
		t.Fatal("two unread indexes were reported as equal")
	}
}

func TestBulkCheckRestrictsToTestDomain(t *testing.T) {
	o := bulkTestOptions()
	o.Users, o.MessagesPerUser = 2, 6
	spec := bulkTestSpec(t, o)
	a, _ := loadBulk(t, spec)
	b, _ := loadBulk(t, spec)

	// An operator account outside the domain, and one whose domain merely
	// ends with the test domain's text, are no part of the comparison.
	addProfile(t, a, "adminoutside0001", "root@ops.example", true)
	addProfile(t, a, "adminlookalike01", "root@xbulk.test", true)
	code, out := runBulkCheckCmd(t, a, b)
	if code != exitOK || !strings.Contains(out, "excluded") {
		t.Fatalf("users outside the domain: code %d: %s", code, out)
	}

	// The same kind of user inside the domain is a difference.
	addProfile(t, a, "admininside00001", "root@bulk.test", true)
	code, out = runBulkCheckCmd(t, a, b)
	if code != exitFail || !strings.Contains(out, "root@bulk.test: only in A") {
		t.Fatalf("a user inside the domain: code %d: %s", code, out)
	}
}

func TestDescribeMessageReportsEveryMissingBlob(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"))
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h1, h2 := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	meta := &bongov1.MailMeta{
		MessageId: "<m@bulk.test>", Guid: "g1",
		Attachments: []*bongov1.BlobRef{{Hash: h1, Size: 10}, {Hash: h2, Size: 20}},
	}
	val, err := proto.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/u1/mail/g1"), val); err != nil {
		t.Fatal(err)
	}
	_, _, problem := describeMessage(storeView{ps: ps, blobs: blobs}, "u1", "INBOX", &bongov1.MailboxMessage{Uid: 1, MailGuid: "g1"})
	for _, h := range []string{h1, h2} {
		if !strings.Contains(problem, h) {
			t.Errorf("problem %q does not name missing blob %s", problem, h)
		}
	}
}
