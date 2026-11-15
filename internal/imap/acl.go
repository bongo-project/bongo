package imap

import (
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The ACL extension (RFC 4314) over the grant key space. SETACL and DELETEACL
// write through the same helpers the web share form uses, so a folder shared
// from a mail client is indistinguishable from one shared in the browser: a new
// user grantee is pending and gets the invitation mail, a change of rights
// keeps the grantee's state, and a group of the owner's domain is accepted.
//
// Every refusal is an *imap.Error with fixed text. go-imap sends those as they
// are and turns any other error into "internal server error", and none of the
// texts echoes the folder name or the identifier.
//
// Existence is never revealed. A folder the user cannot see answers as a
// missing one, because every command goes through resolveMailbox. LISTRIGHTS
// and DELETEACL never resolve an identifier. SETACL does have to, which makes
// it an account oracle exactly like the web share form, so it shares that
// form's per-user limiter and charges it before anything is read, and every
// identifier that is not a local user or a shareable group gets one text.
//
// Rights are RFC 4314 letters. The obsolete c and d of RFC 2086 are refused as
// unknown rights (BAD) rather than mapped, so nothing is granted that the
// client did not spell out. A user holding the administer right may grant any
// letter, a itself included, and may change or remove anyone's grant except the
// owner's, which no one can touch. A grant made that way records the acting
// user as grantedBy. That holds for a change as much as a new grant: the rights
// change goes through SetGrantKeepState, which keeps the state but rewrites
// granted_by, so a delegate who changes someone's rights becomes the recorded
// granter of that grant. This is intended; granted_by names the last person to
// set the rights.

var (
	// errACLIdentifier answers every SETACL or DELETEACL identifier that cannot
	// be acted on: unknown, external, disabled, catch-all only, a group of
	// another domain, the owner, anyone, authenticated and negative rights.
	errACLIdentifier = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Text: "Cannot grant rights to that identifier",
	}

	// errACLCapReached answers a SETACL for a new grantee on a folder already
	// shared with the maximum number of people. It is given for a resolvable
	// and an unresolvable identifier alike, so a full folder is not an oracle.
	errACLCapReached = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeLimit,
		Text: "This folder is shared with the maximum number of people",
	}

	// errShareRateLimited answers a SETACL over the per-user share budget.
	errShareRateLimited = &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeLimit,
		Text: "Too many share requests, try again later",
	}

	// errBadRights answers a rights string with a letter outside lrswipkxtea.
	errBadRights = &imap.Error{
		Type: imap.StatusResponseTypeBad,
		Text: "Unknown right",
	}
)

// ErrShareRefused is what a ShareOfferer returns when the folder is full and
// the grantee is new.
var ErrShareRefused = errors.New("share refused")

// MailboxOffer describes one share of a folder.
type MailboxOffer struct {
	Owner        string
	OwnerEmail   string
	Resource     string
	ResourceName string
	// GranteeID names a user grantee; GroupID a group grantee instead.
	GranteeID string
	GroupID   string
	// Recipient is the address an invitation goes to.
	Recipient string
	Rights    string
	// GrantedBy is the user who made the share.
	GrantedBy string
	// Full says the folder is at its grant cap.
	Full bool
	Now  time.Time
}

// ShareOfferer creates grants the way the web share form does. It is defined
// here, at the consumer, because the web handlers import this package.
type ShareOfferer interface {
	// OfferUser writes a pending grant and queues the invitation for a new
	// grantee, or changes the rights of an existing one and keeps its state.
	OfferUser(MailboxOffer) error
	// OfferGroup writes an accepted grant for a group of the owner's domain.
	OfferGroup(MailboxOffer) error
}

// shareResolver maps an address to the id of an existing, enabled local
// account. It must not resolve through catch-alls, and answers a group address
// with *store.GroupRecipientError.
type shareResolver interface {
	ResolveAccount(email string) (string, error)
}

type groupReader interface {
	GetGroup(id string) (*bongov1.Group, error)
}

// shareLimiter is the per-user share budget, the same instance the web uses.
type shareLimiter interface {
	Allow(key string) bool
}

// shareDeps is what SETACL needs beyond the grant store. Any part may be nil;
// SETACL for a new identifier then answers errACLIdentifier.
type shareDeps struct {
	resolver shareResolver
	groups   groupReader
	limiter  shareLimiter
	offerer  ShareOfferer
}

// SetShareDeps installs what SETACL uses to resolve an identifier and write a
// grant. Like SetACLStore it applies to sessions created afterwards, so call it
// before the first connection is accepted. Pass nil interfaces, not typed nils.
func (s *Server) SetShareDeps(r shareResolver, g groupReader, l shareLimiter, o ShareOfferer) {
	s.share.Store(&shareDeps{resolver: r, groups: g, limiter: l, offerer: o})
}

// mailboxRightLetters is every right in RFC 4314 order.
const mailboxRightLetters = allMailboxRights

// canonicalRights validates a rights string and returns it with each distinct
// letter once, in lrswipkxtea order, so equal sets compare equal as strings.
func canonicalRights(r string) (string, error) {
	for _, l := range r {
		if !strings.ContainsRune(mailboxRightLetters, l) {
			return "", errBadRights
		}
	}
	var b strings.Builder
	for _, l := range mailboxRightLetters {
		if strings.ContainsRune(r, l) {
			b.WriteRune(l)
		}
	}
	return b.String(), nil
}

func addRights(base, more string) string {
	out, _ := canonicalRights(base + more)
	return out
}

func removeRights(base, less string) string {
	var b strings.Builder
	for _, l := range base {
		if !strings.ContainsRune(less, l) {
			b.WriteRune(l)
		}
	}
	out, _ := canonicalRights(b.String())
	return out
}

// reservedIdentifier reports identifiers no one may be granted rights as.
func reservedIdentifier(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "", "anyone", "authenticated":
		return true
	}
	return strings.HasPrefix(id, "-")
}

// aclOwnerAddress is the primary address of the folder's owner, "" when it
// cannot be read.
func (s *session) aclOwnerAddress(rm resolvedMailbox) string {
	if rm.shared {
		return rm.addr
	}
	if s.acl == nil {
		return ""
	}
	if addr, err := s.acl.PrimaryAddressOf(rm.owner); err == nil {
		return addr
	}
	return s.acl.UserEmail(rm.owner)
}

// aclResolve resolves the folder for an ACL command that needs the
// administer right. Without a grant store nothing is shared and nothing can be
// administered, so it answers as a missing folder.
func (s *session) aclResolve(mailbox string) (resolvedMailbox, error) {
	rm, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
	if err != nil {
		return resolvedMailbox{}, err
	}
	if s.acl == nil {
		return resolvedMailbox{}, errNoSuchMailbox
	}
	if err := requireRight(rm, "a"); err != nil {
		return resolvedMailbox{}, err
	}
	return rm, nil
}

// aclGrant is one live grant with the identifier GETACL shows for it.
type aclGrant struct {
	principal  string
	identifier string
	grant      *bongov1.Grant
	// expired is a pending grant past its window; it is listed only to be
	// matched by DELETEACL and never shown or counted.
	expired bool
}

// aclGrants lists the grants on the folder with their identifiers. A grantee
// whose address or group is gone has no identifier and is left out.
func (s *session) aclGrants(rm resolvedMailbox, now time.Time) ([]aclGrant, error) {
	if rm.id == "" {
		return nil, nil
	}
	list, err := s.acl.ListResourceGrants(store.ACLKindMailbox, rm.owner, rm.id)
	if err != nil {
		return nil, fmt.Errorf("listing grants: %w", err)
	}
	var out []aclGrant
	for _, g := range list {
		var ident string
		switch {
		case strings.HasPrefix(g.Principal, "u:"):
			ident = s.acl.UserEmail(strings.TrimPrefix(g.Principal, "u:"))
		case strings.HasPrefix(g.Principal, "g:"):
			if s.share.groups != nil {
				if grp, err := s.share.groups.GetGroup(strings.TrimPrefix(g.Principal, "g:")); err == nil {
					ident = grp.Address
				}
			}
		}
		if ident == "" {
			continue
		}
		out = append(out, aclGrant{
			principal: g.Principal, identifier: ident, grant: g.Grant,
			expired: store.PendingGrantExpired(g.Grant, now.Unix()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].identifier < out[j].identifier })
	return out, nil
}

func matchGrant(grants []aclGrant, identifier string, includeExpired bool) *aclGrant {
	for i := range grants {
		if grants[i].expired && !includeExpired {
			continue
		}
		if strings.EqualFold(grants[i].identifier, strings.TrimSpace(identifier)) {
			return &grants[i]
		}
	}
	return nil
}

// MyRights implements imapserver.SessionACL. A folder the user cannot see is a
// missing folder; one they can see reports the rights re-read for this command.
// It writes nothing, so a personal folder is never given an id here.
func (s *session) MyRights(mailbox string) (*imap.MyRightsData, error) {
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	rm, err := s.resolveMailbox(mailbox, errNoSuchMailbox)
	if err != nil {
		return nil, err
	}
	return &imap.MyRightsData{Mailbox: mailbox, Rights: imap.RightSet(rm.rights)}, nil
}

// GetACL implements imapserver.SessionACL. It lists the owner, then every
// accepted and pending grant that has not expired. A pending grant is shown so
// an administrator can see and withdraw an invitation.
func (s *session) GetACL(mailbox string) (*imap.GetACLData, error) {
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	rm, err := s.aclResolve(mailbox)
	if err != nil {
		return nil, err
	}
	grants, err := s.aclGrants(rm, time.Now())
	if err != nil {
		return nil, err
	}
	data := &imap.GetACLData{Mailbox: mailbox}
	if owner := s.aclOwnerAddress(rm); owner != "" {
		data.ACL = append(data.ACL, imap.ACLEntry{Identifier: imap.RightsIdentifier(owner), Rights: imap.RightSet(allMailboxRights)})
	}
	for _, g := range grants {
		if g.expired {
			continue
		}
		data.ACL = append(data.ACL, imap.ACLEntry{Identifier: imap.RightsIdentifier(g.identifier), Rights: imap.RightSet(g.grant.Rights)})
	}
	return data, nil
}

// ListRights implements imapserver.SessionACL. The identifier is not resolved,
// because answering differently for a real account would be an oracle: the
// owner's rights are fixed, and everyone else may be given any of the letters
// SETACL accepts.
func (s *session) ListRights(mailbox string, identifier imap.RightsIdentifier) (*imap.ListRightsData, error) {
	if err := s.checkAccount(); err != nil {
		return nil, err
	}
	rm, err := s.aclResolve(mailbox)
	if err != nil {
		return nil, err
	}
	data := &imap.ListRightsData{Mailbox: mailbox, Identifier: identifier}
	if owner := s.aclOwnerAddress(rm); owner != "" && strings.EqualFold(owner, strings.TrimSpace(string(identifier))) {
		data.RequiredRights = imap.RightSet(allMailboxRights)
		return data, nil
	}
	for _, l := range mailboxRightLetters {
		data.OptionalRights = append(data.OptionalRights, imap.RightSet(string(l)))
	}
	return data, nil
}

// SetACL implements imapserver.SessionACL.
//
// This is several commands and not atomic. A personal folder with no id is
// given one first (its own command), then the grant is written, then the
// invitation is queued best-effort: a failure between them leaves a folder
// with an id and no grant, which is harmless, and the grant is what counts.
// The cap is read before the write, so concurrent SETACLs can overshoot it, as
// on the web.
func (s *session) SetACL(mailbox string, identifier imap.RightsIdentifier, mod imap.RightModification, rights imap.RightSet) error {
	if err := s.checkAccount(); err != nil {
		return err
	}
	// Charged before anything is read or resolved, so every outcome counts.
	if s.share.limiter != nil && !s.share.limiter.Allow(s.userID) {
		return errShareRateLimited
	}
	given, err := canonicalRights(string(rights))
	if err != nil {
		return err
	}
	ident := strings.TrimSpace(string(identifier))
	if reservedIdentifier(ident) {
		return errACLIdentifier
	}
	rm, err := s.aclResolve(mailbox)
	if err != nil {
		return err
	}
	now := time.Now()
	full := false
	if rm.id != "" {
		if full, err = s.acl.GrantCapReached(store.ACLKindMailbox, rm.owner, rm.id, now); err != nil {
			return fmt.Errorf("reading grant cap: %w", err)
		}
	}
	grants, err := s.aclGrants(rm, now)
	if err != nil {
		return err
	}
	ownerAddr := s.aclOwnerAddress(rm)
	if ownerAddr != "" && strings.EqualFold(ownerAddr, ident) {
		return errACLIdentifier
	}

	var principal, granteeID, groupID, recipient string
	if g := matchGrant(grants, ident, false); g != nil {
		principal, recipient = g.principal, g.identifier
		if id, ok := strings.CutPrefix(principal, "u:"); ok {
			granteeID = id
		} else {
			groupID = strings.TrimPrefix(principal, "g:")
		}
	} else {
		// A new grantee on a full folder is the cap answer whether or not the
		// identifier resolves, so the cap cannot be used to find accounts.
		if full {
			return errACLCapReached
		}
		granteeID, groupID, recipient, err = s.aclResolveTarget(ident, ownerAddr, rm.owner)
		if err != nil {
			return err
		}
		principal = store.GranteeUser(granteeID)
		if groupID != "" {
			principal = store.GranteeGroup(groupID)
		}
	}

	// The base of + and - is this one principal's stored grant, never the
	// effective rights, which would copy a group's rights into a user grant.
	var existing *bongov1.Grant
	if rm.id != "" {
		if existing, err = s.acl.GetGrant(store.ACLKindMailbox, rm.owner, rm.id, principal); err != nil {
			return fmt.Errorf("reading grant: %w", err)
		}
	}
	base := ""
	if existing != nil && !store.PendingGrantExpired(existing, now.Unix()) {
		base = existing.Rights
	}
	result := given
	switch mod {
	case imap.RightModificationAdd:
		result = addRights(base, given)
	case imap.RightModificationRemove:
		result = removeRights(base, given)
	}
	if result == "" {
		return s.aclRevoke(rm, principal, existing)
	}

	if s.share.offerer == nil {
		return errACLIdentifier
	}
	if rm.id == "" {
		if rm.id, err = s.mailboxes.EnsureMailboxID(rm.owner, rm.name); err != nil {
			return fmt.Errorf("assigning mailbox id: %w", err)
		}
	}
	offer := MailboxOffer{
		Owner: rm.owner, OwnerEmail: ownerAddr, Resource: rm.id, ResourceName: rm.name,
		GranteeID: granteeID, GroupID: groupID, Recipient: recipient, Rights: result,
		GrantedBy: s.userID, Full: full, Now: now,
	}
	if groupID != "" {
		err = s.share.offerer.OfferGroup(offer)
	} else {
		err = s.share.offerer.OfferUser(offer)
	}
	switch {
	case errors.Is(err, ErrShareRefused):
		return errACLCapReached
	case err != nil:
		return fmt.Errorf("writing grant: %w", err)
	}
	s.logger.Info("imap: folder grant set", "user", s.userID, "owner", rm.owner)
	return nil
}

// aclResolveTarget turns an identifier that matches no grant into a grantee: a
// local user, or a group of the folder owner's domain. Every failure is
// errACLIdentifier.
func (s *session) aclResolveTarget(ident, ownerAddr, ownerID string) (userID, groupID, recipient string, err error) {
	parsed, perr := mail.ParseAddress(ident)
	if perr != nil || parsed.Name != "" || s.share.resolver == nil {
		return "", "", "", errACLIdentifier
	}
	id, rerr := s.share.resolver.ResolveAccount(parsed.Address)
	var groupErr *store.GroupRecipientError
	if errors.As(rerr, &groupErr) {
		// The domain is the folder owner's, never the acting grantee's, so a
		// delegate cannot attach the groups of their own domain.
		if s.share.groups == nil {
			return "", "", "", errACLIdentifier
		}
		gid, ok := store.ShareableGroup(s.share.groups, groupErr.GroupID, ownerAddr)
		if !ok {
			return "", "", "", errACLIdentifier
		}
		return "", gid, parsed.Address, nil
	}
	if rerr != nil || id == "" || id == ownerID {
		return "", "", "", errACLIdentifier
	}
	return id, "", parsed.Address, nil
}

// aclRevoke removes one grant. Nothing to remove is success.
func (s *session) aclRevoke(rm resolvedMailbox, principal string, existing *bongov1.Grant) error {
	if rm.id == "" || existing == nil {
		return nil
	}
	if err := s.acl.RevokeGrant(store.ACLKindMailbox, rm.owner, rm.id, principal); err != nil {
		return fmt.Errorf("revoking grant: %w", err)
	}
	s.logger.Info("imap: folder grant removed", "user", s.userID, "owner", rm.owner)
	return nil
}

// DeleteACL implements imapserver.SessionACL. It matches the identifier only
// against the folder's own grants and resolves nothing, so it is no oracle.
func (s *session) DeleteACL(mailbox string, identifier imap.RightsIdentifier) error {
	if err := s.checkAccount(); err != nil {
		return err
	}
	ident := strings.TrimSpace(string(identifier))
	if reservedIdentifier(ident) {
		return errACLIdentifier
	}
	rm, err := s.aclResolve(mailbox)
	if err != nil {
		return err
	}
	if owner := s.aclOwnerAddress(rm); owner != "" && strings.EqualFold(owner, ident) {
		return errACLIdentifier
	}
	grants, err := s.aclGrants(rm, time.Now())
	if err != nil {
		return err
	}
	g := matchGrant(grants, ident, true)
	if g == nil {
		return nil
	}
	return s.aclRevoke(rm, g.principal, g.grant)
}
