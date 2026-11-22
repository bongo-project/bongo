package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	sharedMemberID = "u-member"
	sharedOtherID  = "u-other"
	sharedAcctID   = "u-support"
	sharedAddr     = "support@example.com"
)

type overQuotaFor map[string]bool

func (q overQuotaFor) RecipientWouldExceedQuota(userID string, _ int64) (bool, error) {
	return q[userID], nil
}

// newSharedComposeEnv seeds a member, a non-member and a shared inbox whose
// only member is the member, with grants synced as the admin handler does.
func newSharedComposeEnv(t *testing.T, sharedFolders ...string) (*composeSendAsEnv, *store.ACLStore) {
	t.Helper()
	env := newComposeSendAsEnv(t)
	acl := store.NewACLStore(env.ps, env.applier)
	env.handler.SetACLStore(acl)

	for id, email := range map[string]string{sharedMemberID: "member@example.com", sharedOtherID: "other@example.com"} {
		env.seedUser(t, id, email)
		env.seedAddress(t, email, id, "example.com", true, true)
		putComposeProfile(t, env, id, &bongov1.UserProfile{Email: email})
		if err := env.mailboxes.EnsureDefaultMailboxes(id); err != nil {
			t.Fatal(err)
		}
	}
	putComposeProfile(t, env, sharedAcctID, &bongov1.UserProfile{Email: sharedAddr, Kind: store.SharedInboxKind, SharedMembers: []string{store.GranteeUser(sharedMemberID)}})
	env.seedAddress(t, sharedAddr, sharedAcctID, "example.com", true, true)
	if len(sharedFolders) == 0 {
		sharedFolders = store.DefaultMailboxes
	}
	for _, f := range sharedFolders {
		if err := env.mailboxes.CreateMailbox(sharedAcctID, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SyncSharedInboxGrants(acl, env.mailboxes, sharedAcctID, []string{store.GranteeUser(sharedMemberID)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return env, acl
}

func putComposeProfile(t *testing.T, env *composeSendAsEnv, id string, p *bongov1.UserProfile) {
	t.Helper()
	data, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatal(err)
	}
}

func sendFrom(env *composeSendAsEnv, userID, email, from, sid string) *httptest.ResponseRecorder {
	form := url.Values{"send_id": {sid}, "to": {"rcpt@external.example"}, "subject": {"Hello"}, "body": {"hi"}, "from": {from}}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	return w
}

func sentCount(t *testing.T, env *composeSendAsEnv, userID string) int {
	t.Helper()
	msgs, err := env.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		return 0
	}
	return len(msgs)
}

func TestComposeSharedFromPicker(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	opts, _ := env.handler.buildFromPickerOptions(sharedMemberID, "member@example.com")
	if !containsFold(opts, sharedAddr) {
		t.Errorf("member's picker = %v, want %s", opts, sharedAddr)
	}
	opts, _ = env.handler.buildFromPickerOptions(sharedOtherID, "other@example.com")
	if containsFold(opts, sharedAddr) {
		t.Errorf("non-member's picker = %v, must not list %s", opts, sharedAddr)
	}

	// A grant without membership lists nothing: rights alone never permit sending.
	acl := store.NewACLStore(env.ps, env.applier)
	id, err := env.mailboxes.EnsureMailboxID(sharedAcctID, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.SetGrant(store.ACLKindMailbox, sharedAcctID, id, store.GranteeUser(sharedOtherID), "", store.MailboxRightsWrite, store.GrantStateAccepted, sharedAcctID, time.Now()); err != nil {
		t.Fatal(err)
	}
	opts, _ = env.handler.buildFromPickerOptions(sharedOtherID, "other@example.com")
	if containsFold(opts, sharedAddr) {
		t.Errorf("grant holder who is not a member sees %v", opts)
	}
}

func containsFold(list []string, s string) bool {
	for _, e := range list {
		if strings.EqualFold(e, s) {
			return true
		}
	}
	return false
}

func TestComposeSendAsSharedSentCopies(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	sid := sendID()
	if w := sendFrom(env, sharedMemberID, "member@example.com", sharedAddr, sid); w.Code != http.StatusSeeOther {
		t.Fatalf("send = %d: %s", w.Code, w.Body.String())
	}
	if n := sentCount(t, env, sharedMemberID); n != 1 {
		t.Errorf("member Sent = %d, want 1", n)
	}
	if n := sentCount(t, env, sharedAcctID); n != 1 {
		t.Errorf("shared Sent = %d, want 1", n)
	}
	// A retry of the same send places no second copy anywhere.
	if w := sendFrom(env, sharedMemberID, "member@example.com", sharedAddr, sid); w.Code != http.StatusSeeOther {
		t.Fatalf("retry = %d: %s", w.Code, w.Body.String())
	}
	if a, b := sentCount(t, env, sharedMemberID), sentCount(t, env, sharedAcctID); a != 1 || b != 1 {
		t.Errorf("after a retry: member Sent %d, shared Sent %d, want 1 and 1", a, b)
	}
	// A send as the member's own address files nothing in the shared account.
	if w := sendFrom(env, sharedMemberID, "member@example.com", "", sendID()); w.Code != http.StatusSeeOther {
		t.Fatalf("own send = %d", w.Code)
	}
	if n := sentCount(t, env, sharedAcctID); n != 1 {
		t.Errorf("shared Sent = %d after an own-address send, want 1", n)
	}
}

func TestComposeSendAsSharedNoSentFolder(t *testing.T) {
	env, _ := newSharedComposeEnv(t, "INBOX")
	if w := sendFrom(env, sharedMemberID, "member@example.com", sharedAddr, sendID()); w.Code != http.StatusSeeOther {
		t.Fatalf("send = %d: %s", w.Code, w.Body.String())
	}
	if n := sentCount(t, env, sharedMemberID); n != 1 {
		t.Errorf("member Sent = %d, want 1", n)
	}
	if _, err := env.mailboxes.GetMailbox(sharedAcctID, "Sent"); err == nil {
		t.Error("the send created a Sent folder on the shared account")
	}
}

func TestComposeSendAsSharedOverQuota(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	env.handler.SetQuotaChecker(overQuotaFor{sharedAcctID: true})
	if w := sendFrom(env, sharedMemberID, "member@example.com", sharedAddr, sendID()); w.Code != http.StatusSeeOther {
		t.Fatalf("send = %d: %s", w.Code, w.Body.String())
	}
	if n := sentCount(t, env, sharedMemberID); n != 1 {
		t.Errorf("member Sent = %d, want 1", n)
	}
	if n := sentCount(t, env, sharedAcctID); n != 0 {
		t.Errorf("shared Sent = %d, want 0 (over quota)", n)
	}
}

func TestComposeSendAsSharedNonMember(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	w := sendFrom(env, sharedOtherID, "other@example.com", sharedAddr, sendID())
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-member send = %d, want 403", w.Code)
	}
	for _, id := range []string{sharedOtherID, sharedAcctID, sharedMemberID} {
		if n := sentCount(t, env, id); n != 0 {
			t.Errorf("%s Sent = %d after a refused send", id, n)
		}
	}
}

// A shared copy that already exists, with no send log step recorded (an attempt
// that died between the write and the marker), is found by its derived GUID and
// not placed again.
func TestComposeSendAsSharedCopyAlreadyExists(t *testing.T) {
	env, _ := newSharedComposeEnv(t)
	sid := sendID()
	guid := derivedSendID(sharedMemberID, sid, "sent-shared").String()
	if _, err := env.mailboxes.DeliverMessage(sharedAcctID, "Sent", &bongov1.MailMeta{Guid: guid, Subject: "Hello", From: sharedAddr}, store.FlagSeen); err != nil {
		t.Fatal(err)
	}
	if w := sendFrom(env, sharedMemberID, "member@example.com", sharedAddr, sid); w.Code != http.StatusSeeOther {
		t.Fatalf("send = %d: %s", w.Code, w.Body.String())
	}
	if n := sentCount(t, env, sharedAcctID); n != 1 {
		t.Fatalf("shared Sent = %d, want exactly 1", n)
	}
	if n := sentCount(t, env, sharedMemberID); n != 1 {
		t.Errorf("member Sent = %d, want 1", n)
	}
	if _, err := env.ps.Get(store.SendLogKey(sharedMemberID, sid, sendStepSentShared)); err != nil {
		t.Errorf("the step was not recorded after finding the copy: %v", err)
	}
}
