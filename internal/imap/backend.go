package imap

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/observability"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// quotaChecker is satisfied by *store.DomainQuotaStore, the same checker SMTP
// RCPT uses (smtp.QuotaChecker), so both protocols decide "over quota" with
// one implementation.
type quotaChecker interface {
	RecipientWouldExceedQuota(userID string, addBytes int64) (bool, error)
}

// storageGate is satisfied by *diskspace.Monitor. APPEND is refused while the
// data volume is nearly full; COPY, MOVE, STORE and EXPUNGE are not gated.
type storageGate interface {
	Low() bool
}

// session implements imapserver.Session and imapserver.SessionMove.
type session struct {
	// namespaces holds the other-users and shared prefixes: NAMESPACE reports
	// them and CREATE and RENAME refuse personal folders under them.
	namespaces store.IMAPNamespaces

	auth      profileAuthenticator
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	tags      *store.TagStore
	metadata  *store.MetadataManager
	search    *store.SearchManager
	notifier  *Notifier
	logger    *slog.Logger

	// quota gates APPEND. Nil means unlimited (dev, tests, edge).
	quota quotaChecker

	// storage gates APPEND on free disk space. Nil means never refuse.
	storage storageGate

	// Brute-force defence: per-source-IP failed-LOGIN throttling. authLimiter is
	// shared across connections; remoteIP identifies this connection's source.
	remoteIP    net.IP
	authLimiter *authlimit.Limiter

	// conn is the live connection, used to send BYE when the account is revoked.
	// Nil in unit tests that build a bare session.
	conn *imapserver.Conn

	// acl resolves shared folders. Nil means nothing under the other-users
	// prefix resolves.
	acl *store.ACLStore

	// share is what SETACL needs to resolve an identifier and write a grant.
	share shareDeps

	userID string
	// epoch is the account's credential fingerprint recorded at LOGIN; a
	// mismatch later means the password was reset.
	epoch string
	// appPassword is the lookup id of the app password that opened the
	// session, or "" for the account password. Revoking it ends the session.
	appPassword     string
	selectedMailbox string
	readOnly        bool

	// The selected folder as the store knows it. selectedMailbox stays the
	// name the client used; for a shared folder these say whose namespace it
	// lives in and what the user may do there, as of the last command. Read the
	// owner and name through sel(), never from selectedMailbox.
	selShared bool
	selOwner  string
	selName   string
	selID     string
	selAddr   string
	selRights string

	// Cached message list for sequence number mapping. This is the SERVER's
	// view. Any handler may refresh it at will, and most do, because a command
	// needs current sequence numbers to resolve the message set it was given.
	cachedMsgs []*bongov1.MailboxMessage

	// reportedUIDs is the mailbox as the CLIENT believes it to be: the UIDs it
	// has actually been sent, in order, so index i is sequence number i+1.
	//
	// This is deliberately not cachedMsgs. Poll reports the difference between
	// what the client has been told and what is really there, so its baseline
	// has to be the client's view; if it reads a field that handlers refresh,
	// the difference is erased before Poll ever runs and the update is dropped
	// rather than delayed. That was the bug: FETCH, STORE and SEARCH each
	// refresh cachedMsgs, go-imap calls Poll after the handler returns, and so
	// a message that arrived from another connection was never announced.
	//
	// Write here only where the client has actually been sent an untagged
	// EXISTS or EXPUNGE -- Select, Poll, Idle, Expunge and Move. Refreshing a
	// cache is not telling the client anything. A handler that assigns to this
	// field re-creates the original bug.
	reportedUIDs []uint32
}

// setReported records that the client has now been shown exactly these
// messages. Callers must have written the corresponding EXISTS first.
func (s *session) setReported(msgs []*bongov1.MailboxMessage) {
	uids := make([]uint32, len(msgs))
	for i, msg := range msgs {
		uids[i] = msg.Uid
	}
	s.reportedUIDs = uids
}

// dropReported records that the client has been sent an EXPUNGE for uid.
//
// It removes one UID rather than resetting the whole baseline, because the
// client may still be owed an EXISTS for messages that arrived and have not
// been announced yet. Resetting here would swallow those.
func (s *session) dropReported(uid uint32) {
	for i, reported := range s.reportedUIDs {
		if reported == uid {
			s.reportedUIDs = append(s.reportedUIDs[:i], s.reportedUIDs[i+1:]...)
			return
		}
	}
}

// Compile-time interface checks.
var (
	_ imapserver.Session     = (*session)(nil)
	_ imapserver.SessionMove = (*session)(nil)
	// NAMESPACE is advertised in server.go; without this method go-imap
	// panics on every new connection.
	_ imapserver.SessionNamespace = (*session)(nil)
	_ imapserver.SessionQuota     = (*session)(nil)
	// Advertises ACL: go-imap adds the capability when the session has it.
	_ imapserver.SessionACL = (*session)(nil)
)

// Close is called when the connection is closed.
func (s *session) Close() error {
	return nil
}

// errAuthRateLimited is returned once a source IP has failed LOGIN too many
// times. The "rate limit" text lets clients (and operators) distinguish
// throttling from ordinary bad credentials.
var errAuthRateLimited = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeLimit,
	Text: "Too many failed login attempts; rate limit exceeded, try again later",
}

// errMailboxReadOnly is returned for a command that would modify a mailbox
// selected with EXAMINE rather than SELECT.
//
// RFC 3501 section 6.3.2: EXAMINE is identical to SELECT except that "no
// changes to the permanent state of the mailbox, including per-user state, are
// permitted". s.readOnly records which of the two opened the mailbox; before
// this it was consulted in exactly one place -- suppressing the implicit \Seen
// on FETCH -- so STORE, EXPUNGE and MOVE all happily mutated a mailbox the
// client had been told was read-only. go-imap does not enforce it either: the
// library only emits the READ-ONLY response code at select time.
//
// TRYCREATE is deliberately not used; the correct code here is none, with a NO
// response, since retrying will not help until the client re-SELECTs.
var errMailboxReadOnly = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Text: "Mailbox is read-only (opened with EXAMINE)",
}

// errNoSuchMailbox answers a command naming a mailbox that does not exist
// (RFC 3501 section 6.3.1/6.3.4, RFC 9051 NONEXISTENT). go-imap sends an
// *imap.Error as-is and logs nothing; any other error becomes "NO internal
// server error" plus a log line. The text is fixed so the client-supplied name
// is never echoed into a status line.
var errNoSuchMailbox = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeNonExistent,
	Text: "No such mailbox",
}

// errReservedMailboxName answers CREATE or RENAME naming a personal folder that
// would sit on a NAMESPACE prefix. Fixed text, like the others, so the name is
// never echoed.
var errReservedMailboxName = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeCannot,
	Text: "Mailbox name is reserved",
}

// errAlreadyExists answers CREATE or RENAME naming a mailbox that already
// exists (RFC 3501 sections 6.3.3 and 6.3.5, RFC 5530 ALREADYEXISTS). The text
// is fixed so the client-supplied name is never echoed.
var errAlreadyExists = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeAlreadyExists,
	Text: "Mailbox already exists",
}

// errTryCreate answers APPEND, COPY and MOVE to a destination that does not
// exist (RFC 3501 section 6.3.11). Clients react by sending CREATE and
// retrying.
var errTryCreate = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeTryCreate,
	Text: "No such mailbox",
}

// mailboxLookupError maps an error from a mailbox lookup. Only a genuine
// not-found becomes notFound; any other failure (corrupt record, I/O error)
// stays an ordinary wrapped error so it is logged and reported as a server
// error rather than blamed on the client.
//
// Callers pre-check existence with GetMailbox rather than classifying errors
// that come back through ApplyCommand: under real Raft the error chain may not
// survive forwarding to the leader. A pre-check followed by a write is not
// atomic, so a mailbox deleted in between still surfaces as a generic apply
// error; that is an acceptable rare race, since only the status code is at
// stake.
func mailboxLookupError(name string, err error, notFound *imap.Error) error {
	if errors.Is(err, store.ErrNotFound) {
		return notFound
	}
	return fmt.Errorf("looking up mailbox %q: %w", name, err)
}

// requireMailbox returns nil when the named mailbox exists, and otherwise the
// error mailboxLookupError chooses.
func (s *session) requireMailbox(name string, notFound *imap.Error) error {
	_, err := s.resolveMailbox(name, notFound)
	return err
}

// requireNoMailbox returns errAlreadyExists when the named mailbox exists
// (INBOX in any case always does), nil when it is genuinely absent, and a
// wrapped error for any other lookup failure. Sessions are called directly in
// tests without the decoder's INBOX normalisation, hence the EqualFold.
func (s *session) requireNoMailbox(name string) error {
	if strings.EqualFold(name, "INBOX") {
		return errAlreadyExists
	}
	_, err := s.resolve(name)
	switch {
	case err == nil:
		return errAlreadyExists
	case errors.Is(err, store.ErrNotFound):
		return nil
	default:
		return fmt.Errorf("looking up mailbox %q: %w", name, err)
	}
}

// idleRecheckInterval is how often an IDLE session re-validates its account.
// A variable so tests can shorten it.
var idleRecheckInterval = 30 * time.Second

// profileAuthenticator is what LOGIN needs: credentials checked and the
// profile snapshot they were checked against, so the session epoch comes from
// that profile and not from a later re-read.
type profileAuthenticator interface {
	AuthenticateProtocol(username, password string) (string, *bongov1.UserProfile, string, error)
}

var _ profileAuthenticator = (*auth.LocalAuth)(nil)

// accountStateReader is implemented by authenticators that can report the live
// standing of an account (auth.LocalAuth). Authenticators without it get no
// per-command check.
type accountStateReader interface {
	AccountState(userID, appPasswordID string) (auth.AccountState, error)
}

func (s *session) accountReader() accountStateReader {
	r, _ := s.auth.(accountStateReader)
	return r
}

// errAccountRevoked is what a revoked session's command is answered with, just
// after an untagged BYE.
var errAccountRevoked = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Text: "Account no longer available",
}

// errAccountStateUnavailable is what a command is answered with when the
// account state cannot be read. It is temporary: the session stays open and
// the client may retry.
var errAccountStateUnavailable = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeUnavailable,
	Text: "Account state temporarily unavailable, try again later",
}

// checkAccount ends the session when the account was disabled or deleted or
// its password was reset since LOGIN. It must run at the start of every
// authenticated command: go-imap calls Poll only after a command has already
// succeeded, so Poll alone would let the first command after revocation
// return data. The cost is one Pebble Get per command (never per message), plus one more
// for the record when the session was opened with an app password.
//
// The check fails closed. A missing or revoked profile refuses the command and
// ends the session; a store read error refuses the command with a temporary
// NO [UNAVAILABLE] but keeps the session, so the client can retry once the
// store recovers. LOGIN fails closed on any error too. An authenticator that
// cannot report account state is not checked; the only production
// authenticator (auth.LocalAuth) can, so a future remote authenticator must
// implement AccountState rather than rely on that exemption.
func (s *session) checkAccount() error {
	reader := s.accountReader()
	if reader == nil || s.userID == "" {
		return nil
	}
	st, err := reader.AccountState(s.userID, s.appPassword)
	if err != nil {
		s.logger.Warn("imap: account state check failed, refusing command", "user", s.userID, "error", err)
		return errAccountStateUnavailable
	}
	if st.Live(s.epoch) {
		return nil
	}
	s.logger.Info("imap: closing session, account no longer valid", "user", s.userID,
		"missing", st.Missing, "disabled", st.Disabled, "app_password_revoked", st.AppPasswordRevoked)
	if s.conn != nil {
		_ = s.conn.Bye("Account no longer available")
	}
	return errAccountRevoked
}

// Login authenticates the user, throttling brute-force attempts per source IP.
func (s *session) Login(username, password string) error {
	defer observability.IMAPLogin.Since(time.Now())
	ip := ""
	if s.remoteIP != nil {
		ip = s.remoteIP.String()
	}

	if s.authLimiter.Blocked(ip) {
		s.logger.Warn("imap: login throttled after repeated failures", "username", auth.LogUsername(username), "ip", s.remoteIP)
		return errAuthRateLimited
	}

	userID, profile, appPassword, err := s.auth.AuthenticateProtocol(username, password)
	if err != nil {
		s.logger.Warn("imap: login failed", "username", auth.LogUsername(username), "ip", s.remoteIP)
		if s.authLimiter.Fail(ip, username) {
			return errAuthRateLimited
		}
		return imapserver.ErrAuthFailed
	}
	// Fail closed: the epoch must come from the profile the password was
	// verified against, so a reset between verification and a re-read cannot
	// hand the old password a session with the new epoch.
	if profile == nil || profile.Disabled {
		s.logger.Warn("imap: login refused, no usable profile", "username", auth.LogUsername(username))
		return imapserver.ErrAuthFailed
	}
	s.epoch = auth.ProtocolEpoch(profile)
	s.appPassword = appPassword
	s.userID = userID
	// A success forgives failures only when it proves the secret they guessed.
	// An app password proves nothing about the account password, and forgiving
	// here would give its holder unlimited guesses at it.
	if appPassword == "" {
		s.authLimiter.Succeed(ip, username)
	}
	s.logger.Info("imap: login succeeded", "username", auth.LogUsername(username), "user_id", userID, "ip", ip)
	return nil
}

// Select opens a mailbox.
func (s *session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	defer observability.IMAPSelect.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	rm, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
	if err != nil {
		return nil, err
	}
	if err := requireRight(rm, "r"); err != nil {
		return nil, err
	}
	meta := rm.meta

	s.selectedMailbox = mailbox
	s.selectShared(rm)
	s.readOnly = options != nil && options.ReadOnly
	// Without w, s and t nothing in a shared folder can change for this user,
	// so it opens read-only even where the client asked for read-write.
	sharedReadOnly := rm.shared && !rm.has("w") && !rm.has("s") && !rm.has("t")
	if sharedReadOnly {
		s.readOnly = true
	}

	// Load messages for sequence number mapping.
	s.cachedMsgs, err = s.mailboxes.ListMessages(rm.owner, rm.name)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}

	// SELECT is the moment the client is told the whole truth about a mailbox,
	// so it is also where the client's view is (re)established.
	s.setReported(s.cachedMsgs)

	total, recent, unseen, err := s.mailboxes.CountMessages(rm.owner, rm.name)
	if err != nil {
		return nil, fmt.Errorf("counting messages: %w", err)
	}

	flags := []imap.Flag{
		imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged,
		imap.FlagDeleted, imap.FlagDraft,
	}

	_ = unseen

	permanent := append(flags, `\*`)
	if rm.shared {
		// A shared folder offers only the flags the rights allow. Arbitrary
		// keywords need w, because they become the owner's tags.
		permanent = nil
		if !s.readOnly {
			if rm.has("s") {
				permanent = append(permanent, imap.FlagSeen)
			}
			if rm.has("w") {
				permanent = append(permanent, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDraft, `\*`)
			}
			if rm.has("t") {
				permanent = append(permanent, imap.FlagDeleted)
			}
		}
	}

	return &imap.SelectData{
		NumMessages:    total,
		NumRecent:      recent,
		Flags:          flags,
		PermanentFlags: permanent,
		ReadOnly:       sharedReadOnly,
		UIDValidity:    meta.UidValidity,
		UIDNext:        imap.UID(meta.UidNext),
	}, nil
}

// Create creates a new mailbox.
func (s *session) Create(mailbox string, options *imap.CreateOptions) error {
	defer observability.IMAPCreate.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	// Names under the other-users prefix are folders of other accounts; they
	// are created only beneath a folder the user holds k on.
	if s.ns().IsOtherUsersName(mailbox) {
		return s.createShared(mailbox)
	}
	// Before any store read: the check consults configuration only, so it
	// reveals nothing about existing mailboxes.
	if s.namespaces.Reserves(mailbox) {
		return errReservedMailboxName
	}
	if err := s.requireNoMailbox(mailbox); err != nil {
		return err
	}
	if err := s.mailboxes.CreateMailbox(s.userID, mailbox); err != nil {
		s.logger.Warn("imap: create mailbox failed", "mailbox", mailbox, "user", s.userID, "error", err)
		return err
	}
	s.logger.Info("imap: mailbox created", "mailbox", mailbox, "user", s.userID)
	return nil
}

// Namespace reports the personal, other-users and shared namespaces (RFC 2342).
func (s *session) Namespace() (*imap.NamespaceData, error) {
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	return &imap.NamespaceData{
		Personal: []imap.NamespaceDescriptor{{Prefix: "", Delim: '/'}},
		Other:    []imap.NamespaceDescriptor{{Prefix: s.namespaces.OtherUsers + "/", Delim: '/'}},
		Shared:   []imap.NamespaceDescriptor{{Prefix: s.namespaces.Shared + "/", Delim: '/'}},
	}, nil
}

// Delete deletes a mailbox.
func (s *session) Delete(mailbox string) error {
	defer observability.IMAPDelete.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	// RFC 3501 section 6.3.4: deleting a nonexistent mailbox is an error.
	rm, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
	if err != nil {
		return err
	}
	if rm.shared {
		if err := requireRight(rm, "x"); err != nil {
			return err
		}
		if strings.EqualFold(rm.name, "INBOX") {
			return errNoPermission
		}
	}
	if err := s.mailboxes.DeleteMailbox(rm.owner, rm.name); err != nil {
		if errors.Is(err, store.ErrMailboxGrantsNotRevoked) {
			// The mailbox is gone; answering NO would invite a retry that fails as
			// a nonexistent mailbox. The orphan sweep removes the grants.
			s.logger.Warn("imap: mailbox deleted but its grants were not revoked", "mailbox", mailbox, "user", s.userID, "error", err)
			return nil
		}
		s.logger.Warn("imap: delete mailbox failed", "mailbox", mailbox, "user", s.userID, "error", err)
		return err
	}
	s.logger.Info("imap: mailbox deleted", "mailbox", mailbox, "user", s.userID)
	return nil
}

// Rename renames a mailbox.
func (s *session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	defer observability.IMAPRename.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	// A source under the other-users prefix is resolved first, so the answer
	// for a folder the user cannot see does not depend on the new name.
	if s.ns().IsOtherUsersName(mailbox) {
		return s.renameShared(mailbox, newName)
	}
	// Only the new name is checked. A legacy personal folder that already sits
	// under the other-users prefix routes to the shared resolver above, so it
	// cannot be renamed away over IMAP.
	if s.namespaces.Reserves(newName) {
		return errReservedMailboxName
	}
	if err := s.requireMailbox(mailbox, errNoSuchMailbox); err != nil {
		return err
	}
	// The store refuses to overwrite too; this pre-check is what yields the
	// ALREADYEXISTS code, which cannot be recovered from a Raft apply error.
	if err := s.requireNoMailbox(newName); err != nil {
		return err
	}
	if err := s.mailboxes.RenameMailbox(s.userID, mailbox, newName); err != nil {
		s.logger.Warn("imap: rename mailbox failed", "old_name", mailbox, "new_name", newName, "user", s.userID, "error", err)
		return err
	}
	s.logger.Info("imap: mailbox renamed", "old_name", mailbox, "new_name", newName, "user", s.userID)
	return nil
}

// Subscribe adds a mailbox to the user's subscription set.
func (s *session) Subscribe(mailbox string) error {
	defer observability.IMAPSubscribe.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	if s.ns().IsOtherUsersName(mailbox) {
		rm, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
		if err != nil {
			return err
		}
		if err := s.acl.SetSharedSubscription(s.userID, rm.owner, rm.id, true); err != nil {
			s.logger.Warn("imap: subscribe failed", "mailbox", mailbox, "user", s.userID, "error", err)
			return err
		}
		return nil
	}
	if err := s.mailboxes.SetSubscribed(s.userID, mailbox, true); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Client error, not worth a WARN.
			return errNoSuchMailbox
		}
		s.logger.Warn("imap: subscribe failed", "mailbox", mailbox, "user", s.userID, "error", err)
		return err
	}
	s.logger.Info("imap: mailbox subscribed", "mailbox", mailbox, "user", s.userID)
	return nil
}

// Unsubscribe removes a mailbox from the user's subscription set.
func (s *session) Unsubscribe(mailbox string) error {
	defer observability.IMAPUnsubscribe.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	if s.ns().IsOtherUsersName(mailbox) {
		// A name that does not resolve has no subscription left to remove.
		rm, err := s.resolve(mailbox)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("looking up mailbox %q: %w", mailbox, err)
		}
		if err := s.acl.SetSharedSubscription(s.userID, rm.owner, rm.id, false); err != nil {
			s.logger.Warn("imap: unsubscribe failed", "mailbox", mailbox, "user", s.userID, "error", err)
			return err
		}
		return nil
	}
	if err := s.mailboxes.SetSubscribed(s.userID, mailbox, false); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// RFC 3501 section 6.3.7: UNSUBSCRIBE of a name that no longer
			// exists succeeds. There is no subscription left to remove.
			return nil
		}
		s.logger.Warn("imap: unsubscribe failed", "mailbox", mailbox, "user", s.userID, "error", err)
		return err
	}
	s.logger.Info("imap: mailbox unsubscribed", "mailbox", mailbox, "user", s.userID)
	return nil
}

// List returns mailbox listings matching the pattern.
func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	defer observability.IMAPList.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	mailboxes, err := s.mailboxes.ListMailboxes(s.userID)
	if err != nil {
		return fmt.Errorf("listing mailboxes: %w", err)
	}

	for _, mb := range mailboxes {
		// Check if the mailbox name matches any pattern.
		matched := false
		for _, pattern := range patterns {
			if imapserver.MatchList(mb.Name, '/', ref, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// SelectSubscribed is a filter, not a hint: RFC 5258 3.1 has the
		// option list subscribed names "rather than the existing mailboxes",
		// so an unsubscribed mailbox is dropped here rather than reported
		// without \Subscribed. This path is also how LSUB is served — go-imap
		// dispatches LSUB as List with SelectSubscribed and
		// SelectRecursiveMatch set (imapserver/list.go) — so getting it wrong
		// returns every folder to an LSUB, which is what it used to do.
		//
		// SelectRecursiveMatch is ignored: no CHILDINFO is produced and an
		// unsubscribed parent of a subscribed folder is not listed with
		// \Noselect, so LSUB returns exactly the subscribed set.
		subscribed := !mb.Unsubscribed
		if options != nil && options.SelectSubscribed && !subscribed {
			continue
		}

		// Determine hierarchy attribute.
		hasChildren := false
		for _, other := range mailboxes {
			if strings.HasPrefix(other.Name, mb.Name+"/") {
				hasChildren = true
				break
			}
		}
		var attrs []imap.MailboxAttr
		if hasChildren {
			attrs = append(attrs, imap.MailboxAttrHasChildren)
		} else {
			attrs = append(attrs, imap.MailboxAttrHasNoChildren)
		}

		// RFC 5258 3.1: the SUBSCRIBED selection option implies the SUBSCRIBED
		// return option, so \Subscribed is emitted under either one.
		if subscribed && options != nil && (options.ReturnSubscribed || options.SelectSubscribed) {
			attrs = append(attrs, imap.MailboxAttrSubscribed)
		}

		// Add special-use attributes.
		switch mb.Name {
		case "Sent":
			attrs = append(attrs, imap.MailboxAttrSent)
		case "Drafts":
			attrs = append(attrs, imap.MailboxAttrDrafts)
		case "Trash":
			attrs = append(attrs, imap.MailboxAttrTrash)
		case "Junk":
			attrs = append(attrs, imap.MailboxAttrJunk)
		case "Archive":
			attrs = append(attrs, imap.MailboxAttrArchive)
		}

		data := &imap.ListData{
			Mailbox: mb.Name,
			Delim:   '/',
			Attrs:   attrs,
		}

		// Include status data if requested.
		if options != nil && options.ReturnStatus != nil {
			statusData, err := s.statusForMailbox(mb.Name, options.ReturnStatus)
			if err == nil {
				data.Status = statusData
			}
		}

		if err := w.WriteList(data); err != nil {
			return err
		}
	}

	return s.listShared(w.WriteList, func(name string) bool {
		for _, pattern := range patterns {
			if imapserver.MatchList(name, '/', ref, pattern) {
				return true
			}
		}
		return false
	}, options)
}

// Status returns mailbox status data.
func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	defer observability.IMAPStatus.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	return s.statusForMailbox(mailbox, options)
}

func (s *session) statusForMailbox(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	rm, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
	if err != nil {
		return nil, err
	}
	if err := requireRight(rm, "r"); err != nil {
		return nil, err
	}
	return s.statusFor(rm, mailbox, options)
}

// statusFor builds STATUS data for a folder the caller has resolved and holds
// the read right on.
func (s *session) statusFor(rm resolvedMailbox, mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	meta := rm.meta
	if meta == nil {
		var err error
		if meta, err = s.mailboxes.GetMailbox(rm.owner, rm.name); err != nil {
			return nil, mailboxLookupError(mailbox, err, errNoSuchMailbox)
		}
	}

	data := &imap.StatusData{
		Mailbox: mailbox,
	}

	if options.NumMessages || options.NumUnseen || options.NumRecent {
		total, recent, unseen, err := s.mailboxes.CountMessages(rm.owner, rm.name)
		if err != nil {
			return nil, err
		}
		if options.NumMessages {
			data.NumMessages = &total
		}
		if options.NumRecent {
			data.NumRecent = &recent
		}
		if options.NumUnseen {
			data.NumUnseen = &unseen
		}
	}

	if options.UIDValidity {
		data.UIDValidity = meta.UidValidity
	}
	if options.UIDNext {
		data.UIDNext = imap.UID(meta.UidNext)
	}

	if err := s.statusSizes(data, rm.owner, rm.name, options); err != nil {
		return nil, err
	}

	return data, nil
}

// maxAppendBytes caps the size of an IMAP APPEND literal. go-imap enforces this
// via the SessionAppendLimit interface BEFORE Append is called, rejecting
// oversized literals with a TOOBIG response and draining them — without it the
// default is 100 MiB and the handler below would stream the whole literal to a
// blob and then read it entirely back into memory, so an authenticated user
// could OOM the shared server or fill the data volume with one command.
const maxAppendBytes uint32 = 50 * 1024 * 1024 // 50 MiB

// AppendLimit implements imapserver.SessionAppendLimit, advertising and
// enforcing the maximum APPEND payload size.
func (s *session) AppendLimit() uint32 {
	return maxAppendBytes
}

// Append stores a message in the specified mailbox.
func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	defer observability.IMAPAppend.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	// RFC 3501 section 6.3.11: the server MUST NOT create the mailbox
	// implicitly. Checked before any blob work; go-imap drains the literal.
	dest, err := s.resolveMailbox(mailbox, errTryCreate)
	if err != nil {
		return nil, err
	}
	// A shared folder is written into the owner's account: the owner's quota
	// is checked, and the message is delivered and indexed under the owner.
	if err := requireRight(dest, "i"); err != nil {
		return nil, err
	}

	// Free disk space, checked before the quota and before any blob is written.
	if s.storage != nil && s.storage.Low() {
		s.logger.Debug("imap: append refused: storage low", "mailbox", mailbox, "user", s.userID)
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeUnavailable,
			Text: "Insufficient system storage",
		}
	}

	// Storage quota, checked before any blob or metadata is written so a
	// refused APPEND leaves nothing behind. Same checker as SMTP RCPT. The literal
	// size is weighed against the headroom: it is the size PutReader returns and
	// stores as MailMeta.RawMessage.Size, which is what the usage recompute counts.
	// Usage is recomputed every few minutes, so concurrent writers can still
	// overshoot. A checker error fails open, as SMTP does. Nothing here blocks EXPUNGE, COPY or MOVE,
	// so a user over quota can still free space.
	if s.quota != nil {
		over, qErr := s.quota.RecipientWouldExceedQuota(dest.owner, r.Size())
		if qErr != nil {
			s.logger.Warn("imap: quota check error (fail open)", "mailbox", mailbox, "user", s.userID, "error", qErr)
		} else if over {
			s.logger.Warn("imap: append refused: over storage quota", "mailbox", mailbox, "user", s.userID)
			return nil, &imap.Error{
				Type: imap.StatusResponseTypeNo,
				Code: imap.ResponseCodeOverQuota,
				Text: "Storage quota exceeded",
			}
		}
	}

	// Store raw message as a blob.
	hash, size, err := s.blobs.PutReader(r)
	if err != nil {
		s.logger.Warn("imap: append blob store failed", "mailbox", mailbox, "user", s.userID, "error", err)
		return nil, fmt.Errorf("storing message blob: %w", err)
	}

	// Re-open blob and parse headers.
	rc, err := s.blobs.Get(hash)
	if err != nil {
		return nil, fmt.Errorf("reading blob for parsing: %w", err)
	}
	rawMsg, err := io.ReadAll(rc)
	if closeErr := rc.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("reading raw message: %w", err)
	}

	// Parse envelope for metadata.
	envelope, _, _ := mimesafe.ReadEnvelope(rawMsg)

	subject := ""
	messageID := ""
	headerFrom := ""
	var toAddrs, ccAddrs []string
	var listID string
	if envelope != nil {
		subject = envelope.GetHeader("Subject")
		messageID = envelope.GetHeader("Message-Id")
		headerFrom = envelope.GetHeader("From")
		// The same extraction delivery uses, so imported list mail reaches
		// the Mailing lists and To me views.
		toAddrs, ccAddrs, listID = store.MailAddressHeaders(envelope)
	}

	// Determine internal date: from APPEND options, Date header, or now.
	var internalDate time.Time
	if options != nil && !options.Time.IsZero() {
		internalDate = options.Time
	} else if envelope != nil {
		if dateStr := envelope.GetHeader("Date"); dateStr != "" {
			if t, parseErr := time.Parse(time.RFC1123Z, dateStr); parseErr == nil {
				internalDate = t
			} else if t, parseErr := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", dateStr); parseErr == nil {
				internalDate = t
			}
		}
	}
	if internalDate.IsZero() {
		internalDate = time.Now()
	}

	// Map IMAP flags to store bitfield.
	var flags uint32
	if options != nil {
		flags = imapFlagsToUint32(options.Flags)
		if dest.shared {
			// RFC 4314: the server ignores flags the user may not set. Keywords
			// are never stored here (imapFlagsToUint32 drops them), so only the
			// system flags need masking, each by its own right.
			flags = maskSharedAppendFlags(flags, dest)
		}
	}

	// Generate GUID and store MailMeta.
	guid := fmt.Sprintf("%d-%s", time.Now().UnixNano(), hash[:8])

	// Extract attachment blobs so the web UI can serve them individually.
	var attachmentRefs []*bongov1.BlobRef
	if envelope != nil {
		for _, att := range envelope.Attachments {
			if len(att.Content) == 0 {
				continue
			}
			attHash, attErr := s.blobs.Put(att.Content)
			if attErr != nil {
				s.logger.Warn("imap: append attachment blob failed",
					"mailbox", mailbox, "filename", att.FileName, "error", attErr)
				continue
			}
			attachmentRefs = append(attachmentRefs, &bongov1.BlobRef{
				Hash:        attHash,
				Size:        int64(len(att.Content)),
				ContentType: att.ContentType,
				Filename:    att.FileName,
			})
		}
	}

	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  messageID,
		Subject:    subject,
		From:       headerFrom,
		To:         toAddrs,
		Cc:         ccAddrs,
		ListId:     listID,
		ReceivedAt: internalDate.Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash:        hash,
			Size:        size,
			ContentType: "message/rfc822",
		},
		Attachments: attachmentRefs,
	}
	uid, err := s.mailboxes.DeliverMessage(dest.owner, dest.name, meta, flags)
	if err != nil {
		s.logger.Warn("imap: append failed", "mailbox", mailbox, "user", s.userID, "guid", guid, "error", err)
		return nil, fmt.Errorf("delivering to mailbox: %w", err)
	}

	s.logger.Info("imap: mail stored", "mailbox", mailbox, "user", s.userID, "guid", guid, "size", size)

	// Index for search.
	if s.search != nil {
		body := ""
		if envelope != nil {
			body = envelope.Text
		}
		if err := s.search.IndexMail(dest.owner, guid, subject, headerFrom, toAddrs, body, internalDate.Unix(), false, nil); err != nil {
			s.logger.Warn("imap: search index failed on append", "guid", guid, "error", err)
		}
	}

	// IDLE watchers are told by the apply of the delivery itself, which runs on
	// every node. Announcing it here as well sent an idling client two EXISTS
	// for one APPEND -- it reads the second as further new mail -- and told
	// nobody on any other node.

	// Get mailbox UIDValidity.
	mbMeta, _ := s.mailboxes.GetMailbox(dest.owner, dest.name)
	validity := uint32(0)
	if mbMeta != nil {
		validity = mbMeta.UidValidity
	}

	return &imap.AppendData{
		UID:         imap.UID(uid),
		UIDValidity: validity,
	}, nil
}

// updateWriter is the part of *imapserver.UpdateWriter that pollUpdates needs.
// Declared at the consumer both because that is the house style and because
// imapserver.UpdateWriter has only unexported fields, so a test cannot build
// one; an interface is the only way to observe what Poll emits without
// standing up a whole server and client.
type updateWriter interface {
	WriteNumMessages(n uint32) error
	WriteExpunge(seqNum uint32) error
}

// Poll reports mailbox changes the client has not been told about yet.
//
// go-imap calls this after every command once the connection is authenticated
// or selected (imapserver/conn.go), which makes it the delivery path for
// untagged EXISTS and EXPUNGE to a client that polls with NOOP. It used to
// return nil on the assumption that IDLE was the only channel that mattered.
// It is not: Thunderbird IDLEs on INBOX and NOOP-polls every other folder, so a
// message appended to Sent was stored correctly and then stayed invisible until
// the client restarted and issued a fresh SELECT.
func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	defer observability.IMAPPoll.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		// go-imap treats a Poll error as fatal to the connection, and Poll runs
		// after the command has already been answered (NOOP has no handler of
		// its own). A temporary store error therefore skips the mailbox diff,
		// so no mailbox data is sent, and leaves the session usable; the next
		// command's own check refuses it. Revocation still ends the session.
		if errors.Is(err, errAccountStateUnavailable) {
			return nil
		}
		return err
	}
	// Losing a shared folder (or failing to re-read it) is reported by the
	// next command, not here: a Poll error would end the connection. Nothing
	// of the folder is sent.
	if err := s.checkSelected(); err != nil {
		return nil
	}
	return s.pollUpdates(w, allowExpunge)
}

// pollUpdates diffs the mailbox against s.cachedMsgs -- the sequence-number map
// the client was last shown -- and writes the difference.
func (s *session) pollUpdates(w updateWriter, allowExpunge bool) error {
	if s.selectedMailbox == "" {
		return nil
	}

	owner, name := s.sel()
	msgs, err := s.mailboxes.ListMessages(owner, name)
	if err != nil {
		return fmt.Errorf("polling mailbox %q: %w", s.selectedMailbox, err)
	}

	present := make(map[uint32]struct{}, len(msgs))
	for _, msg := range msgs {
		present[msg.Uid] = struct{}{}
	}

	// Sequence numbers are positions in the list the client currently holds, so
	// they are derived from the client's view rather than from the fresh list.
	var expungedSeqNums []uint32
	for i, uid := range s.reportedUIDs {
		if _, ok := present[uid]; !ok {
			expungedSeqNums = append(expungedSeqNums, uint32(i+1))
		}
	}

	if len(expungedSeqNums) > 0 && !allowExpunge {
		// FETCH, STORE and SEARCH forbid EXPUNGE mid-command (RFC 3501 7.4.1):
		// the client is still resolving the message set it named, and
		// renumbering underneath it would silently retarget the command.
		// WriteExpunge would reject the call anyway. Report nothing at all --
		// an EXISTS describing a mailbox whose removals the client has not been
		// told about would leave it with a count it cannot reconcile -- and
		// leave the client's view untouched so the next expunge-allowing Poll
		// still reports these removals instead of losing them.
		return nil
	}

	// Highest sequence number first. Each EXPUNGE the client applies shifts
	// every higher sequence number down by one, so descending order lets the
	// client apply them verbatim; ascending order would leave every later
	// number in this batch pointing one message too high.
	for i := len(expungedSeqNums) - 1; i >= 0; i-- {
		if err := w.WriteExpunge(expungedSeqNums[i]); err != nil {
			return fmt.Errorf("writing expunge for mailbox %q: %w", s.selectedMailbox, err)
		}
	}

	// After applying the expunges above, this is the count the client believes.
	// Anything else means messages arrived, which is what EXISTS announces.
	clientCount := len(s.reportedUIDs) - len(expungedSeqNums)
	if len(msgs) != clientCount {
		if err := w.WriteNumMessages(clampUint32(len(msgs))); err != nil {
			return fmt.Errorf("writing exists for mailbox %q: %w", s.selectedMailbox, err)
		}
	}

	// The client is now in step with msgs. Without this the next Poll would
	// re-diff against the stale view and repeat every report.
	s.setReported(msgs)

	// Refreshing the server-side cache here is incidental: the fresh list is
	// already in hand, and it keeps EXPUNGE's sequence-number lookup current
	// across a run of NOOPs.
	s.cachedMsgs = msgs
	return nil
}

// Idle waits for updates and notifies the client.
func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if err := s.checkAccount(); err != nil {
		return err
	}
	// A long IDLE sends no commands, so revocation is noticed on a timer.
	if err := s.checkSelected(); err != nil {
		return err
	}
	recheck := time.NewTicker(idleRecheckInterval)
	defer recheck.Stop()

	if s.selectedMailbox == "" || s.notifier == nil {
		for {
			select {
			case <-stop:
				return nil
			case <-recheck.C:
				if err := s.checkAccount(); err != nil {
					return err
				}
				if err := s.checkSelected(); err != nil {
					return err
				}
			}
		}
	}

	owner, name := s.sel()
	ch := s.notifier.Subscribe(owner, name)
	defer s.notifier.Unsubscribe(ch)

	for {
		select {
		case <-stop:
			return nil
		case <-recheck.C:
			if err := s.checkAccount(); err != nil {
				return err
			}
			if err := s.checkSelected(); err != nil {
				return err
			}
		case notif, ok := <-ch:
			if !ok {
				return nil
			}
			switch notif.Type {
			case NotifNewMail:
				// Refresh cached messages to get accurate count.
				owner, name := s.sel()
				msgs, err := s.mailboxes.ListMessages(owner, name)
				if err == nil {
					s.cachedMsgs = msgs
					// The EXISTS below tells the client about these, so the
					// next Poll must not announce them a second time.
					s.setReported(msgs)
				}
				if err := w.WriteNumMessages(notif.ExistsCount); err != nil {
					return err
				}
			case NotifExpunge:
				// Translate to THIS client's numbering. The notification comes
				// from the store, which has no idea what any one client has
				// been shown, so the UID is the only thing it can say that is
				// true for every recipient.
				//
				// A zero means the client was never told about this message --
				// or was already told to expunge it, because dropReported below
				// removes it from the baseline. Skipping is right in both
				// cases, and makes a repeated notification a no-op rather than
				// an EXPUNGE that renumbers the client twice for one removal.
				seqNum := s.reportedSeqNum(notif.UID)
				if seqNum == 0 {
					continue
				}
				if err := w.WriteExpunge(seqNum); err != nil {
					return err
				}
				// The client's view has changed, so the next Poll must not
				// diff against the old one and report the removal again.
				s.dropReported(notif.UID)
			case NotifFlagChange:
				// Same translation, same reason. A client that has not been
				// sent this message cannot be sent a FETCH for it either; it
				// will learn the flags with the message when the next EXISTS
				// prompts it to fetch.
				seqNum := s.reportedSeqNum(notif.UID)
				if seqNum == 0 {
					continue
				}
				// Fetch the message to include tag-derived keywords in the flags response.
				owner, name := s.sel()
				updated, _ := s.mailboxes.GetMessage(owner, name, notif.UID)
				var imapFlags []imap.Flag
				if updated != nil {
					imapFlags = messageToIMAPFlags(updated.Flags, updated.Tags)
				} else {
					imapFlags = flagsToIMAP(notif.Flags)
				}
				if err := w.WriteMessageFlags(seqNum, imap.UID(notif.UID), imapFlags); err != nil {
					return err
				}
			}
		}
	}
}

// Unselect closes the selected mailbox.
func (s *session) Unselect() error {
	defer observability.IMAPUnselect.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	s.selectedMailbox = ""
	s.selShared, s.selOwner, s.selName, s.selID, s.selAddr, s.selRights = false, "", "", "", "", ""
	s.cachedMsgs = nil
	s.reportedUIDs = nil
	return nil
}

// Expunge removes messages marked as deleted.
func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	defer observability.IMAPExpunge.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	// go-imap's CLOSE calls Expunge with a zero ExpungeWriter (and never when
	// the folder is read-only). CLOSE must always succeed, so a shared folder
	// whose grant was revoked, downgraded or lost its e right expunges nothing
	// and answers OK; EXPUNGE and UID EXPUNGE are refused in those cases.
	isClose := w != nil && *w == (imapserver.ExpungeWriter{})
	if err := s.checkSelected(); err != nil {
		if isClose && s.selShared {
			return nil
		}
		return err
	}
	if s.readOnly {
		return errMailboxReadOnly
	}
	if !s.selHas("e") {
		if isClose {
			return nil
		}
		return errNoPermission
	}
	owner, name := s.sel()
	expunged, err := s.mailboxes.Expunge(owner, name)
	if err != nil {
		return err
	}

	if len(expunged) > 0 {
		s.logger.Info("imap: expunged", "mailbox", s.selectedMailbox, "user", s.userID, "count", len(expunged))
	}

	// Write expunge notifications (sequence numbers, from highest to lowest).
	for i := len(expunged) - 1; i >= 0; i-- {
		uid := expunged[i]
		seqNum := s.reportedSeqNum(uid)
		if seqNum > 0 {
			if err := w.WriteExpunge(seqNum); err != nil {
				return err
			}
		}
		s.removeFromCache(uid)
		// The client has been sent the EXPUNGE, so its view has changed too.
		// Without this a following Poll re-sends the same sequence number and
		// the client drops a message it should have kept.
		s.dropReported(uid)
	}

	return nil
}

// Search searches for messages matching the criteria.
func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	defer observability.IMAPSearch.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	if s.selectedMailbox == "" {
		return nil, fmt.Errorf("no mailbox selected")
	}

	if err := s.checkSelected(); err != nil {
		return nil, err
	}

	// Refresh cached messages.
	owner, name := s.sel()
	msgs, err := s.mailboxes.ListMessages(owner, name)
	if err != nil {
		return nil, err
	}
	s.cachedMsgs = msgs

	// Use Bleve for text/header/body criteria if available.
	var bleveGUIDs map[string]struct{}
	usedBleve := false
	if s.search != nil && hasTextCriteria(criteria) {
		bleveGUIDs, err = searchBleve(s.search, owner, criteria)
		if err != nil {
			s.logger.Warn("imap: bleve search failed, falling back to in-memory", "error", err)
		} else {
			usedBleve = true
		}
	}

	var matchedUIDs []imap.UID
	for _, msg := range msgs {
		// If Bleve was used, filter by Bleve results first.
		if usedBleve {
			if _, ok := bleveGUIDs[msg.MailGuid]; !ok {
				continue
			}
		} else if hasTextCriteria(criteria) {
			// Fallback: use in-memory matching for text criteria.
			if !s.matchesCriteria(msg, criteria) {
				continue
			}
		}

		// Always apply flag criteria as post-filter.
		if !matchesFlagCriteria(msg.Flags, msg.InternalDate, criteria) {
			continue
		}

		matchedUIDs = append(matchedUIDs, imap.UID(msg.Uid))
	}

	if kind == imapserver.NumKindSeq {
		// Convert UIDs to sequence numbers.
		var seqNums imap.SeqSet
		for _, uid := range matchedUIDs {
			seqNum := s.reportedSeqNum(uint32(uid))
			if seqNum > 0 {
				seqNums.AddNum(seqNum)
			}
		}
		return &imap.SearchData{All: seqNums}, nil
	}

	var uidSet imap.UIDSet
	for _, uid := range matchedUIDs {
		uidSet.AddNum(uid)
	}
	return &imap.SearchData{All: uidSet}, nil
}

// Fetch retrieves message data.
func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	defer observability.IMAPFetch.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	if s.selectedMailbox == "" {
		return fmt.Errorf("no mailbox selected")
	}

	if err := s.checkSelected(); err != nil {
		return err
	}

	// Refresh cached messages.
	owner, name := s.sel()
	msgs, err := s.mailboxes.ListMessages(owner, name)
	if err != nil {
		return err
	}
	s.cachedMsgs = msgs

	// Resolve which messages to fetch.
	for seqNum, msg := range msgs {
		seqNum := uint32(seqNum + 1) // 1-based
		uid := imap.UID(msg.Uid)

		if !numSetContains(numSet, seqNum, uid) {
			continue
		}

		if err := s.fetchMessage(w, seqNum, msg, options); err != nil {
			return err
		}
	}

	return nil
}

func (s *session) fetchMessage(w *imapserver.FetchWriter, seqNum uint32, msg *bongov1.MailboxMessage, options *imap.FetchOptions) error {
	respW := w.CreateMessage(seqNum)

	if options.UID {
		respW.WriteUID(imap.UID(msg.Uid))
	}

	if options.Flags {
		respW.WriteFlags(messageToIMAPFlags(msg.Flags, msg.Tags))
	}

	if options.InternalDate {
		respW.WriteInternalDate(time.Unix(msg.InternalDate, 0))
	}

	// Load mail metadata and raw message if needed.
	var mailMeta *bongov1.MailMeta
	var rawMsg []byte

	needContent := options.Envelope || options.RFC822Size ||
		options.BodyStructure != nil || len(options.BodySection) > 0

	if needContent {
		var err error
		owner, _ := s.sel()
		mailMeta, rawMsg, err = s.loadMailContent(owner, msg.MailGuid)
		if err != nil {
			s.logger.Warn("imap: failed to load mail content", "guid", msg.MailGuid, "error", err)
			return respW.Close()
		}
	}

	if options.RFC822Size && rawMsg != nil {
		respW.WriteRFC822Size(int64(len(rawMsg)))
	}

	if options.Envelope && rawMsg != nil {
		envelope := s.buildEnvelope(mailMeta, rawMsg)
		respW.WriteEnvelope(envelope)
	}

	if options.BodyStructure != nil && rawMsg != nil {
		bodyStructure := s.buildBodyStructure(rawMsg)
		if bodyStructure != nil {
			// A client requesting the extended BODYSTRUCTURE (as opposed to the
			// non-extended BODY) requires every part to carry a non-nil Extended
			// field; go-imap panics otherwise. Ensure it before writing.
			ensureExtendedBodyStructure(bodyStructure)
			respW.WriteBodyStructure(bodyStructure)
		}
	}

	// Handle BODY[] sections.
	for _, section := range options.BodySection {
		if rawMsg == nil {
			continue
		}
		if err := s.writeBodySection(respW, section, rawMsg); err != nil {
			return err
		}

		// Set \Seen flag if not PEEK and not read-only. On a shared folder it
		// is the owner's flag, and is set only for a user holding the s right.
		if !section.Peek && !s.readOnly && s.selHas("s") {
			owner, name := s.sel()
			if err := s.mailboxes.AddFlags(owner, name, msg.Uid, store.FlagSeen); err != nil {
				s.logger.Warn("imap: failed to set seen flag", "uid", msg.Uid, "error", err)
			}
		}
	}

	return respW.Close()
}

func (s *session) loadMailContent(owner, guid string) (*bongov1.MailMeta, []byte, error) {
	// Read mail metadata from Pebble, from the namespace that owns the message.
	key := fmt.Sprintf("/user/%s/mail/%s", owner, guid)
	data, err := s.pebble.Get([]byte(key))
	if err != nil {
		return nil, nil, fmt.Errorf("mail meta not found: %w", err)
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil, nil, fmt.Errorf("parsing mail meta: %w", err)
	}

	// Read raw message from blob store.
	if meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		return &meta, nil, nil
	}

	// Reconstruct the full message (splits are rebuilt byte-exact; legacy
	// whole-raw records pass through unchanged).
	rawMsg, err := store.RawMessageBytes(s.blobs, &meta)
	if err != nil {
		return &meta, nil, fmt.Errorf("reading raw message: %w", err)
	}

	return &meta, rawMsg, nil
}

func (s *session) buildEnvelope(meta *bongov1.MailMeta, rawMsg []byte) *imap.Envelope {
	env, _, err := mimesafe.ReadEnvelope(rawMsg)
	if err != nil {
		// Fall back to proto metadata.
		return &imap.Envelope{
			Subject:   meta.Subject,
			From:      []imap.Address{{Mailbox: meta.From}},
			MessageID: meta.MessageId,
			Date:      time.Unix(meta.ReceivedAt, 0),
		}
	}

	envelope := &imap.Envelope{
		Subject:   env.GetHeader("Subject"),
		MessageID: env.GetHeader("Message-Id"),
	}

	// Parse date.
	if dateStr := env.GetHeader("Date"); dateStr != "" {
		if t, err := time.Parse(time.RFC1123Z, dateStr); err == nil {
			envelope.Date = t
		} else if t, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", dateStr); err == nil {
			envelope.Date = t
		}
	}
	if envelope.Date.IsZero() {
		envelope.Date = time.Unix(meta.ReceivedAt, 0)
	}

	envelope.From = parseAddresses(store.EnvelopeAddresses(env, "From"))
	envelope.Sender = parseAddresses(store.EnvelopeAddresses(env, "Sender"))
	envelope.ReplyTo = parseAddresses(store.EnvelopeAddresses(env, "Reply-To"))
	envelope.To = parseAddresses(store.EnvelopeAddresses(env, "To"))
	envelope.Cc = parseAddresses(store.EnvelopeAddresses(env, "Cc"))
	envelope.Bcc = parseAddresses(store.EnvelopeAddresses(env, "Bcc"))
	if inReplyTo := env.GetHeader("In-Reply-To"); inReplyTo != "" {
		envelope.InReplyTo = []string{inReplyTo}
	}

	return envelope
}

func parseAddressList(header string) []imap.Address {
	if header == "" {
		return nil
	}
	return parseAddresses(store.SplitAddressHeader(header))
}

func parseAddresses(parts []string) []imap.Address {
	var addrs []imap.Address
	for _, part := range parts {
		addrs = append(addrs, parseAddress(part))
	}
	return addrs
}

func parseAddress(s string) imap.Address {
	s = strings.TrimSpace(s)

	// RFC 5322 first: it unquotes names, handles backslash escapes and a "<"
	// inside a quoted name, and splits at the last "@".
	if a, err := mail.ParseAddress(s); err == nil {
		addr := imap.Address{Name: a.Name, Mailbox: a.Address}
		if i := strings.LastIndex(a.Address, "@"); i >= 0 {
			addr.Mailbox, addr.Host = a.Address[:i], a.Address[i+1:]
		}
		return addr
	}

	// Handle "Display Name <user@host>" format.
	if idx := strings.LastIndex(s, "<"); idx >= 0 {
		name := strings.TrimSpace(s[:idx])
		if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
			name = unescapeQuoted(name[1 : len(name)-1])
		}
		email := strings.TrimSuffix(strings.TrimPrefix(s[idx:], "<"), ">")
		parts := strings.SplitN(email, "@", 2)
		addr := imap.Address{Name: name, Mailbox: parts[0]}
		if len(parts) == 2 {
			addr.Host = parts[1]
		}
		return addr
	}

	// Plain email address.
	parts := strings.SplitN(s, "@", 2)
	addr := imap.Address{Mailbox: parts[0]}
	if len(parts) == 2 {
		addr.Host = parts[1]
	}
	return addr
}

// unescapeQuoted resolves the backslash escapes of a quoted-string body.
func unescapeQuoted(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (s *session) buildBodyStructure(rawMsg []byte) imap.BodyStructure {
	env, _, err := mimesafe.ReadEnvelope(rawMsg)
	if err != nil {
		return &imap.BodyStructureSinglePart{
			Type:    "text",
			Subtype: "plain",
			Size:    clampUint32(len(rawMsg)),
		}
	}

	contentType := env.GetHeader("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	parts := strings.SplitN(contentType, ";", 2)
	mediaType := strings.TrimSpace(parts[0])
	typeParts := strings.SplitN(mediaType, "/", 2)

	mainType := "text"
	subType := "plain"
	if len(typeParts) == 2 {
		mainType = strings.ToLower(typeParts[0])
		subType = strings.ToLower(typeParts[1])
	}

	if mainType == "multipart" {
		var children []imap.BodyStructure
		if env.Text != "" {
			children = append(children, &imap.BodyStructureSinglePart{
				Type:    "text",
				Subtype: "plain",
				Size:    clampUint32(len(env.Text)),
			})
		}
		if env.HTML != "" {
			children = append(children, &imap.BodyStructureSinglePart{
				Type:    "text",
				Subtype: "html",
				Size:    clampUint32(len(env.HTML)),
			})
		}
		for _, att := range env.Attachments {
			attParts := strings.SplitN(att.ContentType, "/", 2)
			attType := "application"
			attSub := "octet-stream"
			if len(attParts) == 2 {
				attType = attParts[0]
				attSub = attParts[1]
			}
			children = append(children, &imap.BodyStructureSinglePart{
				Type:    attType,
				Subtype: attSub,
				Size:    clampUint32(len(att.Content)),
			})
		}
		if len(children) == 0 {
			children = append(children, &imap.BodyStructureSinglePart{
				Type:    "text",
				Subtype: "plain",
			})
		}
		return &imap.BodyStructureMultiPart{
			Children: children,
			Subtype:  subType,
		}
	}

	return &imap.BodyStructureSinglePart{
		Type:    mainType,
		Subtype: subType,
		Size:    clampUint32(len(rawMsg)),
	}
}

// ensureExtendedBodyStructure walks a body structure and gives every part a
// non-nil Extended field. go-imap panics when a client requests the extended
// BODYSTRUCTURE but any part lacks it; the extension fields are all optional,
// so an empty value is a valid (NIL-filled) extended response.
func ensureExtendedBodyStructure(bs imap.BodyStructure) {
	switch v := bs.(type) {
	case *imap.BodyStructureSinglePart:
		if v.Extended == nil {
			v.Extended = &imap.BodyStructureSinglePartExt{}
		}
	case *imap.BodyStructureMultiPart:
		if v.Extended == nil {
			v.Extended = &imap.BodyStructureMultiPartExt{}
		}
		for _, child := range v.Children {
			ensureExtendedBodyStructure(child)
		}
	}
}

func clampUint32(n int) uint32 {
	if n <= 0 {
		return 0
	}
	if uint64(n) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

func (s *session) writeBodySection(respW *imapserver.FetchResponseWriter, section *imap.FetchItemBodySection, rawMsg []byte) error {
	var content []byte

	switch section.Specifier {
	case imap.PartSpecifierHeader:
		// Return everything up to and including the blank line.
		if idx := bytes.Index(rawMsg, []byte("\r\n\r\n")); idx >= 0 {
			content = rawMsg[:idx+4]
		} else {
			content = rawMsg
		}
	case imap.PartSpecifierText:
		// Return everything after the header.
		if idx := bytes.Index(rawMsg, []byte("\r\n\r\n")); idx >= 0 {
			content = rawMsg[idx+4:]
		}
	default:
		// Full message or MIME part.
		if len(section.Part) == 0 {
			content = rawMsg
		} else {
			// For specific MIME parts, use enmime to extract.
			content = s.extractMIMEPart(rawMsg, section.Part)
		}
	}

	if content == nil {
		content = []byte{}
	}

	// Apply partial if requested.
	if section.Partial != nil {
		offset := int(section.Partial.Offset)
		size := int(section.Partial.Size)
		if offset >= len(content) {
			content = []byte{}
		} else {
			end := offset + size
			if end > len(content) {
				end = len(content)
			}
			content = content[offset:end]
		}
	}

	wc := respW.WriteBodySection(section, int64(len(content)))
	if _, err := wc.Write(content); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return nil
}

func (s *session) extractMIMEPart(rawMsg []byte, partPath []int) []byte {
	env, _, err := mimesafe.ReadEnvelope(rawMsg)
	if err != nil {
		return nil
	}

	// For part [1], return the text body.
	// For part [2], return the first attachment.
	if len(partPath) == 1 {
		switch partPath[0] {
		case 1:
			return []byte(env.Text)
		case 2:
			return []byte(env.HTML)
		default:
			attIdx := partPath[0] - 3
			if attIdx >= 0 && attIdx < len(env.Attachments) {
				return env.Attachments[attIdx].Content
			}
		}
	}

	return nil
}

// Store updates message flags.
func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	defer observability.IMAPStore.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	if s.selectedMailbox == "" {
		return fmt.Errorf("no mailbox selected")
	}
	if err := s.checkSelected(); err != nil {
		return err
	}
	if s.readOnly {
		return errMailboxReadOnly
	}

	// Separate standard system flags from custom $keyword flags.
	stdFlags, keywords := separateFlags(flags.Flags)
	flagBits := imapFlagsToUint32(stdFlags)
	// Refresh cache.
	owner, name := s.sel()
	msgs, err := s.mailboxes.ListMessages(owner, name)
	if err != nil {
		return err
	}
	s.cachedMsgs = msgs

	// Check everything before the first write, so a refusal changes nothing.
	// On a shared folder the messages are the owner's, and so are their
	// keywords (tags): \Seen needs s, \Deleted needs t, every other flag and
	// any keyword change needs w, and new keywords answer to the owner's tag
	// limits.
	if s.selShared {
		if err := s.checkSharedStore(owner, msgs, numSet, flags.Op, flagBits, keywords); err != nil {
			return err
		}
	}

	var updatedUIDs []uint32
	for seqNum, msg := range msgs {
		seqNum := uint32(seqNum + 1)
		uid := imap.UID(msg.Uid)

		if !numSetContains(numSet, seqNum, uid) {
			continue
		}

		// Apply standard flag changes.
		if flagBits != 0 || flags.Op == imap.StoreFlagsSet {
			switch flags.Op {
			case imap.StoreFlagsSet:
				err = s.mailboxes.SetFlags(owner, name, msg.Uid, flagBits)
			case imap.StoreFlagsAdd:
				err = s.mailboxes.AddFlags(owner, name, msg.Uid, flagBits)
			case imap.StoreFlagsDel:
				err = s.mailboxes.RemoveFlags(owner, name, msg.Uid, flagBits)
			}
			if err != nil {
				return err
			}
		}

		// Apply keyword (tag) changes. On a shared folder they are the
		// owner's tags: a failure fails the command, and a replace that leaves
		// a message's keywords as they are writes nothing, so a user without w
		// can still change flags with FLAGS.
		if s.selShared {
			if keywordChanges(flags.Op, keywords, msg) {
				if err := s.applyKeywords(owner, name, msg.MailGuid, msg.Uid, flags.Op, keywords); err != nil {
					s.logger.Warn("imap: shared keyword update failed", "guid", msg.MailGuid, "user", s.userID, "error", err)
					return err
				}
			}
		} else if len(keywords) > 0 || flags.Op == imap.StoreFlagsSet {
			if err := s.applyKeywords(owner, name, msg.MailGuid, msg.Uid, flags.Op, keywords); err != nil {
				s.logger.Warn("imap: keyword update failed", "guid", msg.MailGuid, "error", err)
				// Non-fatal: continue with other messages and standard flags.
			}
		}

		updatedUIDs = append(updatedUIDs, msg.Uid)

		// Send updated flags back (unless SILENT).
		if !flags.Silent {
			updated, _ := s.mailboxes.GetMessage(owner, name, msg.Uid)
			if updated != nil {
				respW := w.CreateMessage(seqNum)
				respW.WriteUID(uid)
				respW.WriteFlags(messageToIMAPFlags(updated.Flags, updated.Tags))
				if err := respW.Close(); err != nil {
					return err
				}
			}
		}
	}

	if len(updatedUIDs) > 0 {
		s.logger.Info("imap: flags updated", "mailbox", s.selectedMailbox, "user", s.userID, "op", flags.Op, "count", len(updatedUIDs))
	}

	return nil
}

// applyKeywords applies keyword (tag) changes to MailMeta and syncs the
// denormalised MailboxMessage.Tags field plus search indexes.
//
// Tags belong to the account that owns the message, so owner and mailbox name
// that account's namespace: the session user for a personal folder, the folder's
// owner for a shared one. Nothing below may use s.userID or s.selectedMailbox,
// or a grantee's keyword would land in the grantee's own tags. Two sessions
// changing one message's tags at once is last writer wins on the MailMeta
// read-modify-write.
func (s *session) applyKeywords(owner, mailbox, mailGUID string, uid uint32, op imap.StoreFlagsOp, keywords []string) error {
	// Load MailMeta from Pebble.
	key := fmt.Sprintf("/user/%s/mail/%s", owner, mailGUID)
	data, err := s.pebble.Get([]byte(key))
	if err != nil {
		return fmt.Errorf("reading mail meta: %w", err)
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parsing mail meta: %w", err)
	}

	switch op {
	case imap.StoreFlagsAdd:
		// Auto-create tag definitions for any new keywords (best-effort).
		if s.tags != nil {
			for _, kw := range keywords {
				if _, err := s.tags.GetTag(owner, kw); err != nil {
					// Tag does not exist yet — create it.
					if _, createErr := s.tags.CreateTag(owner, kw); createErr != nil {
						if s.selShared {
							return fmt.Errorf("creating owner tag: %w", createErr)
						}
						s.logger.Warn("imap: auto-create tag failed", "tag", kw, "error", createErr)
					}
				}
			}
		}
		if err := store.AddTags(&meta, keywords); err != nil {
			return fmt.Errorf("adding tags: %w", err)
		}

	case imap.StoreFlagsDel:
		if err := store.RemoveTags(&meta, keywords); err != nil {
			return fmt.Errorf("removing tags: %w", err)
		}

	case imap.StoreFlagsSet:
		// Replace all tags with the new keyword set.
		// Auto-create definitions for new keywords first (best-effort).
		if s.tags != nil {
			for _, kw := range keywords {
				if _, err := s.tags.GetTag(owner, kw); err != nil {
					if _, createErr := s.tags.CreateTag(owner, kw); createErr != nil {
						if s.selShared {
							return fmt.Errorf("creating owner tag: %w", createErr)
						}
						s.logger.Warn("imap: auto-create tag failed", "tag", kw, "error", createErr)
					}
				}
			}
		}
		if err := store.SetTags(&meta, keywords); err != nil {
			return fmt.Errorf("setting tags: %w", err)
		}
	}

	// Persist updated MailMeta via Raft.
	if err := s.mailboxes.StoreMailMeta(owner, &meta); err != nil {
		return fmt.Errorf("persisting mail meta: %w", err)
	}

	// Update the denormalised Tags field on the MailboxMessage.
	if writeErr := s.mailboxes.WriteMessageTags(owner, mailbox, uid, meta.Tags); writeErr != nil {
		// On a shared folder MailMeta is already written, so swallowing this
		// would leave it and MailboxMessage.Tags disagreeing behind an OK.
		if s.selShared {
			return fmt.Errorf("updating mailbox message tags: %w", writeErr)
		}
		s.logger.Warn("imap: updating mailbox message tags failed", "uid", uid, "error", writeErr)
	}

	// Update SQLite index (best-effort).
	if s.metadata != nil {
		if err := s.metadata.IndexMailTags(owner, mailGUID, meta.Tags); err != nil {
			s.logger.Warn("imap: sqlite tag index update failed", "guid", mailGUID, "error", err)
		}
	}

	// Update Bleve index (best-effort).
	if s.search != nil {
		if err := s.search.IndexMail(owner, mailGUID, meta.Subject, meta.From, meta.To, "", meta.ReceivedAt, false, meta.Tags); err != nil {
			s.logger.Warn("imap: search index failed on store", "guid", mailGUID, "error", err)
		}
	}

	return nil
}

// Copy copies messages to another mailbox.
//
// COPY (and MOVE, which is COPY plus removal) is deliberately not quota-gated
// and does not count toward usage. It writes a new MailboxMessage that points
// at the existing MailMeta and blob: no new bytes are stored, and usage is
// summed per MailMeta, so the copy is free under the existing accounting.
// Refusing it would also block moving mail to Trash to get back under quota.
//
// Between folders of different owners the copy IS a new MailMeta in the
// destination owner's account (same blob, no tags), so that path runs as one
// composite command and the destination owner's quota applies (OVERQUOTA).
func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	defer observability.IMAPCopy.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	if s.selectedMailbox == "" {
		return nil, fmt.Errorf("no mailbox selected")
	}
	if err := s.checkSelected(); err != nil {
		return nil, err
	}
	srcOwner, srcName := s.sel()
	dst, err := s.resolveMailbox(dest, errTryCreate)
	if err != nil {
		return nil, err
	}
	if err := s.checkTransfer(dst, ""); err != nil {
		return nil, err
	}

	msgs, err := s.mailboxes.ListMessages(srcOwner, srcName)
	if err != nil {
		return nil, err
	}
	s.cachedMsgs = msgs

	if dst.owner != srcOwner {
		srcUIDs, dstUIDs, _, err := s.transferAcrossOwners(srcOwner, srcName, msgs, numSet, dst, false)
		if err != nil {
			return nil, err
		}
		return &imap.CopyData{UIDValidity: s.destValidity(dst), SourceUIDs: srcUIDs, DestUIDs: dstUIDs}, nil
	}

	var srcUIDs, dstUIDs imap.UIDSet
	var copyCount int

	for seqNum, msg := range msgs {
		seqNum := uint32(seqNum + 1)
		uid := imap.UID(msg.Uid)

		if !numSetContains(numSet, seqNum, uid) {
			continue
		}

		if err := s.mailboxes.CopyMessage(srcOwner, srcName, msg.Uid, dst.name); err != nil {
			return nil, err
		}

		srcUIDs.AddNum(uid)
		copyCount++

		// Get the UID assigned in the dest mailbox.
		dstMeta, _ := s.mailboxes.GetMailbox(dst.owner, dst.name)
		if dstMeta != nil {
			dstUIDs.AddNum(imap.UID(dstMeta.UidNext - 1))
		}
	}

	dstMeta, _ := s.mailboxes.GetMailbox(dst.owner, dst.name)
	validity := uint32(0)
	if dstMeta != nil {
		validity = dstMeta.UidValidity
	}

	if copyCount > 0 {
		s.logger.Info("imap: messages copied", "src", s.selectedMailbox, "dest", dest, "user", s.userID, "count", copyCount)
	}

	return &imap.CopyData{
		UIDValidity: validity,
		SourceUIDs:  srcUIDs,
		DestUIDs:    dstUIDs,
	}, nil
}

// Move moves messages to another mailbox (MOVE extension).
func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	defer observability.IMAPMove.Since(time.Now())
	if err := s.checkAccount(); err != nil {
		return err
	}
	if s.selectedMailbox == "" {
		return fmt.Errorf("no mailbox selected")
	}
	// MOVE deletes from the source (RFC 6851), so a read-only source forbids
	// it. COPY deliberately has no such check: it only writes to dest and
	// leaves the selected mailbox untouched, which EXAMINE permits.
	if err := s.checkSelected(); err != nil {
		return err
	}
	if s.readOnly {
		return errMailboxReadOnly
	}
	srcOwner, srcName := s.sel()
	dst, err := s.resolveMailbox(dest, errTryCreate)
	if err != nil {
		return err
	}
	// MOVE removes from the source, so it needs t and e there as well.
	if err := s.checkTransfer(dst, "te"); err != nil {
		return err
	}

	msgs, err := s.mailboxes.ListMessages(srcOwner, srcName)
	if err != nil {
		return err
	}
	s.cachedMsgs = msgs

	var srcUIDs, dstUIDs imap.UIDSet
	var expungeSeqNums []uint32
	var movedUIDs []uint32

	if dst.owner != srcOwner {
		var err error
		srcUIDs, dstUIDs, expungeSeqNums, err = s.transferAcrossOwners(srcOwner, srcName, msgs, numSet, dst, true)
		if err != nil {
			return err
		}
		for seqNum, msg := range msgs {
			if numSetContains(numSet, uint32(seqNum+1), imap.UID(msg.Uid)) {
				movedUIDs = append(movedUIDs, msg.Uid)
			}
		}
		msgs = nil
	}

	for seqNum, msg := range msgs {
		seqNum := uint32(seqNum + 1)
		uid := imap.UID(msg.Uid)

		if !numSetContains(numSet, seqNum, uid) {
			continue
		}

		if err := s.mailboxes.MoveMessage(srcOwner, srcName, msg.Uid, dst.name); err != nil {
			return err
		}

		srcUIDs.AddNum(uid)
		dstMeta, _ := s.mailboxes.GetMailbox(dst.owner, dst.name)
		if dstMeta != nil {
			dstUIDs.AddNum(imap.UID(dstMeta.UidNext - 1))
		}
		expungeSeqNums = append(expungeSeqNums, seqNum)
		movedUIDs = append(movedUIDs, msg.Uid)
	}

	dstMeta, _ := s.mailboxes.GetMailbox(dst.owner, dst.name)
	validity := uint32(0)
	if dstMeta != nil {
		validity = dstMeta.UidValidity
	}

	if len(expungeSeqNums) > 0 {
		s.logger.Info("imap: messages moved", "src", s.selectedMailbox, "dest", dest, "user", s.userID, "count", len(expungeSeqNums))
	}

	if err := w.WriteCopyData(&imap.CopyData{
		UIDValidity: validity,
		SourceUIDs:  srcUIDs,
		DestUIDs:    dstUIDs,
	}); err != nil {
		return err
	}

	// Write expunge notifications in reverse order.
	for i := len(expungeSeqNums) - 1; i >= 0; i-- {
		if err := w.WriteExpunge(expungeSeqNums[i]); err != nil {
			return err
		}
	}

	// MOVE deletes from the source (RFC 6851) and has just told the client so,
	// which changes the client's view as well as the cache.
	for _, uid := range movedUIDs {
		s.dropReported(uid)
	}

	// Refresh cache.
	s.cachedMsgs, _ = s.mailboxes.ListMessages(srcOwner, srcName)

	return nil
}

// matchesCriteria checks if a message matches the IMAP search criteria.
func (s *session) matchesCriteria(msg *bongov1.MailboxMessage, criteria *imap.SearchCriteria) bool {
	// Flag filters.
	for _, flag := range criteria.Flag {
		if !hasFlag(msg.Flags, flag) {
			return false
		}
	}
	for _, flag := range criteria.NotFlag {
		if hasFlag(msg.Flags, flag) {
			return false
		}
	}

	// Date filters.
	msgDate := time.Unix(msg.InternalDate, 0)
	if !criteria.Since.IsZero() && msgDate.Before(criteria.Since) {
		return false
	}
	if !criteria.Before.IsZero() && !msgDate.Before(criteria.Before) {
		return false
	}

	// Header/body text search requires loading the message content.
	if len(criteria.Header) > 0 || len(criteria.Body) > 0 || len(criteria.Text) > 0 {
		owner, _ := s.sel()
		_, rawMsg, err := s.loadMailContent(owner, msg.MailGuid)
		if err != nil || rawMsg == nil {
			return false
		}

		rawStr := strings.ToLower(string(rawMsg))

		for _, h := range criteria.Header {
			target := strings.ToLower(h.Value)
			if !strings.Contains(rawStr, target) {
				return false
			}
		}

		for _, body := range criteria.Body {
			if !strings.Contains(rawStr, strings.ToLower(body)) {
				return false
			}
		}

		for _, text := range criteria.Text {
			if !strings.Contains(rawStr, strings.ToLower(text)) {
				return false
			}
		}
	}

	// NOT criteria.
	for _, notCrit := range criteria.Not {
		if s.matchesCriteria(msg, &notCrit) {
			return false
		}
	}

	// OR criteria.
	for _, orPair := range criteria.Or {
		if !s.matchesCriteria(msg, &orPair[0]) && !s.matchesCriteria(msg, &orPair[1]) {
			return false
		}
	}

	return true
}

// Helper functions.

func numSetContains(numSet imap.NumSet, seqNum uint32, uid imap.UID) bool {
	switch set := numSet.(type) {
	case imap.SeqSet:
		return set.Contains(seqNum)
	case imap.UIDSet:
		return set.Contains(uid)
	}
	return false
}

// reportedSeqNum maps a UID to the sequence number the CLIENT would use for
// it, which is its position in reportedUIDs -- not its position in cachedMsgs.
//
// A sequence number is only meaningful relative to what the client has been
// told the mailbox contains. The two views agree most of the time, because the
// usual divergence is an append the client has not been told about yet, and
// appends land at the end in UID order without shifting anything before them.
// They stop agreeing the moment another connection removes a message between
// the client's last update and this command: the server list then shortens
// while the client's has not, every position after the removal shifts by one,
// and a number computed from the server view addresses the wrong message.
//
// That matters most for EXPUNGE, where the wrong number makes the client drop
// a message it should have kept, and for SEARCH, where it returns hits the
// client cannot resolve. Returns 0 for a UID the client has never been told
// about, which both callers treat as "skip" -- correct, since the client
// cannot act on a message it does not know exists.
func (s *session) reportedSeqNum(uid uint32) uint32 {
	for i, reported := range s.reportedUIDs {
		if reported == uid {
			return uint32(i + 1)
		}
	}
	return 0
}

func (s *session) removeFromCache(uid uint32) {
	for i, msg := range s.cachedMsgs {
		if msg.Uid == uid {
			s.cachedMsgs = append(s.cachedMsgs[:i], s.cachedMsgs[i+1:]...)
			return
		}
	}
}

func flagsToIMAP(flags uint32) []imap.Flag {
	var result []imap.Flag
	if flags&store.FlagSeen != 0 {
		result = append(result, imap.FlagSeen)
	}
	if flags&store.FlagAnswered != 0 {
		result = append(result, imap.FlagAnswered)
	}
	if flags&store.FlagFlagged != 0 {
		result = append(result, imap.FlagFlagged)
	}
	if flags&store.FlagDeleted != 0 {
		result = append(result, imap.FlagDeleted)
	}
	if flags&store.FlagDraft != 0 {
		result = append(result, imap.FlagDraft)
	}
	return result
}

// tagsToIMAPKeywords converts tag names to IMAP keyword flags with a $ prefix.
// Tag "work" becomes keyword "$work". Tags that already start with $ or \ are
// passed through unchanged to handle pre-existing standard keywords.
func tagsToIMAPKeywords(tags []string) []imap.Flag {
	if len(tags) == 0 {
		return nil
	}
	result := make([]imap.Flag, 0, len(tags))
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if strings.HasPrefix(tag, "$") || strings.HasPrefix(tag, "\\") {
			result = append(result, imap.Flag(tag))
		} else {
			result = append(result, imap.Flag("$"+tag))
		}
	}
	return result
}

// messageToIMAPFlags combines system flag bits and tag-derived keywords into
// a single IMAP flag list suitable for FETCH FLAGS responses.
func messageToIMAPFlags(flags uint32, tags []string) []imap.Flag {
	result := flagsToIMAP(flags)
	result = append(result, tagsToIMAPKeywords(tags)...)
	return result
}

// separateFlags splits a list of IMAP flags into standard system flags
// (prefixed with \) and custom keyword tags (prefixed with $, or no prefix).
// Keywords are returned as bare tag names (the $ prefix is stripped).
func separateFlags(flags []imap.Flag) (stdFlags []imap.Flag, keywords []string) {
	for _, f := range flags {
		s := string(f)
		switch {
		case strings.HasPrefix(s, "\\"):
			stdFlags = append(stdFlags, f)
		case strings.HasPrefix(s, "$"):
			name := strings.ToLower(s[1:])
			if name != "" {
				keywords = append(keywords, name)
			}
		}
		// Flags with no prefix and no $ are ignored (non-standard).
	}
	return stdFlags, keywords
}

func imapFlagsToUint32(flags []imap.Flag) uint32 {
	var result uint32
	for _, f := range flags {
		switch f {
		case imap.FlagSeen:
			result |= store.FlagSeen
		case imap.FlagAnswered:
			result |= store.FlagAnswered
		case imap.FlagFlagged:
			result |= store.FlagFlagged
		case imap.FlagDeleted:
			result |= store.FlagDeleted
		case imap.FlagDraft:
			result |= store.FlagDraft
		}
	}
	return result
}

func hasFlag(flags uint32, flag imap.Flag) bool {
	switch flag {
	case imap.FlagSeen:
		return flags&store.FlagSeen != 0
	case imap.FlagAnswered:
		return flags&store.FlagAnswered != 0
	case imap.FlagFlagged:
		return flags&store.FlagFlagged != 0
	case imap.FlagDeleted:
		return flags&store.FlagDeleted != 0
	case imap.FlagDraft:
		return flags&store.FlagDraft != 0
	}
	return false
}

// maskSharedAppendFlags keeps only the system flags the grantee may set on the
// owner's folder: \Seen needs s, \Deleted needs t, and the rest need w.
func maskSharedAppendFlags(flags uint32, dest resolvedMailbox) uint32 {
	allowed := uint32(0)
	if dest.has("s") {
		allowed |= store.FlagSeen
	}
	if dest.has("t") {
		allowed |= store.FlagDeleted
	}
	if dest.has("w") {
		allowed |= store.FlagAnswered | store.FlagFlagged | store.FlagDraft
	}
	return flags & allowed
}

// destValidity returns dst's current UIDVALIDITY, read fresh so a COPYUID
// response carries the destination's value and not the source's.
func (s *session) destValidity(dst resolvedMailbox) uint32 {
	meta, err := s.mailboxes.GetMailbox(dst.owner, dst.name)
	if err != nil || meta == nil {
		return 0
	}
	return meta.UidValidity
}

// transferAcrossOwners copies or moves the messages numSet selects into dst,
// which belongs to a different owner than the source. Rights on both sides have
// been checked by the caller; this adds the storage gate and the destination
// owner's quota, both before any write, then issues the composite command(s)
// through MailboxStore.TransferMessages. The source sequence numbers of the
// moved messages are returned for the EXPUNGE responses of a MOVE.
//
// The flags a copy keeps are masked by the user's rights on dst (RFC 4314); the
// owner's keywords are never carried over. A request beyond
// store.MaxMailboxTransferItems is several commands that are not atomic
// together; the error then leaves the earlier chunks applied.
func (s *session) transferAcrossOwners(srcOwner, srcName string, msgs []*bongov1.MailboxMessage, numSet imap.NumSet, dst resolvedMailbox, move bool) (srcUIDs, dstUIDs imap.UIDSet, seqNums []uint32, err error) {
	var picked []*bongov1.MailboxMessage
	var uids []uint32
	for seqNum, msg := range msgs {
		if !numSetContains(numSet, uint32(seqNum+1), imap.UID(msg.Uid)) {
			continue
		}
		picked = append(picked, msg)
		uids = append(uids, msg.Uid)
		seqNums = append(seqNums, uint32(seqNum+1))
	}
	if len(uids) == 0 {
		return srcUIDs, dstUIDs, nil, nil
	}
	if s.storage != nil && s.storage.Low() {
		return srcUIDs, dstUIDs, nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeUnavailable,
			Text: "Insufficient system storage",
		}
	}
	if err := s.checkTransferQuota(srcOwner, dst, picked); err != nil {
		return srcUIDs, dstUIDs, nil, err
	}
	pairs, err := s.mailboxes.TransferMessages(srcOwner, srcName, dst.owner, dst.name, uids, move, transferFlagMask(dst))
	if err != nil {
		s.logger.Warn("imap: cross-owner transfer failed", "user", s.userID, "mailbox", dst.name, "move", move, "error", err)
		// Refusals from the FSM carry store detail (guids, uids) that a
		// client has no use for; only protocol errors built here pass through.
		var imapErr *imap.Error
		if errors.As(err, &imapErr) {
			return srcUIDs, dstUIDs, nil, err
		}
		return srcUIDs, dstUIDs, nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Text: "Copy or move to the shared folder failed",
		}
	}
	for _, p := range pairs {
		srcUIDs.AddNum(imap.UID(p.SrcUID))
		dstUIDs.AddNum(imap.UID(p.DstUID))
	}
	s.logger.Info("imap: messages transferred across owners", "user", s.userID, "move", move, "count", len(pairs))
	return srcUIDs, dstUIDs, seqNums, nil
}
