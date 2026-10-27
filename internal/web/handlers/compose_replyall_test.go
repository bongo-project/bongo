package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// replyPage renders the reply form for INBOX/1 with the given query string and
// returns the response body.
func replyPage(t *testing.T, env *composeSendAsEnv, uid, email, query string) string {
	t.Helper()
	return replyRequest(t, env, uid, email, httptest.NewRequest(http.MethodGet, "/compose/reply/INBOX/1?"+query, nil))
}

// replyPost submits the quick reply form: the fields travel in a url-encoded
// POST body, the URL carries nothing.
func replyPost(t *testing.T, env *composeSendAsEnv, uid, email string, form url.Values) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/compose/reply/INBOX/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return replyRequest(t, env, uid, email, req)
}

func replyRequest(t *testing.T, env *composeSendAsEnv, uid, email string, req *http.Request) string {
	t.Helper()
	req = withUserContext(req, uid, email)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", "INBOX")
	rctx.URLParams.Add("uid", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	env.handler.Reply(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// TestReplyAllQueryForcesAllRecipients: ?all=1 puts the other To and Cc
// recipients (minus the current user) into the Cc field even when the user's
// default reply mode is plain reply; without it the Cc field stays empty.
func TestReplyAllQueryForcesAllRecipients(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	meta := &bongov1.MailMeta{
		Guid: "g-ra", MessageId: "<ra@x>", Subject: "Plan", From: "bob@example.com",
		To: []string{email, "dana@example.com"}, Cc: []string{"erin@example.com"},
	}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}

	plain := replyPage(t, env, uid, email, "")
	for _, who := range []string{"dana@example.com", "erin@example.com"} {
		if strings.Contains(plain, who) {
			t.Errorf("plain reply leaked %s into the form", who)
		}
	}

	all := replyPage(t, env, uid, email, "all=1")
	for _, who := range []string{"dana@example.com", "erin@example.com"} {
		if !strings.Contains(all, who) {
			t.Errorf("reply-all form is missing %s", who)
		}
	}
	// The current user must never be added as their own Cc.
	cc := ccFieldValue(all)
	if strings.Contains(cc, email) {
		t.Errorf("reply-all Cc %q contains the current user %s", cc, email)
	}
}

// ccFieldValue pulls the value of the Cc input out of the rendered form.
func ccFieldValue(body string) string {
	i := strings.Index(body, `name="cc"`)
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

// TestReplyQuickTextPrefillsEscapedBody: text typed in the message view's quick
// reply card arrives in a POST body and seeds the composer body. It is plain text
// that the template escapes; markup must not survive.
func TestReplyQuickTextPrefillsEscapedBody(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	meta := &bongov1.MailMeta{Guid: "g-q", MessageId: "<q@x>", Subject: "Hi", From: "bob@example.com", To: []string{email}}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"quick": {"Thursday works <script>alert(1)</script>"}}
	body := replyPost(t, env, uid, email, q)
	if !strings.Contains(body, "Thursday works") {
		t.Fatal("quick text did not reach the composer body")
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("quick text was rendered unescaped")
	}
}

// TestReplyDefaultReplyAllPreferenceWithoutQuery: the stored DefaultReply=ALL
// preference alone, with no ?all=1, still produces reply-all recipients.
func TestReplyDefaultReplyAllPreferenceWithoutQuery(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	prefs := store.NewPreferenceStore(env.ps, env.applier)
	env.handler.SetPreferences(prefs)
	p := store.DefaultPreferences()
	p.DefaultReply = bongov1.DefaultReplyMode_REPLY_MODE_ALL
	if err := prefs.Set(uid, p); err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{
		Guid: "g-pref", MessageId: "<pref@x>", Subject: "Plan", From: "bob@example.com",
		To: []string{email, "dana@example.com"}, Cc: []string{"erin@example.com"},
	}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	cc := ccFieldValue(replyPage(t, env, uid, email, ""))
	for _, who := range []string{"dana@example.com", "erin@example.com"} {
		if !strings.Contains(cc, who) {
			t.Errorf("preference-driven reply-all Cc %q is missing %s", cc, who)
		}
	}
}

// TestReplyQuickTextIsCappedAtRuneBoundary: the quick text is cut to
// maxQuickReplyRunes runes. Three-byte runes and a count that is not a
// multiple of the cap catch both a missing cap and a byte-wise cut.
func TestReplyQuickTextIsCappedAtRuneBoundary(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	meta := &bongov1.MailMeta{Guid: "g-cap", MessageId: "<cap@x>", Subject: "Hi", From: "bob@example.com", To: []string{email}}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"quick": {strings.Repeat("€", maxQuickReplyRunes+37)}}
	body := replyPost(t, env, uid, email, q)
	if got := strings.Count(body, "€"); got != maxQuickReplyRunes {
		t.Fatalf("composer body carries %d runes of quick text, want %d", got, maxQuickReplyRunes)
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel"},
		{"héllo€", 3, "hél"},
		{"€€€€€", 2, "€€"},
		{"", 3, ""},
	}
	for _, tt := range tests {
		if got := truncateRunes(tt.in, tt.n); got != tt.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

// TestReplyQuickTextInURLIsIgnored: ?quick= on a GET is not read, so a crafted
// link cannot seed the body and the form has no reason to put text in a URL.
func TestReplyQuickTextInURLIsIgnored(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	meta := &bongov1.MailMeta{Guid: "g-qu", MessageId: "<qu@x>", Subject: "Hi", From: "bob@example.com", To: []string{email}}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	body := replyPage(t, env, uid, email, url.Values{"quick": {"Thursday works"}}.Encode())
	if strings.Contains(body, "Thursday works") {
		t.Fatal("quick text from the URL reached the composer body")
	}
}

// TestReplyAllQueryBeatsStoredPlainReplyPreference: with a preference store
// wired and the default (plain reply) mode stored, an explicit ?all=1 still
// yields reply-all, and without it the Cc stays empty.
func TestReplyAllQueryBeatsStoredPlainReplyPreference(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	prefs := store.NewPreferenceStore(env.ps, env.applier)
	env.handler.SetPreferences(prefs)
	p := store.DefaultPreferences()
	p.DefaultReply = bongov1.DefaultReplyMode_REPLY_MODE_SENDER
	if err := prefs.Set(uid, p); err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{
		Guid: "g-pq", MessageId: "<pq@x>", Subject: "Plan", From: "bob@example.com",
		To: []string{email, "dana@example.com"}, Cc: []string{"erin@example.com"},
	}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	if cc := ccFieldValue(replyPage(t, env, uid, email, "")); cc != "" {
		t.Errorf("plain reply with a plain-reply preference has Cc %q", cc)
	}
	cc := ccFieldValue(replyPage(t, env, uid, email, "all=1"))
	for _, who := range []string{"dana@example.com", "erin@example.com"} {
		if !strings.Contains(cc, who) {
			t.Errorf("?all=1 Cc %q is missing %s despite the stored preference", cc, who)
		}
	}
}

// TestReplyAllQueryIsNotUndoneByReplyAllPreference: an explicit ?all=1 behaves
// the same when the stored preference is already reply-all.
func TestReplyAllQueryIsNotUndoneByReplyAllPreference(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	prefs := store.NewPreferenceStore(env.ps, env.applier)
	env.handler.SetPreferences(prefs)
	p := store.DefaultPreferences()
	p.DefaultReply = bongov1.DefaultReplyMode_REPLY_MODE_ALL
	if err := prefs.Set(uid, p); err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{
		Guid: "g-pa", MessageId: "<pa@x>", Subject: "Plan", From: "bob@example.com",
		To: []string{email, "dana@example.com"},
	}
	if _, err := env.mailboxes.DeliverMessage(uid, "INBOX", meta, 0); err != nil {
		t.Fatal(err)
	}
	if cc := ccFieldValue(replyPage(t, env, uid, email, "all=1")); !strings.Contains(cc, "dana@example.com") {
		t.Errorf("Cc %q is missing dana", cc)
	}
}
