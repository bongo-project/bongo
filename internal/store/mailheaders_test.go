package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestMailHeaderFields(t *testing.T) {
	mine := map[string]bool{"me@example.com": true, "alias@example.com": true}
	tests := []struct {
		name string
		meta *bongov1.MailMeta
		want MailHeaderValues
	}{
		{
			name: "display name and case are normalised",
			meta: &bongov1.MailMeta{From: `"Doe, Jane" <Jane.Doe@Example.COM>`, To: []string{"Me <ME@example.com>"}},
			want: MailHeaderValues{ToMe: true, SenderAddr: "jane.doe@example.com"},
		},
		{
			name: "address in Cc counts",
			meta: &bongov1.MailMeta{From: "a@x.test", To: []string{"other@example.com"}, Cc: []string{"Me <me@example.com>"}},
			want: MailHeaderValues{ToMe: true, SenderAddr: "a@x.test"},
		},
		{
			name: "alias counts as mine",
			meta: &bongov1.MailMeta{From: "a@x.test", To: []string{"alias@example.com"}},
			want: MailHeaderValues{ToMe: true, SenderAddr: "a@x.test"},
		},
		{
			name: "an address not owned (catch-all delivery) is not to me",
			meta: &bongov1.MailMeta{From: "a@x.test", To: []string{"anything@example.com"}},
			want: MailHeaderValues{SenderAddr: "a@x.test"},
		},
		{
			name: "bare address with no angle brackets",
			meta: &bongov1.MailMeta{From: "a@x.test", To: []string{"me@example.com"}},
			want: MailHeaderValues{ToMe: true, SenderAddr: "a@x.test"},
		},
		{
			name: "fragment of a naively comma-split To header",
			meta: &bongov1.MailMeta{From: "a@x.test", To: []string{`"Doe`, `Jane" <me@example.com>`}},
			want: MailHeaderValues{ToMe: true, SenderAddr: "a@x.test"},
		},
		{
			name: "List-Id with a phrase",
			meta: &bongov1.MailMeta{From: "a@x.test", ListId: `"Go Users" <Go-Users.Example.org>`},
			want: MailHeaderValues{SenderAddr: "a@x.test", ListID: "go-users.example.org", ListName: "Go Users"},
		},
		{
			name: "List-Id without angle brackets",
			meta: &bongov1.MailMeta{From: "a@x.test", ListId: "plain.lists.example.org"},
			want: MailHeaderValues{SenderAddr: "a@x.test", ListID: "plain.lists.example.org"},
		},
		{
			name: "List-Id with a path separator is dropped, not stored",
			meta: &bongov1.MailMeta{From: "a@x.test", ListId: "Evil <../../admin>"},
			want: MailHeaderValues{SenderAddr: "a@x.test"},
		},
		{
			name: "List-Id with markup in the id is dropped",
			meta: &bongov1.MailMeta{From: "a@x.test", ListId: "<a<script>.example.org>"},
			want: MailHeaderValues{SenderAddr: "a@x.test"},
		},
		{
			name: "unterminated List-Id is dropped",
			meta: &bongov1.MailMeta{From: "a@x.test", ListId: "Name <list.example.org"},
			want: MailHeaderValues{SenderAddr: "a@x.test"},
		},
		{
			name: "unparseable sender yields no address",
			meta: &bongov1.MailMeta{From: "not an address"},
			want: MailHeaderValues{},
		},
		{
			name: "empty meta",
			meta: &bongov1.MailMeta{},
			want: MailHeaderValues{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MailHeaderFields(tt.meta, mine); got != tt.want {
				t.Errorf("MailHeaderFields = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseListIDNameIsCleaned(t *testing.T) {
	_, name := ParseListID("Line\x00one\r\nTwo <x.example.org>")
	if strings.ContainsAny(name, "\x00\r\n") {
		t.Errorf("name %q keeps control characters", name)
	}
	_, long := ParseListID(strings.Repeat("é", 500) + " <x.example.org>")
	if n := len([]rune(long)); n != maxListNameLen {
		t.Errorf("name length = %d runes, want %d", n, maxListNameLen)
	}
	if id, _ := ParseListID("<" + strings.Repeat("a", 300) + ">"); id != "" {
		t.Error("an over-long id must be dropped")
	}
}

func TestParseHeaderView(t *testing.T) {
	tests := []struct {
		name string
		want HeaderView
		ok   bool
	}{
		{"to-me", HeaderView{Kind: ViewToMe}, true},
		{"from-contacts", HeaderView{Kind: ViewFromContacts}, true},
		{"lists", HeaderView{Kind: ViewLists}, true},
		{"lists/go-users.example.org", HeaderView{Kind: ViewLists, ListID: "go-users.example.org"}, true},
		{"lists/Upper", HeaderView{}, false},
		{"lists/a/b", HeaderView{}, false},
		{"lists/", HeaderView{}, false},
		{"INBOX", HeaderView{}, false},
		{"starred", HeaderView{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseHeaderView(tt.name)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseHeaderView(%q) = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestHeaderViewExcludes(t *testing.T) {
	for name, want := range map[string]bool{
		"INBOX": false, "Archive": false, "Projects": false, "Projects/Sent": false,
		"Junk": true, "Trash": true, "Sent": true, "Drafts": true, "Trash/Old": true,
		"Sentinel": false,
	} {
		if got := HeaderViewExcludes(name); got != want {
			t.Errorf("HeaderViewExcludes(%q) = %v, want %v", name, got, want)
		}
	}
}

func openTestUserIndex(t *testing.T) *UserIndex {
	t.Helper()
	m, err := NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	idx, err := m.OpenUser("u1")
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestHeaderColumnsSentinelAndIndexMailKeepsThem(t *testing.T) {
	idx := openTestUserIndex(t)
	if err := idx.IndexMail("g1", "s", "a@x.test", 10, 1); err != nil {
		t.Fatal(err)
	}
	if p, _ := idx.PendingHeaders("", 10); !reflect.DeepEqual(p, []string{"g1"}) {
		t.Fatalf("a freshly indexed mail must be pending, got %v", p)
	}
	// Not-to-me is still "derived": 0, not NULL.
	if err := idx.SetMailHeaders("g1", MailHeaderValues{SenderAddr: "a@x.test"}); err != nil {
		t.Fatal(err)
	}
	// Derived from a MailMeta that may lack Cc and List-Id: still pending for
	// the backfill's blob check.
	if p, _ := idx.PendingHeaders("", 10); !reflect.DeepEqual(p, []string{"g1"}) {
		t.Fatalf("pending after an incomplete SetMailHeaders = %v, want g1", p)
	}
	if err := idx.SetMailHeaders("g1", MailHeaderValues{ToMe: true, SenderAddr: "a@x.test", ListID: "l.example.org", ListName: "L", Complete: true}); err != nil {
		t.Fatal(err)
	}
	// IndexMail re-runs on every flag or tag PUT: it must not reset the columns.
	if err := idx.IndexMail("g1", "s2", "a@x.test", 11, 2); err != nil {
		t.Fatal(err)
	}
	if p, _ := idx.PendingHeaders("", 10); len(p) != 0 {
		t.Fatalf("IndexMail reset the NULL sentinel: pending = %v", p)
	}
	if g, _ := idx.QueryToMe(); !reflect.DeepEqual(g, []string{"g1"}) {
		t.Fatalf("to-me = %v", g)
	}
	if g, _ := idx.QueryByListID("l.example.org"); !reflect.DeepEqual(g, []string{"g1"}) {
		t.Fatalf("list = %v", g)
	}
}

func TestPendingHeadersCursor(t *testing.T) {
	idx := openTestUserIndex(t)
	for _, g := range []string{"a", "b", "c", "d"} {
		if err := idx.IndexMail(g, "s", "x", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := idx.PendingHeaders("b", 5)
	if !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Errorf("after b = %v", got)
	}
	got, _ = idx.PendingHeaders("", 2)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("limit 2 = %v", got)
	}
}

func TestQueriesOrderAndGroups(t *testing.T) {
	idx := openTestUserIndex(t)
	type row struct {
		guid string
		at   int64
		v    MailHeaderValues
	}
	for _, r := range []row{
		{"old", 100, MailHeaderValues{ToMe: true, SenderAddr: "a@x.test", ListID: "l1.example.org", ListName: ""}},
		{"new", 300, MailHeaderValues{ToMe: true, SenderAddr: "b@x.test", ListID: "l1.example.org", ListName: "List One"}},
		{"mid", 200, MailHeaderValues{SenderAddr: "a@x.test", ListID: "l2.example.org", ListName: "List Two"}},
		{"plain", 400, MailHeaderValues{SenderAddr: "c@x.test"}},
	} {
		if err := idx.IndexMail(r.guid, "s", "x", r.at, 1); err != nil {
			t.Fatal(err)
		}
		if err := idx.SetMailHeaders(r.guid, r.v); err != nil {
			t.Fatal(err)
		}
	}
	if g, _ := idx.QueryToMe(); !reflect.DeepEqual(g, []string{"new", "old"}) {
		t.Errorf("to-me = %v, want newest first", g)
	}
	groups, err := idx.ListGroups()
	if err != nil {
		t.Fatal(err)
	}
	want := []ListGroup{
		{ID: "l1.example.org", Name: "List One", GUIDs: []string{"new", "old"}},
		{ID: "l2.example.org", Name: "List Two", GUIDs: []string{"mid"}},
	}
	if !reflect.DeepEqual(groups, want) {
		t.Errorf("groups = %+v, want %+v", groups, want)
	}
	got, _ := idx.QueryBySenders(map[string]bool{"a@x.test": true})
	if len(got) != 2 {
		t.Errorf("senders = %v", got)
	}
	if got, _ := idx.QueryBySenders(nil); got != nil {
		t.Errorf("no senders must match nothing, got %v", got)
	}
}

// A database written before the header columns existed must gain them, and
// their indexes, when it is reopened: the indexes name columns an old table
// lacks, so creating them in the schema string would fail on every install.
func TestOldDatabaseGainsHeaderColumnsAndIndexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata", "users", "u1.db")
	m, err := NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenUser("u1"); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseUser("u1"); err != nil {
		t.Fatal(err)
	}
	m.Close()

	// Rewrite the file as a v0.14 database: no header columns, no indexes.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP TABLE maildocument`,
		`CREATE TABLE maildocument (
			guid TEXT PRIMARY KEY, conversation_guid TEXT, subject TEXT, sender TEXT,
			message_id TEXT, parent_message_id TEXT, list_id TEXT, received_at INTEGER,
			preview TEXT, has_attachments INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO maildocument (guid, subject, received_at) VALUES ('old1', 's', 5)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.Close()

	m, err = NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	idx, err := m.OpenUser("u1")
	if err != nil {
		t.Fatalf("reopening an old database: %v", err)
	}
	var names []string
	rows, err := idx.db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'maildocument'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	for _, want := range []string{"idx_maildocument_to_me", "idx_maildocument_sender", "idx_maildocument_list"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("index %s missing after reopen: have %v", want, names)
		}
	}
	// The existing row is the backfill's work.
	if p, _ := idx.PendingHeaders("", 10); !reflect.DeepEqual(p, []string{"old1"}) {
		t.Errorf("pending = %v, want the pre-existing row", p)
	}
}

func TestSplitAddressHeader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single bare", "a@x.example", []string{"a@x.example"}},
		{"bare list", "a@x.example, b@y.example", []string{"a@x.example", "b@y.example"}},
		{"bare list no spaces", "a@x.example,b@y.example", []string{"a@x.example", "b@y.example"}},
		{"named list is unchanged", "Jane Doe <j@x.example>, b@y.example", []string{"Jane Doe <j@x.example>", "b@y.example"}},
		{"quoted comma is one recipient", `"Doe, Jane" <j@x.example>, b@y.example`, []string{`"Doe, Jane" <j@x.example>`, "b@y.example"}},
		{"story case", `"Smith, John" <j@x.com>, b@y.com`, []string{`"Smith, John" <j@x.com>`, "b@y.com"}},
		{"quoted comma last", `b@y.example, "Doe, Jane" <j@x.example>`, []string{"b@y.example", `"Doe, Jane" <j@x.example>`}},
		{"two quoted commas", `"A, B" <a@x.example>, "C, D" <c@x.example>`, []string{`"A, B" <a@x.example>`, `"C, D" <c@x.example>`}},
		{"trailing comma", "a@x.example,", []string{"a@x.example"}},
		{"separators only", " , ,", nil},
		{"invalid list falls back to comma split", "a@x.example, not an address", []string{"a@x.example", "not an address"}},
		{"unterminated quote falls back", `"Doe, Jane <j@x.example>, b@y.example`, []string{`"Doe`, `Jane <j@x.example>`, "b@y.example"}},
		{"group keeps the raw value", "undisclosed-recipients:;", []string{"undisclosed-recipients:;"}},
		{"non-ASCII name stays decoded", `"Müller, Jörg" <j@x.example>, b@y.example`, []string{`"Müller, Jörg" <j@x.example>`, "b@y.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitAddressHeader(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitAddressHeader(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
