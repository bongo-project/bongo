package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestListPostAddress(t *testing.T) {
	long := "<mailto:" + strings.Repeat("a", 330) + "@example.org>"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"mailto", "<mailto:list@example.org>", "list@example.org"},
		{"uppercase scheme", "<MAILTO:list@example.org>", "list@example.org"},
		{"NO", "NO", ""},
		{"no lowercase", "no", ""},
		{"NO before a mailto", "NO <mailto:list@example.org>", ""},
		{"NO with comment", "NO (posting not allowed)", ""},
		{"https only", "<https://example.org/post>", ""},
		{"https then mailto", "<https://example.org/post>, <mailto:list@example.org>", "list@example.org"},
		{"mailto with query", "<mailto:list@example.org?subject=hello>", "list@example.org"},
		{"two addresses", "<mailto:a@example.org,b@example.org>", ""},
		{"percent-encoded at", "<mailto:list%40example.org>", "list@example.org"},
		{"encoded comma", "<mailto:a@example.org%2Cb@example.org>", ""},
		{"encoded CRLF", "<mailto:list@example.org%0D%0ABcc:evil@example.org>", ""},
		{"raw CRLF", "<mailto:list@example.org>\r\nBcc: evil@example.org", ""},
		{"over 320 bytes", long, ""},
		{"display name", "<mailto:Jane <jane@example.org>>", ""},
		{"display name form", "<mailto:Jane%20Doe%20%3Cjane@example.org%3E>", ""},
		{"invalid address", "<mailto:not-an-address>", ""},
		{"no brackets", "mailto:list@example.org", ""},
		{"unterminated", "<mailto:list@example.org", ""},
		{"empty", "", ""},
		{"spaces", "   ", ""},
		{"bad escape", "<mailto:list%zz@example.org>", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listPostAddress(tt.in); got != tt.want {
				t.Errorf("listPostAddress(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestReplyToAddresses(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "reply@example.org", []string{"reply@example.org"}},
		{"display name dropped", "Team <reply@example.org>", []string{"reply@example.org"}},
		{"multiple", "a@example.org, B <b@example.org>", []string{"a@example.org", "b@example.org"}},
		{"invalid", "not an address", nil},
		{"one invalid in list", "a@example.org, nope", nil},
		{"raw CRLF", "a@example.org\r\nBcc: b@example.org", nil},
		{"empty", "", nil},
		{"too many", strings.Repeat("a@example.org, ", maxReplyToAddresses) + "z@example.org", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replyToAddresses(tt.in)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("replyToAddresses(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// ownResolver is a localRecipientResolver whose strict and routing lookups
// differ, like the real one: account maps primary, alias and domain-alias
// addresses; route also maps catch-all addresses.
type ownResolver struct {
	account map[string]string
	route   map[string]string
}

func (r ownResolver) ResolveAccount(email string) (string, error) {
	if id, ok := r.account[strings.ToLower(email)]; ok {
		return id, nil
	}
	return "", errors.New("not an account")
}

func (r ownResolver) ResolveRecipient(email string) (string, error) {
	if id, ok := r.route[strings.ToLower(email)]; ok {
		return id, nil
	}
	return r.ResolveAccount(email)
}

// putRawMessage stores raw as a blob and delivers a message pointing at it.
func putRawMessage(t *testing.T, env *composeSendAsEnv, uid string, meta *bongov1.MailMeta, raw string) {
	t.Helper()
	if raw != "" {
		hash, err := env.blobs.Put([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		meta.RawMessage = &bongov1.BlobRef{Hash: hash, ContentType: "message/rfc822"}
	}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
}

// fieldValue pulls the value of the named input out of the rendered form.
func fieldValue(body, name string) string {
	i := strings.Index(body, `name="`+name+`"`)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	j := strings.Index(rest, `value="`)
	if j < 0 {
		return ""
	}
	rest = rest[j+len(`value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func replyListRequest(t *testing.T, env *composeSendAsEnv, uid, email, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := withUserContext(httptest.NewRequest(http.MethodGet, "/compose/reply-list/INBOX/1"+query, nil), uid, email)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", "INBOX")
	rctx.URLParams.Add("uid", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	env.handler.ReplyList(w, req)
	return w
}

func TestReplyAddressesReplyTo(t *testing.T) {
	const hdr = "From: Bob <bob@example.com>\r\nTo: alice@example.com\r\nSubject: Hi\r\n"
	tests := []struct {
		name   string
		meta   *bongov1.MailMeta
		raw    string
		wantTo string
	}{
		{"no Reply-To uses From", &bongov1.MailMeta{From: "bob@example.com"}, "", "bob@example.com"},
		{"stored Reply-To", &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "Team <team@example.org>"}, "", "team@example.org"},
		{"multiple Reply-To", &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "a@example.org, b@example.org"}, "", "a@example.org, b@example.org"},
		{"invalid Reply-To falls back to From", &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "not an address"}, "", "bob@example.com"},
		{"empty meta filled from blob", &bongov1.MailMeta{From: "bob@example.com"}, hdr + "Reply-To: Blob Team <blob@example.org>\r\n\r\nBody\r\n", "blob@example.org"},
		{"invalid blob Reply-To falls back", &bongov1.MailMeta{From: "bob@example.com"}, hdr + "Reply-To: nonsense\r\n\r\nBody\r\n", "bob@example.com"},
		{"encoded name with a comma in the blob", &bongov1.MailMeta{From: "bob@example.com"}, hdr + "Reply-To: =?UTF-8?Q?M=C3=BCller=2C_Hans?= <hans@example.org>\r\n\r\nBody\r\n", "hans@example.org"},
		{"encoded name with a comma stored raw", &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "=?UTF-8?Q?M=C3=BCller=2C_Hans?= <hans@example.org>"}, "", "hans@example.org"},
		{"stored beats blob", &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "stored@example.org"}, hdr + "Reply-To: blob@example.org\r\n\r\nBody\r\n", "stored@example.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, uid, email := newPipelineEnv(t)
			tt.meta.Guid = "g-rt"
			tt.meta.Subject = "Hi"
			putRawMessage(t, env, uid, tt.meta, tt.raw)
			if got := fieldValue(replyPage(t, env, uid, email, ""), "to"); got != tt.wantTo {
				t.Errorf("To = %q, want %q", got, tt.wantTo)
			}
		})
	}
}

func TestReplyAllStripsOwnAddresses(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.SetLocalResolver(ownResolver{
		account: map[string]string{
			"alice@example.com": uid,
			"alias@example.com": uid,
			"da@dom-alias.test": uid,
			"other@example.com": "u-bob",
			"bob@example.com":   "u-bob",
			"dana@example.com":  "u-dana",
			"erin@example.com":  "u-erin",
			"claim@example.com": "u-nobody",
			"ident@example.com": "u-nobody",
		},
		route: map[string]string{"catch@example.test": uid, "postmaster@example.test": uid},
	})
	env.seedAddress(t, "ident@example.com", uid, "example.com", true, true)
	env.seedAddress(t, "claim@example.com", uid, "example.com", true, false)

	meta := &bongov1.MailMeta{
		Guid: "g-own", Subject: "Plan", From: "bob@example.com",
		To: []string{
			"Alice <alice@example.com>", "alias@example.com", "da@dom-alias.test", "ident@example.com",
			"ALICE@Example.COM", "Me <alice@example.com>", "claim@example.com", "catch@example.test",
			"postmaster@example.test", "dana@example.com",
		},
		Cc: []string{"erin@example.com", "bob@example.com", "dana@example.com"},
	}
	putRawMessage(t, env, uid, meta, "")

	cc := fieldValue(replyPage(t, env, uid, email, "all=1"), "cc")
	got := strings.Split(cc, ", ")
	sort.Strings(got)
	want := []string{"catch@example.test", "claim@example.com", "dana@example.com", "erin@example.com", "postmaster@example.test"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Cc = %v, want %v", got, want)
	}
}

// TestReplyAllEdgeNodeStripsPrimaryOnly: without a resolver or address store
// the session primary (any case, with a display name) is still removed, and
// nothing else is.
func TestReplyAllEdgeNodeStripsPrimaryOnly(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.SetLocalResolver(nil)
	env.handler.SetAddressStore(nil)
	meta := &bongov1.MailMeta{
		Guid: "g-edge", Subject: "Plan", From: "bob@example.com",
		To: []string{"Me <ALICE@example.com>", "alias@example.com", "dana@example.com"},
	}
	putRawMessage(t, env, uid, meta, "")
	if cc := fieldValue(replyPage(t, env, uid, email, "all=1"), "cc"); cc != "alias@example.com, dana@example.com" {
		t.Errorf("Cc = %q", cc)
	}
}

// TestReplyAllQuotedCommaName: the stored To is a naive comma split, so the
// envelope header must be parsed instead.
func TestReplyAllQuotedCommaName(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	raw := "From: Bob <bob@example.com>\r\nTo: \"Doe, John\" <john@example.com>, Alice <alice@example.com>\r\n" +
		"Cc: Erin <erin@example.com>\r\nSubject: Hi\r\n\r\nBody\r\n"
	meta := &bongov1.MailMeta{
		Guid: "g-comma", Subject: "Hi", From: "Bob <bob@example.com>",
		To: []string{`"Doe`, `John" <john@example.com>`, "Alice <alice@example.com>"},
		Cc: []string{"Erin <erin@example.com>"},
	}
	putRawMessage(t, env, uid, meta, raw)
	cc := fieldValue(replyPage(t, env, uid, email, "all=1"), "cc")
	if cc != "john@example.com, erin@example.com" {
		t.Errorf("Cc = %q, want john and erin only", cc)
	}
}

// TestReplyAllDropsDuplicatesOfTo: Reply-To addresses and the From address are
// not repeated in Cc.
func TestReplyAllDropsDuplicatesOfTo(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	meta := &bongov1.MailMeta{
		Guid: "g-dup", Subject: "Hi", From: "Bob <bob@example.com>", ReplyTo: "team@example.org",
		To: []string{"alice@example.com", "TEAM@example.org", "dana@example.com"},
		Cc: []string{"dana@example.com"},
	}
	putRawMessage(t, env, uid, meta, "")
	body := replyPage(t, env, uid, email, "all=1")
	if to := fieldValue(body, "to"); to != "team@example.org" {
		t.Errorf("To = %q", to)
	}
	if cc := fieldValue(body, "cc"); cc != "dana@example.com" {
		t.Errorf("Cc = %q, want dana once", cc)
	}
}

// TestReplyToExcludesOwnAddresses runs the real store.RecipientResolver (domain
// alias rewrite, legacy alias tier, verified send-as) through the reply
// handler. The user's own addresses never stay in To; when nothing foreign is
// left the original recipients are addressed, and a note to self falls back to
// the primary. Plain reply and reply-all agree on To.
func TestReplyToExcludesOwnAddresses(t *testing.T) {
	tests := []struct {
		name   string
		meta   *bongov1.MailMeta
		wantTo string
		wantCc string // reply-all only
	}{
		{
			name:   "Reply-To own alias-domain address dropped",
			meta:   &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "alice@alias.test, team@example.org"},
			wantTo: "team@example.org",
		},
		{
			name:   "Reply-To all own falls back to From",
			meta:   &bongov1.MailMeta{From: "Bob <bob@example.com>", ReplyTo: "alice@alias.test, ALICE@example.com", To: []string{"alice@example.com", "dana@example.com"}},
			wantTo: "Bob &lt;bob@example.com&gt;", wantCc: "dana@example.com",
		},
		{
			name:   "Reply-To on a catch-all address is not own",
			meta:   &bongov1.MailMeta{From: "bob@example.com", ReplyTo: "ghost@example.com"},
			wantTo: "ghost@example.com",
		},
		{
			name:   "own sent via legacy alias",
			meta:   &bongov1.MailMeta{From: "old@example.com", To: []string{"bob@example.com", "alice@alias.test"}, Cc: []string{"dana@example.com", "ALICE@example.com"}},
			wantTo: "bob@example.com", wantCc: "dana@example.com",
		},
		{
			name:   "own sent via verified send-as identity",
			meta:   &bongov1.MailMeta{From: "me@sendas.test", To: []string{"bob@example.com"}},
			wantTo: "bob@example.com",
		},
		{
			name:   "own sent with Reply-To pointing at self",
			meta:   &bongov1.MailMeta{From: "Me <alice@example.com>", ReplyTo: "alice@alias.test", To: []string{"bob@example.com"}},
			wantTo: "bob@example.com",
		},
		{
			name:   "own sent: To recipients deduplicated and not repeated in Cc",
			meta:   &bongov1.MailMeta{From: "alice@example.com", To: []string{"bob@example.com", "BOB@example.com", "dana@example.com"}, Cc: []string{"bob@example.com", "erin@example.com"}},
			wantTo: "bob@example.com, dana@example.com", wantCc: "erin@example.com",
		},
		{
			name:   "note to self keeps the primary",
			meta:   &bongov1.MailMeta{From: "alice@example.com", To: []string{"alice@alias.test"}},
			wantTo: "alice@example.com",
		},
		{
			name:   "no To header keeps the primary",
			meta:   &bongov1.MailMeta{From: "alice@example.com"},
			wantTo: "alice@example.com",
		},
		{
			name:   "foreign From without Reply-To is unchanged",
			meta:   &bongov1.MailMeta{From: "Bob <bob@example.com>", To: []string{"alice@example.com", "dana@example.com"}},
			wantTo: "Bob &lt;bob@example.com&gt;", wantCc: "dana@example.com",
		},
	}
	for _, tt := range tests {
		for _, all := range []bool{false, true} {
			name := tt.name + "/reply"
			query := ""
			if all {
				name = tt.name + "/all"
				query = "all=1"
			}
			t.Run(name, func(t *testing.T) {
				env, uid, email := newPipelineEnv(t)
				// The alias domain must be hosted, as must its target, or the
				// rewrite silently does nothing.
				for k, v := range map[string]string{
					"/system/domain/example.com":         "enabled",
					"/system/domain/alias.test":          "enabled",
					"/system/domain/alias.test/alias_of": "example.com",
					"/system/alias/old@example.com":      "alice@example.com",
				} {
					if err := env.ps.Set([]byte(k), []byte(v)); err != nil {
						t.Fatal(err)
					}
				}
				if err := env.ps.Set(store.CatchAllKey("example.com"), []byte(uid)); err != nil {
					t.Fatal(err)
				}
				env.seedAddress(t, "me@sendas.test", uid, "sendas.test", true, true)

				tt.meta.Guid = "g-own-to"
				tt.meta.Subject = "Hi"
				putRawMessage(t, env, uid, tt.meta, "")
				body := replyPage(t, env, uid, email, query)
				if got := fieldValue(body, "to"); got != tt.wantTo {
					t.Errorf("To = %q, want %q", got, tt.wantTo)
				}
				wantCc := ""
				if all {
					wantCc = tt.wantCc
				}
				if got := fieldValue(body, "cc"); got != wantCc {
					t.Errorf("Cc = %q, want %q", got, wantCc)
				}
			})
		}
	}
}

// TestReplyAllCapsCandidates: a huge To header does not reach the resolver for
// every address.
func TestReplyAllCapsCandidates(t *testing.T) {
	var addrs []string
	for i := range 200 {
		addrs = append(addrs, fmt.Sprintf("u%d@example.com", i))
	}
	if got := replyAllCandidates(nil, addrs, nil); len(got) != maxReplyAllAddresses {
		t.Errorf("candidates = %d, want %d", len(got), maxReplyAllAddresses)
	}
}

func TestReplyList(t *testing.T) {
	tests := []struct {
		name     string
		meta     *bongov1.MailMeta
		raw      string
		query    string
		wantCode int
		wantTo   string
	}{
		{"stored mailto", &bongov1.MailMeta{From: "bob@example.com", ListPost: "<mailto:list@example.org>", To: []string{"dana@example.com"}}, "", "", http.StatusOK, "list@example.org"},
		{"all ignored", &bongov1.MailMeta{From: "bob@example.com", ListPost: "<mailto:list@example.org>", To: []string{"dana@example.com"}}, "", "?all=1", http.StatusOK, "list@example.org"},
		{"blob fallback", &bongov1.MailMeta{From: "bob@example.com"}, "From: bob@example.com\r\nTo: alice@example.com\r\nList-Post: <mailto:blob@example.org>\r\nSubject: Hi\r\n\r\nBody\r\n", "", http.StatusOK, "blob@example.org"},
		{"no List-Post", &bongov1.MailMeta{From: "bob@example.com"}, "", "", http.StatusSeeOther, ""},
		{"List-Post NO", &bongov1.MailMeta{From: "bob@example.com", ListPost: "NO"}, "", "", http.StatusSeeOther, ""},
		{"https only", &bongov1.MailMeta{From: "bob@example.com", ListPost: "<https://example.org/post>"}, "", "", http.StatusSeeOther, ""},
		{"URL cannot supply the address", &bongov1.MailMeta{From: "bob@example.com"}, "", "?to=evil@example.net&list=evil@example.net", http.StatusSeeOther, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, uid, email := newPipelineEnv(t)
			tt.meta.Guid = "g-rl"
			tt.meta.Subject = "Hi"
			putRawMessage(t, env, uid, tt.meta, tt.raw)
			w := replyListRequest(t, env, uid, email, tt.query)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				if loc := w.Header().Get("Location"); loc != "/compose" {
					t.Errorf("Location = %q", loc)
				}
				return
			}
			body := w.Body.String()
			if to := fieldValue(body, "to"); to != tt.wantTo {
				t.Errorf("To = %q, want %q", to, tt.wantTo)
			}
			if cc := fieldValue(body, "cc"); cc != "" {
				t.Errorf("Cc = %q, want empty", cc)
			}
			if !strings.Contains(body, `value="reply"`) {
				t.Error("form is not in reply mode")
			}
		})
	}
}
