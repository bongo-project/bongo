package store

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func participantMap(v MailHeaderValues) map[string]int {
	out := map[string]int{}
	for _, p := range v.Participants {
		out[p.Addr] = p.Roles
	}
	return out
}

func TestMailHeaderFieldsParticipants(t *testing.T) {
	meta := &bongov1.MailMeta{
		From:    "Ann <ANN@X.org>",
		To:      []string{`"Doe, Jane" <j@x.org>`, "bob@x.org", "undisclosed-recipients:;"},
		Cc:      []string{"ann@x.org", "Carl <carl@x.org>"},
		ReplyTo: "reply@x.org",
	}
	got := participantMap(MailHeaderFields(meta, nil))
	want := map[string]int{
		"ann@x.org":  RoleFrom | RoleCc,
		"j@x.org":    RoleTo,
		"bob@x.org":  RoleTo,
		"carl@x.org": RoleCc,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("participants = %v, want %v", got, want)
	}
	if _, ok := got["reply@x.org"]; ok {
		t.Error("Reply-To must not be a participant")
	}
	// Order is From, To, Cc.
	var order []string
	for _, p := range MailHeaderFields(meta, nil).Participants {
		order = append(order, p.Addr)
	}
	if !reflect.DeepEqual(order, []string{"ann@x.org", "j@x.org", "bob@x.org", "carl@x.org"}) {
		t.Errorf("order = %v", order)
	}
}

func TestMailHeaderFieldsParticipantCap(t *testing.T) {
	var to []string
	for i := range 500 {
		to = append(to, fmt.Sprintf("u%d@x.org", i))
	}
	v := MailHeaderFields(&bongov1.MailMeta{From: "from@x.org", To: to}, nil)
	if len(v.Participants) != MaxParticipantsPerMessage {
		t.Fatalf("participants = %d, want %d", len(v.Participants), MaxParticipantsPerMessage)
	}
	if v.Participants[0].Addr != "from@x.org" {
		t.Errorf("From must be kept first, got %q", v.Participants[0].Addr)
	}
	long := strings.Repeat("a", 250) + "@x.org"
	v = MailHeaderFields(&bongov1.MailMeta{From: "f@x.org", To: []string{long}}, nil)
	if len(v.Participants) != 1 {
		t.Errorf("an over-long address must be dropped, got %d participants", len(v.Participants))
	}
}

func setParts(t *testing.T, idx *UserIndex, guid string, addrs ...string) {
	t.Helper()
	v := MailHeaderValues{Complete: true}
	for _, a := range addrs {
		v.Participants = append(v.Participants, Participant{Addr: a, Roles: RoleTo})
	}
	if err := idx.SetMailHeaders(guid, v); err != nil {
		t.Fatal(err)
	}
}

func TestSetMailHeadersReplacesParticipants(t *testing.T) {
	idx := openTestUserIndex(t)
	if err := idx.IndexMail("g1", "s", "x", 1, 1); err != nil {
		t.Fatal(err)
	}
	setParts(t, idx, "g1", "a@x.org", "b@x.org")
	setParts(t, idx, "g1", "b@x.org", "c@x.org")
	if g, _ := idx.QueryByParticipants([]string{"a@x.org"}); len(g) != 0 {
		t.Errorf("a must be gone after the replace, got %v", g)
	}
	for _, a := range []string{"b@x.org", "c@x.org"} {
		if g, _ := idx.QueryByParticipants([]string{a}); !reflect.DeepEqual(g, []string{"g1"}) {
			t.Errorf("%s = %v, want g1", a, g)
		}
	}
}

func TestIndexMailKeepsParticipants(t *testing.T) {
	idx := openTestUserIndex(t)
	if err := idx.IndexMail("g1", "s", "x", 1, 1); err != nil {
		t.Fatal(err)
	}
	setParts(t, idx, "g1", "a@x.org")
	if err := idx.IndexMail("g1", "s2", "x", 2, 2); err != nil {
		t.Fatal(err)
	}
	if g, _ := idx.QueryByParticipants([]string{"a@x.org"}); !reflect.DeepEqual(g, []string{"g1"}) {
		t.Errorf("IndexMail dropped the participants: %v", g)
	}
}

func TestSetMailHeadersWithoutRowWritesNoParticipants(t *testing.T) {
	idx := openTestUserIndex(t)
	setParts(t, idx, "ghost", "a@x.org")
	var n int
	if err := idx.db.QueryRow(`SELECT COUNT(*) FROM participants`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("participants rows = %d for a message with no maildocument row", n)
	}
}

func TestQueryByParticipantsORDistinct(t *testing.T) {
	idx := openTestUserIndex(t)
	for _, g := range []string{"g1", "g2", "g3"} {
		if err := idx.IndexMail(g, "s", "x", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	setParts(t, idx, "g1", "a@x.org", "b@x.org")
	setParts(t, idx, "g2", "b@x.org")
	setParts(t, idx, "g3", "z@x.org")
	got, err := idx.QueryByParticipants([]string{"a@x.org", "b@x.org"})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"g1", "g2"}) {
		t.Errorf("got %v, want g1 once and g2", got)
	}
	if g, _ := idx.QueryByParticipants(nil); len(g) != 0 {
		t.Errorf("no addresses must match nothing, got %v", g)
	}
}

func TestQueryByParticipantsUserIsolation(t *testing.T) {
	m, err := NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	for user, guid := range map[string]string{"u1": "g-one", "u2": "g-two"} {
		idx, err := m.OpenUser(user)
		if err != nil {
			t.Fatal(err)
		}
		if err := idx.IndexMail(guid, "s", "x", 1, 1); err != nil {
			t.Fatal(err)
		}
		setParts(t, idx, guid, "shared@x.org")
	}
	for user, want := range map[string]string{"u1": "g-one", "u2": "g-two"} {
		got, err := m.QueryByParticipants(user, []string{"shared@x.org"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s got %v, want only %s", user, got, want)
		}
	}
}

func TestParticipantsQueryPlanUsesIndex(t *testing.T) {
	idx := openTestUserIndex(t)
	cases := []struct {
		name, sql string
		args      []any
		want      string
	}{
		// The very text QueryByParticipants runs.
		{"lookup", participantsQuery(2), []any{"a", "b"}, "USING PRIMARY KEY"},
		{"delete", `DELETE FROM participants WHERE mail_guid = ?`, []any{"g"}, "idx_participants_guid"},
	}
	for _, c := range cases {
		rows, err := idx.db.Query("EXPLAIN QUERY PLAN "+c.sql, c.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		if strings.Contains(joined, "SCAN participants") {
			t.Errorf("%s scans the table: %s", c.name, joined)
		}
		if !strings.Contains(joined, "SEARCH participants") || !strings.Contains(joined, c.want) {
			t.Errorf("%s plan = %s, want SEARCH participants ... %s", c.name, joined, c.want)
		}
	}
}

func TestPendingHeadersIncludesEarlierRevision(t *testing.T) {
	idx := openTestUserIndex(t)
	for _, g := range []string{"null", "rev1", "rev2"} {
		if err := idx.IndexMail(g, "s", "x", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	for g, rev := range map[string]any{"null": nil, "rev1": 1, "rev2": 2} {
		if _, err := idx.db.Exec(`UPDATE maildocument SET to_me = 0, headers_rev = ? WHERE guid = ?`, rev, g); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := idx.PendingHeaders("", 10)
	if !reflect.DeepEqual(got, []string{"null", "rev1"}) {
		t.Errorf("pending = %v, want null and rev1 only", got)
	}
}

func TestSetMailHeadersCompleteWritesCurrentRevision(t *testing.T) {
	idx := openTestUserIndex(t)
	if err := idx.IndexMail("g1", "s", "x", 1, 1); err != nil {
		t.Fatal(err)
	}
	setParts(t, idx, "g1", "a@x.org")
	var rev int
	if err := idx.db.QueryRow(`SELECT headers_rev FROM maildocument WHERE guid = 'g1'`).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if rev != HeadersRevCurrent {
		t.Errorf("headers_rev = %d, want %d", rev, HeadersRevCurrent)
	}
}

func TestOldDatabaseGainsParticipantsTable(t *testing.T) {
	dir := t.TempDir()
	m, err := NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := m.OpenUser("u1")
	if err != nil {
		t.Fatal(err)
	}
	// A v0.15 database: header columns at revision 1, no participants table.
	if err := idx.IndexMail("old1", "s", "x", 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.db.Exec(`UPDATE maildocument SET to_me = 1, headers_rev = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.db.Exec(`DROP TABLE participants`); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseUser("u1"); err != nil {
		t.Fatal(err)
	}
	m.Close()

	m, err = NewMetadataManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	idx, err = m.OpenUser("u1")
	if err != nil {
		t.Fatalf("reopening a v0.15 database: %v", err)
	}
	if g, err := idx.QueryByParticipants([]string{"a@x.org"}); err != nil || len(g) != 0 {
		t.Fatalf("participants table missing after reopen: %v %v", g, err)
	}
	if p, _ := idx.PendingHeaders("", 10); !reflect.DeepEqual(p, []string{"old1"}) {
		t.Errorf("pending = %v, a revision 1 row must be revisited", p)
	}
}

func TestHeaderViewParticipantURL(t *testing.T) {
	v := HeaderView{Kind: ViewParticipant, Participants: []string{"a+b@x.org", "c@x.org"}}
	if got, want := v.URL(), "/search?participant=a%2Bb%40x.org&participant=c%40x.org"; got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
	if got, want := v.BulkURL(), "/search/bulk?participant=a%2Bb%40x.org&participant=c%40x.org"; got != want {
		t.Errorf("BulkURL() = %q, want %q", got, want)
	}
	if v.Excludes("Sent") || v.Excludes("Drafts") || !v.Excludes("Junk") || !v.Excludes("Trash/x") {
		t.Error("a participant view lists Sent and Drafts and skips Junk, Trash and below")
	}
	if !(HeaderView{Kind: ViewToMe}).Excludes("Sent") {
		t.Error("the other views keep excluding Sent")
	}
}

func TestDeleteMailRemovesParticipants(t *testing.T) {
	idx := openTestUserIndex(t)
	if err := idx.IndexMail("g1", "s", "x", 1, 1); err != nil {
		t.Fatal(err)
	}
	setParts(t, idx, "g1", "a@x.org")
	if err := idx.DeleteMail("g1"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = idx.db.QueryRow(`SELECT COUNT(*) FROM participants WHERE mail_guid = 'g1'`).Scan(&n)
	if n != 0 {
		t.Errorf("participant rows after DeleteMail = %d", n)
	}
}

func TestContactEmails(t *testing.T) {
	c := &bongov1.Contact{
		Email:    "Ann@X.org",
		VcardRaw: []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ann\r\nEMAIL:ann@x.org\r\nEMAIL;TYPE=work:other@x.org\r\nEND:VCARD\r\n"),
	}
	if got, want := ContactEmails(c), []string{"ann@x.org", "other@x.org"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ContactEmails = %v, want %v", got, want)
	}
	if got := ContactEmails(&bongov1.Contact{}); len(got) != 0 {
		t.Errorf("no email = %v", got)
	}
}
