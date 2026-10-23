package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// composeSendAsEnv is a test fixture wiring compose handler with AddressStore.
type composeSendAsEnv struct {
	ps        *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	meta      *store.MetadataManager
	addresses *store.AddressStore
	qs        *queue.QueueStore
	applier   *directApplierTags
	handler   *ComposeHandler
	spool     *fakeSpool
}

func newComposeSendAsEnv(t *testing.T) *composeSendAsEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	meta, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	as := store.NewAddressStore(ps, applier, meta.System)
	qs := queue.NewQueueStore(ps, applier)
	resolver := store.NewRecipientResolver(as, ps, meta.System)

	h := NewComposeHandler(ps, blobs, mailboxes, nil, applier, discardLogger())
	h.SetAddressStore(as)
	h.SetLocalResolver(resolver)
	h.SetQueueStore(qs)
	spool := &fakeSpool{}
	h.SetSpool(spool)

	return &composeSendAsEnv{
		ps:        ps,
		blobs:     blobs,
		mailboxes: mailboxes,
		meta:      meta,
		addresses: as,
		qs:        qs,
		applier:   applier,
		handler:   h,
		spool:     spool,
	}
}

// seedUser inserts user into SQLite users table and seeds primary address in Pebble.
func (e *composeSendAsEnv) seedUser(t *testing.T, userID, email string) {
	t.Helper()
	_, err := e.meta.System.DB().Exec(
		`INSERT INTO users (id, username, display_name, enabled, created_at, modified_at)
		 VALUES (?, ?, ?, 1, unixepoch(), unixepoch())
		 ON CONFLICT(id) DO NOTHING`,
		userID, email, "Test User",
	)
	if err != nil {
		t.Fatalf("seedUser %q: %v", email, err)
	}
}

// seedAddress stores an AddressRecord in the AddressStore (Pebble + SQLite).
func (e *composeSendAsEnv) seedAddress(t *testing.T, address, userID, domain string, canSend, verified bool) {
	t.Helper()
	err := e.addresses.Put(address, &bongov1.AddressRecord{
		UserId:   userID,
		Domain:   domain,
		Kind:     "primary",
		CanSend:  canSend,
		Verified: verified,
	})
	if err != nil {
		t.Fatalf("seedAddress %q: %v", address, err)
	}
}

// rawSentMessage retrieves the raw RFC 5322 bytes of the first message in Sent.
func (e *composeSendAsEnv) rawSentMessage(t *testing.T, userID string) []byte {
	t.Helper()

	msgs, err := e.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages in Sent mailbox")
	}

	key := fmt.Sprintf("/user/%s/mail/%s", userID, msgs[0].MailGuid)
	data, err := e.ps.Get([]byte(key))
	if err != nil {
		t.Fatalf("get MailMeta key %s: %v", key, err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	if meta.RawMessage == nil {
		t.Fatal("MailMeta.RawMessage is nil")
	}

	rc, err := e.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		t.Fatalf("BlobStore.Get: %v", err)
	}
	defer rc.Close()

	raw, err := func() ([]byte, error) {
		var buf bytes.Buffer
		_, err := buf.ReadFrom(rc)
		return buf.Bytes(), err
	}()
	if err != nil {
		t.Fatalf("ReadAll blob: %v", err)
	}
	return raw
}

// mailMetaForSent returns the MailMeta for the first Sent message.
func (e *composeSendAsEnv) mailMetaForSent(t *testing.T, userID string) *bongov1.MailMeta {
	t.Helper()

	msgs, err := e.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages in Sent mailbox")
	}

	key := fmt.Sprintf("/user/%s/mail/%s", userID, msgs[0].MailGuid)
	data, err := e.ps.Get([]byte(key))
	if err != nil {
		t.Fatalf("get MailMeta key %s: %v", key, err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	return &meta
}

// TestBuildFromPickerOptions verifies that buildFromPickerOptions returns only
// addresses the user owns with CanSend=true AND Verified=true.
func TestBuildFromPickerOptions(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-picker-options"
	email := "alice@example.com"

	env.seedUser(t, userID, email)

	// Primary: can_send=true, verified=true — eligible.
	env.seedAddress(t, "alice@example.com", userID, "example.com", true, true)

	// Alias B: can_send=true, verified=true — eligible.
	env.seedAddress(t, "b@d2.com", userID, "d2.com", true, true)

	// Alias C: can_send=false, verified=false — NOT eligible.
	env.seedAddress(t, "c@example.com", userID, "example.com", false, false)

	// Alias D: can_send=true, verified=false — NOT eligible.
	env.seedAddress(t, "d@example.com", userID, "example.com", true, false)

	// Another user's address: not owned by userID — must never appear.
	env.seedAddress(t, "other@example.com", "other-user", "example.com", true, true)

	opts, sel := env.handler.buildFromPickerOptions(userID, email)

	// Selected must default to the primary email.
	if !strings.EqualFold(sel, email) {
		t.Errorf("selected = %q, want %q", sel, email)
	}

	// Check eligible addresses appear.
	optSet := make(map[string]bool)
	for _, o := range opts {
		optSet[strings.ToLower(o)] = true
	}

	if !optSet["alice@example.com"] {
		t.Error("picker missing eligible primary alice@example.com")
	}
	if !optSet["b@d2.com"] {
		t.Error("picker missing eligible alias b@d2.com")
	}

	// Ineligible addresses must not appear.
	if optSet["c@example.com"] {
		t.Error("picker includes ineligible (can_send=false) c@example.com")
	}
	if optSet["d@example.com"] {
		t.Error("picker includes ineligible (verified=false) d@example.com")
	}
	if optSet["other@example.com"] {
		t.Error("picker includes other user's address other@example.com")
	}
}

// TestBuildFromPickerOptions_NilAddressStore verifies that when AddressStore is
// nil, the picker falls back to a single-entry list with the primary email.
func TestBuildFromPickerOptions_NilAddressStore(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	_ = blobs

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	h := NewComposeHandler(ps, blobs, mailboxes, nil, applier, discardLogger())
	// No SetAddressStore — h.addresses is nil.

	opts, sel := h.buildFromPickerOptions("user1", "primary@example.com")
	if len(opts) != 1 || opts[0] != "primary@example.com" {
		t.Errorf("expected [primary@example.com], got %v", opts)
	}
	if sel != "primary@example.com" {
		t.Errorf("selected = %q, want primary@example.com", sel)
	}
}

// TestComposeSendAs_DomainAligned verifies that selecting a send-as address
// sets the From header, meta.From, Message-ID host, and envelope SenderEmail
// to the chosen address/domain.
func TestComposeSendAs_DomainAligned(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-sendas-domain"
	primaryEmail := "alice@example.com"
	sendAsEmail := "b@d2.com"

	env.seedUser(t, userID, primaryEmail)

	// Primary: eligible (can_send+verified).
	env.seedAddress(t, primaryEmail, userID, "example.com", true, true)
	// Send-as address: eligible.
	env.seedAddress(t, sendAsEmail, userID, "d2.com", true, true)

	// External recipient to trigger enqueue so we can check SenderEmail.
	externalRecipient := "recipient@external.example"

	form := url.Values{
		"to":      {externalRecipient},
		"subject": {"Send-as test"},
		"body":    {"hello from b@d2.com"},
		"from":    {sendAsEmail},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, primaryEmail)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	// Check Sent copy meta.From.
	meta := env.mailMetaForSent(t, userID)
	if !strings.EqualFold(meta.From, sendAsEmail) {
		t.Errorf("meta.From = %q, want %q", meta.From, sendAsEmail)
	}

	// Check raw From header.
	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	fromHeader := msg.Header.Get("From")
	if !strings.Contains(strings.ToLower(fromHeader), strings.ToLower(sendAsEmail)) {
		t.Errorf("From header = %q, want to contain %q", fromHeader, sendAsEmail)
	}

	// Check Message-ID host == d2.com.
	msgID := msg.Header.Get("Message-ID")
	if !strings.Contains(msgID, "@d2.com") {
		t.Errorf("Message-ID = %q, want host d2.com", msgID)
	}

	// Check enqueued OutboundMessage.SenderEmail == sendAsEmail.
	pending, err := env.qs.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("no messages enqueued for external recipient")
	}
	if !strings.EqualFold(pending[0].SenderEmail, sendAsEmail) {
		t.Errorf("OutboundMessage.SenderEmail = %q, want %q", pending[0].SenderEmail, sendAsEmail)
	}
	// Web compose must be marked LOCAL so the queue worker DKIM-signs it.
	if got := pending[0].GetOrigin(); got != bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL {
		t.Errorf("OutboundMessage.Origin = %v, want LOCAL", got)
	}
}

// TestComposeSendAs_DomainAligned_RawFromHeaderDrivesDKIM verifies that after
// send-as, the From header domain == d2.com so that a DKIMRegistry keyed for
// "d2.com" would sign the message with d=d2.com. We assert on the From header
// domain (not the DKIM-Signature itself, which requires a real key) since the
// registry keys on the From-header domain.
func TestComposeSendAs_DomainAligned_RawFromHeaderDrivesDKIM(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-sendas-dkim"
	primaryEmail := "alice@example.com"
	sendAsEmail := "b@d2.com"

	env.seedUser(t, userID, primaryEmail)
	env.seedAddress(t, primaryEmail, userID, "example.com", true, true)
	env.seedAddress(t, sendAsEmail, userID, "d2.com", true, true)

	// Local recipient so we don't need an external delivery.
	localRecipient := "alice@example.com"

	form := url.Values{
		"to":      {localRecipient},
		"subject": {"DKIM domain test"},
		"body":    {"checking from domain"},
		"from":    {sendAsEmail},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, primaryEmail)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	fromHeader := msg.Header.Get("From")
	// The From header domain must be d2.com so a DKIMRegistry keyed on "d2.com" would sign it.
	if !strings.Contains(strings.ToLower(fromHeader), "@d2.com") {
		t.Errorf("From header = %q: domain must be d2.com for DKIM registry to select the d2 signer", fromHeader)
	}
}

// TestComposeSendAs_DefaultFrom_NoAddressStoreInHandler verifies backward-compat:
// when AddressStore is nil and from is empty, send succeeds with the session email.
func TestComposeSendAs_DefaultFrom_NoAddressStoreInHandler(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-default-no-store"
	email := "alice@example.com"

	env.seedUser(t, userID, email)

	// Remove the address store from the handler to simulate old behaviour.
	env.handler.addresses = nil

	form := url.Values{
		"to":      {email},
		"subject": {"Default from test"},
		"body":    {"hello"},
		// no "from" field
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect (backward-compat), got %d: %s", w.Code, w.Body.String())
	}

	meta := env.mailMetaForSent(t, userID)
	if !strings.EqualFold(meta.From, email) {
		t.Errorf("meta.From = %q, want %q (session email)", meta.From, email)
	}
}

// TestComposeSendAs_TamperedFrom_UnownedAddress verifies that a tampered from
// field pointing to an address NOT owned by the session user is rejected 403.
func TestComposeSendAs_TamperedFrom_UnownedAddress(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-tampered-unowned"
	email := "alice@example.com"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)

	// Another user's address — tampered form submission.
	env.seedAddress(t, "evil@evil.com", "other-user", "evil.com", true, true)

	form := url.Values{
		"to":      {email},
		"subject": {"Tampered from"},
		"body":    {"attack"},
		"from":    {"evil@evil.com"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 403/400 for unowned tampered from, got %d: %s", w.Code, w.Body.String())
	}

	// Assert nothing was stored in Sent.
	msgs, err := env.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected no Sent messages after rejection, got %d", len(msgs))
	}
}

// TestComposeSendAs_TamperedFrom_OwnedButNotVerified verifies that a tampered
// from that is owned by the user but NOT (can_send && verified) is rejected 403.
func TestComposeSendAs_TamperedFrom_OwnedButNotVerified(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-tampered-notverified"
	email := "alice@example.com"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)

	// Owned address but not can_send/verified — simulates admin-added not-yet-granted.
	env.seedAddress(t, "alias@notverified.com", userID, "notverified.com", false, false)

	form := url.Values{
		"to":      {email},
		"subject": {"Tampered not-verified from"},
		"body":    {"attack"},
		"from":    {"alias@notverified.com"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 403/400 for not-verified tampered from, got %d: %s", w.Code, w.Body.String())
	}

	// Nothing must have been stored.
	msgs, err := env.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected no Sent messages after rejection, got %d", len(msgs))
	}
}

// TestComposeSendAs_TamperedFrom_NotFoundInStore verifies that a tampered from
// for an address that doesn't exist at all is rejected 403.
func TestComposeSendAs_TamperedFrom_NotFoundInStore(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-tampered-notfound"
	email := "alice@example.com"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)

	form := url.Values{
		"to":      {email},
		"subject": {"From not in store"},
		"body":    {"attack"},
		"from":    {"nonexistent@nowhere.com"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 403/400 for non-existent from, got %d: %s", w.Code, w.Body.String())
	}
}

// TestComposeSendAs_DefaultFrom_SameAsSession verifies that submitting from=
// equal to the session email bypasses AddressStore enforcement (back-compat gate).
func TestComposeSendAs_DefaultFrom_SameAsSession(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-default-same"
	email := "alice@example.com"

	env.seedUser(t, userID, email)
	// No address in store for the primary — simulates pre-migration node.
	// The handler should still accept from=<session-email>.

	form := url.Values{
		"to":      {email},
		"subject": {"Same-as-session from"},
		"body":    {"hello"},
		"from":    {email}, // explicitly set to session email
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect (same-as-session bypass), got %d: %s", w.Code, w.Body.String())
	}
}

// TestComposeSendAs_Rejection_LeavesNoPartialState verifies that a send-as
// rejection (403) does not store a Sent copy, blob, or enqueued message.
func TestComposeSendAs_Rejection_LeavesNoPartialState(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-rejection-partial"
	email := "alice@example.com"

	env.seedUser(t, userID, email)
	env.seedAddress(t, email, userID, "example.com", true, true)

	form := url.Values{
		"to":      {email},
		"subject": {"No partial state"},
		"body":    {"attack body"},
		"from":    {"attacker@evil.com"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
		t.Fatalf("expected 403/400, got %d: %s", w.Code, w.Body.String())
	}

	// Sent mailbox must be empty.
	msgs, err := env.mailboxes.ListMessages(userID, "Sent")
	if err != nil {
		t.Fatalf("ListMessages(Sent): %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty Sent after rejection, got %d messages", len(msgs))
	}

	// Queue must be empty.
	pending, err := env.qs.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected empty queue after rejection, got %d messages", len(pending))
	}
}

// TestComposeSendAs_RichMode_FromAligned verifies that rich-mode composition
// with a send-as address also sets the From header correctly.
func TestComposeSendAs_RichMode_FromAligned(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-sendas-rich"
	primaryEmail := "alice@example.com"
	sendAsEmail := "b@d2.com"

	env.seedUser(t, userID, primaryEmail)
	env.seedAddress(t, primaryEmail, userID, "example.com", true, true)
	env.seedAddress(t, sendAsEmail, userID, "d2.com", true, true)

	form := url.Values{
		"to":        {primaryEmail},
		"subject":   {"Rich send-as test"},
		"body":      {"<p>hello</p>"},
		"body_mode": {"rich"},
		"from":      {sendAsEmail},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, primaryEmail)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", w.Code, w.Body.String())
	}

	meta := env.mailMetaForSent(t, userID)
	if !strings.EqualFold(meta.From, sendAsEmail) {
		t.Errorf("meta.From = %q, want %q (rich mode send-as)", meta.From, sendAsEmail)
	}

	raw := env.rawSentMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !strings.Contains(strings.ToLower(msg.Header.Get("From")), "@d2.com") {
		t.Errorf("From header = %q, want @d2.com domain (rich mode)", msg.Header.Get("From"))
	}
}

// TestComposeSendAs_CaseInsensitive verifies that the from form value is
// matched case-insensitively against the address store.
func TestComposeSendAs_CaseInsensitive(t *testing.T) {
	env := newComposeSendAsEnv(t)
	userID := "user-sendas-case"
	primaryEmail := "alice@example.com"
	sendAsEmail := "b@d2.com"

	env.seedUser(t, userID, primaryEmail)
	env.seedAddress(t, primaryEmail, userID, "example.com", true, true)
	env.seedAddress(t, sendAsEmail, userID, "d2.com", true, true)

	// Submit with uppercase FROM.
	form := url.Values{
		"to":      {primaryEmail},
		"subject": {"Case-insensitive test"},
		"body":    {"hello"},
		"from":    {"B@D2.COM"}, // uppercase
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, primaryEmail)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect (case-insensitive), got %d: %s", w.Code, w.Body.String())
	}

	// Confirm the stored From is the canonical (lowercased) send-as address.
	meta := env.mailMetaForSent(t, userID)
	if !strings.EqualFold(meta.From, sendAsEmail) {
		t.Errorf("meta.From = %q, want %q", meta.From, sendAsEmail)
	}
}

// TestComposeSendAs_PickerExcludesOtherUserAddresses ensures that when two users
// exist with overlapping domains, each user only sees their own addresses in the picker.
func TestComposeSendAs_PickerExcludesOtherUserAddresses(t *testing.T) {
	env := newComposeSendAsEnv(t)

	// Two users with verified+can_send addresses on the same domain.
	alice := "user-alice-isolation"
	aliceEmail := "alice@shared.com"
	bob := "user-bob-isolation"
	bobEmail := "bob@shared.com"

	env.seedUser(t, alice, aliceEmail)
	env.seedUser(t, bob, bobEmail)

	env.seedAddress(t, aliceEmail, alice, "shared.com", true, true)
	env.seedAddress(t, bobEmail, bob, "shared.com", true, true)

	// Alice's picker must not include bob's address.
	opts, _ := env.handler.buildFromPickerOptions(alice, aliceEmail)
	for _, o := range opts {
		if strings.EqualFold(o, bobEmail) {
			t.Errorf("Alice's picker includes Bob's address %q — cross-account exposure", bobEmail)
		}
	}

	// Bob's picker must not include alice's address.
	opts, _ = env.handler.buildFromPickerOptions(bob, bobEmail)
	for _, o := range opts {
		if strings.EqualFold(o, aliceEmail) {
			t.Errorf("Bob's picker includes Alice's address %q — cross-account exposure", aliceEmail)
		}
	}
}
