package imap

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Shared folders appear under {other_users_prefix}/{owner primary address}/{folder}.
//
// Every session method reaches a mailbox name through resolve, so there is one
// place that decides whether a name is the user's own folder or somebody
// else's, and one rule for what a user may not see: a name that does not
// resolve, a folder the user holds no accepted grant on, a pending or expired
// pending grant, and a grant without the lookup right all behave exactly like
// a folder that does not exist. The refusal is the caller's own not-found
// error (NONEXISTENT for SELECT, TRYCREATE for APPEND, the reserved-name error
// for CREATE), never a different text, so a probe cannot tell a folder it may
// not see from one that is not there. Only a user who holds the lookup right
// gets NO [NOPERM] for a command that needs another right; that reveals
// nothing LIST had not already shown.
//
// Grants name a folder by its MailboxMeta id, so the owner renaming it does
// not unshare it, and a new folder that takes the old name is not shared.
//
// Personal folders under the other-users prefix are unreachable over IMAP:
// CREATE and RENAME have refused such names since the prefix was reserved, and
// the resolver now reads the whole tree as shared. An owner address that
// contains "/" is unreachable too, because the address ends at the first one.

// allMailboxRights is what the owner of a folder holds on it.
const allMailboxRights = "lrswipkxtea"

var (
	// errNoPermission answers a command the user holds the lookup right for
	// but not the right the command needs (RFC 4314 section 5.2).
	errNoPermission = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeNoPerm,
		Text: "Permission denied",
	}

	// errCrossOwner answers COPY and MOVE between folders of different
	// owners. Writing a message into another account needs quota, indexing and
	// one composite command, which a later change adds.
	errCrossOwner = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeCannot,
		Text: "Copying or moving between different owners is not supported yet",
	}

	// errCrossNamespace answers RENAME of a shared folder to a name outside
	// the owner's tree.
	errCrossNamespace = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeCannot,
		Text: "A shared folder can only be renamed within its owner's tree",
	}

	// errSharedKeywords answers a keyword change on a shared folder. The
	// owner's tags are shown as read-only keywords until keywords on shared
	// messages become the owner's tags.
	errSharedKeywords = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeCannot,
		Text: "Keywords are not supported on shared folders yet",
	}
)

// errSharedNotFound is what resolve returns for every name that does not
// resolve. It wraps store.ErrNotFound so callers map it with
// mailboxLookupError exactly as they map a missing personal folder.
var errSharedNotFound = fmt.Errorf("shared mailbox: %w", store.ErrNotFound)

// resolvedMailbox is a mailbox name resolved for the session's user.
type resolvedMailbox struct {
	// owner is the account whose Pebble namespace holds the folder; it is the
	// session user for personal names. name is the folder's name in that
	// namespace.
	owner, name string
	// id is the folder's stable id, empty for a personal folder never shared.
	id     string
	shared bool
	// rights are the RFC 4314 rights the user holds; a personal folder carries
	// all of them so a right check needs no special case.
	rights string
	// addr is the owner's canonical primary address (shared only).
	addr string
	meta *bongov1.MailboxMeta
}

func (r resolvedMailbox) has(letters string) bool {
	for _, l := range letters {
		if !strings.ContainsRune(r.rights, l) {
			return false
		}
	}
	return true
}

// requireRight refuses with NOPERM when the resolved folder lacks a right. It
// runs only after resolve, so the lookup right is already held.
func requireRight(rm resolvedMailbox, letters string) error {
	if rm.has(letters) {
		return nil
	}
	return errNoPermission
}

func (s *session) ns() store.IMAPNamespaces { return s.namespaces.OrDefaults() }

// resolve maps a client-supplied mailbox name to a folder. A name that does
// not resolve returns an error wrapping store.ErrNotFound; any other error is
// a store failure.
func (s *session) resolve(name string) (resolvedMailbox, error) {
	ns := s.ns()
	if !ns.IsOtherUsersName(name) {
		meta, err := s.mailboxes.GetMailbox(s.userID, name)
		if err != nil {
			return resolvedMailbox{}, err
		}
		return resolvedMailbox{owner: s.userID, name: name, id: meta.Id, rights: allMailboxRights, meta: meta}, nil
	}
	return s.resolveShared(ns, name)
}

func (s *session) resolveShared(ns store.IMAPNamespaces, name string) (resolvedMailbox, error) {
	// Without an ACL store nothing under the prefix resolves.
	if s.acl == nil {
		return resolvedMailbox{}, errSharedNotFound
	}
	addr, folder, ok := ns.SplitOtherUsers(name)
	if !ok {
		return resolvedMailbox{}, errSharedNotFound
	}
	owner, email, err := s.acl.OwnerByPrimaryAddress(addr)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return resolvedMailbox{}, errSharedNotFound
		}
		return resolvedMailbox{}, fmt.Errorf("resolving owner: %w", err)
	}
	// No folder has two names.
	if owner == s.userID {
		return resolvedMailbox{}, errSharedNotFound
	}
	if strings.EqualFold(folder, "INBOX") {
		folder = "INBOX"
	}
	meta, err := s.mailboxes.GetMailbox(owner, folder)
	if err != nil {
		return resolvedMailbox{}, err
	}
	if !store.ValidMailboxID(meta.Id) {
		return resolvedMailbox{}, errSharedNotFound
	}
	rights, err := s.acl.MailboxRights(owner, meta.Id, s.userID, time.Now())
	if err != nil {
		return resolvedMailbox{}, fmt.Errorf("reading rights: %w", err)
	}
	if !strings.ContainsRune(rights, 'l') {
		return resolvedMailbox{}, errSharedNotFound
	}
	return resolvedMailbox{owner: owner, name: folder, id: meta.Id, shared: true, rights: rights, addr: email, meta: meta}, nil
}

// resolveMailbox is resolve with the caller's not-found answer applied.
func (s *session) resolveMailbox(name string, notFound *imap.Error) (resolvedMailbox, error) {
	rm, err := s.resolve(name)
	if err != nil {
		return resolvedMailbox{}, mailboxLookupError(name, err, notFound)
	}
	return rm, nil
}

// sel returns the owner and folder name the selected mailbox lives under.
func (s *session) sel() (owner, name string) {
	if s.selShared {
		return s.selOwner, s.selName
	}
	return s.userID, s.selectedMailbox
}

// selHas reports whether the selected folder grants every right in letters. A
// personal folder grants all of them.
func (s *session) selHas(letters string) bool {
	if !s.selShared {
		return true
	}
	return resolvedMailbox{rights: s.selRights}.has(letters)
}

// selectShared records rm as the selected folder.
func (s *session) selectShared(rm resolvedMailbox) {
	s.selShared = rm.shared
	s.selOwner, s.selName, s.selID, s.selRights, s.selAddr = rm.owner, rm.name, rm.id, rm.rights, rm.addr
}

// checkSelected re-reads what the user may do to a selected shared folder. It
// runs on the same path as checkAccount, at the start of every command that
// works on the selected folder, and on IDLE's timer, because a revoke or a
// downgrade has to take effect without a new SELECT. Rights are never cached
// past one command.
//
// The folder is found again by id, so an owner renaming it is followed and a
// new folder that took the old name is not. Losing the lookup or read right,
// the folder, or the owner answers as a missing mailbox and drops what the
// session holds of it. A store failure is a server error, not a loss.
func (s *session) checkSelected() error {
	if !s.selShared {
		return nil
	}
	lost := func() error {
		s.cachedMsgs = nil
		s.reportedUIDs = nil
		return errNoSuchMailbox
	}
	if s.acl == nil {
		return lost()
	}
	owner, _, err := s.acl.OwnerByPrimaryAddress(s.selAddr)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return lost()
	case err != nil:
		return fmt.Errorf("re-reading owner: %w", err)
	case owner != s.selOwner:
		return lost()
	}
	name, err := s.mailboxes.MailboxNameByID(s.selOwner, s.selID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return lost()
	case err != nil:
		return fmt.Errorf("re-reading shared folder: %w", err)
	}
	rights, err := s.acl.MailboxRights(s.selOwner, s.selID, s.userID, time.Now())
	if err != nil {
		return fmt.Errorf("re-reading rights: %w", err)
	}
	s.selRights = rights
	if !strings.ContainsRune(rights, 'l') || !strings.ContainsRune(rights, 'r') {
		return lost()
	}
	s.selName = name
	return nil
}

// storeNeeds returns the rights a flag change needs: \Seen needs s, \Deleted
// needs t, every other system flag needs w.
func storeNeeds(changed uint32) string {
	var need string
	if changed&store.FlagSeen != 0 {
		need += "s"
	}
	if changed&store.FlagDeleted != 0 {
		need += "t"
	}
	if changed&^(store.FlagSeen|store.FlagDeleted) != 0 {
		need += "w"
	}
	return need
}

// checkTransfer decides whether the selected folder's messages may go to dst
// with COPY or MOVE. Both folders must have the same owner: own to own, or
// within one owner's shared tree. Anything across owners is refused with a
// fixed text, because a cross-owner write needs quota, indexing and a single
// composite command that a later change adds. dst needs the insert right and,
// for MOVE, the selected folder needs srcNeeds (t and e). dst has been
// resolved, so the user already holds the lookup right on it.
func (s *session) checkTransfer(dst resolvedMailbox, srcOwner, srcNeeds string) error {
	if dst.owner != srcOwner {
		return errCrossOwner
	}
	if err := requireRight(dst, "i"); err != nil {
		return err
	}
	if srcNeeds != "" && !s.selHas(srcNeeds) {
		return errNoPermission
	}
	return nil
}

// sharedParent splits a shared name into the folder it would sit beneath and
// its own last segment, resolving the parent. CREATE and RENAME use it: a user
// may only make or move folders beneath one they can see, never directly under
// the owner's address. Anything that cannot be a child of a visible folder is
// the reserved-name answer, which is also what CREATE of such a name has always
// returned.
func (s *session) sharedParent(name string) (parent resolvedMailbox, leaf string, err error) {
	_, folder, ok := s.ns().SplitOtherUsers(name)
	if !ok {
		return resolvedMailbox{}, "", errReservedMailboxName
	}
	i := strings.LastIndex(folder, "/")
	if i <= 0 || i == len(folder)-1 {
		return resolvedMailbox{}, "", errReservedMailboxName
	}
	leaf = folder[i+1:]
	parent, err = s.resolveMailbox(name[:len(name)-len(leaf)-1], errReservedMailboxName)
	return parent, leaf, err
}

// createShared creates a folder beneath one the user holds k on. The new
// folder takes the parent's accepted grants so the creator can see what they
// made. Pending grants are not copied: that would refresh their invitation
// window, invite people to a folder they were never offered, and let a k holder
// fill the owner's grant cap.
//
// This is several commands and not atomic: the folder, its id, the creator's
// grant and each other grantee's copy are separate applies. If the creator's own
// grant cannot be written the command fails, and the folder then exists with no
// grant, so only its owner can see or delete it. The copies for the other
// grantees are best-effort and logged; a missing copy leaves that grantee
// without the new folder until the owner shares it.
func (s *session) createShared(name string) error {
	parent, leaf, err := s.sharedParent(name)
	if err != nil {
		return err
	}
	if err := requireRight(parent, "k"); err != nil {
		return err
	}
	child := parent.name + "/" + leaf
	switch _, err := s.mailboxes.GetMailbox(parent.owner, child); {
	case err == nil:
		return errAlreadyExists
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("looking up mailbox %q: %w", name, err)
	}
	if err := s.mailboxes.CreateMailbox(parent.owner, child); err != nil {
		s.logger.Warn("imap: create shared mailbox failed", "mailbox", name, "user", s.userID, "error", err)
		return err
	}
	id, err := s.mailboxes.EnsureMailboxID(parent.owner, child)
	if err != nil {
		s.logger.Warn("imap: shared mailbox created but not given an id", "mailbox", name, "user", s.userID, "error", err)
		return err
	}
	now := time.Now()
	if err := s.acl.SetGrantKeepState(store.ACLKindMailbox, parent.owner, id, store.GranteeUser(s.userID), "",
		parent.rights, store.GrantStateAccepted, parent.owner, now); err != nil {
		s.logger.Warn("imap: shared mailbox created but the creator's grant failed", "mailbox", name, "user", s.userID, "error", err)
		return err
	}
	grants, err := s.acl.ListResourceGrants(store.ACLKindMailbox, parent.owner, parent.id)
	if err != nil {
		s.logger.Warn("imap: could not read the parent's grants to copy", "mailbox", name, "user", s.userID, "error", err)
		return nil
	}
	for _, g := range grants {
		if g.Principal == store.GranteeUser(s.userID) || g.Grant.State != store.GrantStateAccepted || !store.ValidMailboxRights(g.Grant.Rights) {
			continue
		}
		by := g.Grant.GrantedBy
		if by == "" {
			by = parent.owner
		}
		if err := s.acl.SetGrantKeepState(store.ACLKindMailbox, parent.owner, id, g.Principal, "",
			g.Grant.Rights, g.Grant.State, by, now); err != nil {
			s.logger.Warn("imap: could not copy a grant to the new shared mailbox", "mailbox", name, "grantee", g.Principal, "error", err)
		}
	}
	s.logger.Info("imap: shared mailbox created", "mailbox", name, "user", s.userID)
	return nil
}

// renameShared renames a shared folder within its owner's tree.
func (s *session) renameShared(mailbox, newName string) error {
	src, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
	if err != nil {
		return err
	}
	if err := requireRight(src, "x"); err != nil {
		return err
	}
	if strings.EqualFold(src.name, "INBOX") {
		return errNoPermission
	}
	if !s.ns().IsOtherUsersName(newName) {
		return errCrossNamespace
	}
	parent, leaf, err := s.sharedParent(newName)
	if err != nil {
		return err
	}
	if parent.owner != src.owner {
		return errCrossOwner
	}
	if err := requireRight(parent, "k"); err != nil {
		return err
	}
	dst := parent.name + "/" + leaf
	switch _, err := s.mailboxes.GetMailbox(src.owner, dst); {
	case err == nil:
		return errAlreadyExists
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("looking up mailbox %q: %w", newName, err)
	}
	if err := s.mailboxes.RenameMailbox(src.owner, src.name, dst); err != nil {
		s.logger.Warn("imap: rename shared mailbox failed", "old_name", mailbox, "new_name", newName, "user", s.userID, "error", err)
		return err
	}
	s.logger.Info("imap: shared mailbox renamed", "old_name", mailbox, "new_name", newName, "user", s.userID)
	return nil
}

// sharedListEntry is one name LIST or LSUB may show under the other-users
// prefix.
type sharedListEntry struct {
	name       string
	selectable bool
	subscribed bool
	hasKids    bool
	rights     string
	owner, mb  string
}

// sharedListEntries returns the shared folders the user may see, built live from
// the accepted grants they hold, and, unless subscribedOnly, the \Noselect
// levels that lead to them. It is empty for a user who holds no shares, so
// LIST output is unchanged for everyone else.
func (s *session) sharedListEntries(subscribedOnly bool) ([]sharedListEntry, error) {
	if s.acl == nil {
		return nil, nil
	}
	held, err := s.acl.MailboxesSharedWith(s.userID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("listing shared mailboxes: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}
	prefix := s.ns().OtherUsers
	byName := map[string]*sharedListEntry{}
	emails := map[string]string{}
	for _, h := range held {
		if h.Owner == s.userID {
			continue
		}
		email, ok := emails[h.Owner]
		if !ok {
			email, err = s.acl.PrimaryAddressOf(h.Owner)
			switch {
			case errors.Is(err, store.ErrNotFound):
				email = ""
			case err != nil:
				return nil, fmt.Errorf("reading owner address: %w", err)
			}
			emails[h.Owner] = email
		}
		if email == "" || strings.Contains(email, "/") {
			continue
		}
		name, err := s.mailboxes.MailboxNameByID(h.Owner, h.Resource)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading shared mailbox name: %w", err)
		}
		subscribed, err := s.acl.SharedSubscribed(s.userID, h.Owner, h.Resource)
		if err != nil {
			return nil, err
		}
		if subscribedOnly && !subscribed {
			continue
		}
		full := prefix + "/" + email + "/" + name
		byName[full] = &sharedListEntry{name: full, selectable: true, subscribed: subscribed, rights: h.Rights, owner: h.Owner, mb: name}
	}
	if !subscribedOnly {
		for full := range byName {
			for cur := full; ; {
				i := strings.LastIndex(cur, "/")
				if i < 0 {
					break
				}
				cur = cur[:i]
				if len(cur) < len(prefix) {
					break
				}
				if _, ok := byName[cur]; !ok {
					byName[cur] = &sharedListEntry{name: cur}
				}
				if cur == prefix {
					break
				}
			}
		}
	}
	out := make([]sharedListEntry, 0, len(byName))
	for _, e := range byName {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	for i := range out {
		for _, other := range out {
			if strings.HasPrefix(other.name, out[i].name+"/") {
				out[i].hasKids = true
				break
			}
		}
	}
	return out, nil
}

// listShared writes the shared part of a LIST or LSUB response.
func (s *session) listShared(write func(*imap.ListData) error, match func(name string) bool, options *imap.ListOptions) error {
	subscribedOnly := options != nil && options.SelectSubscribed
	entries, err := s.sharedListEntries(subscribedOnly)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !match(e.name) {
			continue
		}
		attrs := []imap.MailboxAttr{imap.MailboxAttrHasNoChildren}
		if e.hasKids {
			attrs = []imap.MailboxAttr{imap.MailboxAttrHasChildren}
		}
		if !e.selectable {
			attrs = append([]imap.MailboxAttr{imap.MailboxAttrNoSelect}, attrs...)
		}
		if e.subscribed && options != nil && (options.ReturnSubscribed || options.SelectSubscribed) {
			attrs = append(attrs, imap.MailboxAttrSubscribed)
		}
		data := &imap.ListData{Mailbox: e.name, Delim: '/', Attrs: attrs}
		if options != nil && options.ReturnStatus != nil && e.selectable && strings.ContainsRune(e.rights, 'r') {
			rm := resolvedMailbox{owner: e.owner, name: e.mb, shared: true, rights: e.rights}
			if st, err := s.statusFor(rm, e.name, options.ReturnStatus); err == nil {
				data.Status = st
			}
		}
		if err := write(data); err != nil {
			return err
		}
	}
	return nil
}
