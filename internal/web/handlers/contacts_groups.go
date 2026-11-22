package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Contact groups on the web. A contact group is an ordinary contact record whose
// card lists other cards (see store/contact_groups.go); "group" alone means a
// distribution group elsewhere in Bongo. Every handler here loads the group by
// (session user, guid) and requires it to be a contact group, so another user's
// guid, a person's guid and an unknown guid are all one 404. Contact data is
// never logged: only ids.

// maxContactGroupNameLen bounds a contact group's name.
const maxContactGroupNameLen = 100

// contactGroupURL is the page of one contact group (and of any contact).
func contactGroupURL(guid string) string { return "/contacts/" + url.PathEscape(guid) }

// groupLockKey names the per-node lock for a read-modify-write of one group.
func groupLockKey(userID, guid string) string { return userID + "/" + guid }

// loadContactGroup answers 404 itself when the guid is not one of the user's
// contact groups.
func (h *ContactsHandler) loadContactGroup(w http.ResponseWriter, r *http.Request, userID, guid string) (*bongov1.Contact, bool) {
	g, err := h.contacts.Get(userID, guid)
	if err != nil || !store.IsContactGroup(g) {
		bookFail(w, r, http.StatusNotFound, "contacts.errGroupNotFound")
		return nil, false
	}
	return g, true
}

// contactGroupFailed answers a persistence failure with the translated message.
func contactGroupFailed(w http.ResponseWriter, r *http.Request, what, userID, guid string, err error) {
	slog.Warn("contact group write failed", "op", what, "user", userID, "guid", guid, "error", err)
	bookFail(w, r, http.StatusInternalServerError, "contacts.errGroupFailed")
}

// CreateContactGroup handles POST /contacts/groups: name and optional
// addressbook. The card is written in Apple's form, VERSION 3.0.
func (h *ContactsHandler) CreateContactGroup(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	name := store.CleanAddressBookText(r.FormValue("name"), maxContactGroupNameLen)
	if name == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errGroupNameRequired")
		return
	}
	book, err := h.formBook(userID, r.FormValue("addressbook"))
	if err != nil {
		answerFormBookError(w, r, err)
		return
	}

	guid := store.NewContactGUID()
	uid, _ := store.UUIDFromGUID(guid)
	raw, err := store.NewContactGroupCard(name, uid)
	if err != nil {
		contactGroupFailed(w, r, "create", userID, guid, err)
		return
	}
	g := &bongov1.Contact{Guid: guid, DisplayName: name, VcardRaw: raw, AddressbookId: book}
	if quotaExceeded(h.quota, nil, userID, store.ContactRecordSize(g)) {
		refuseQuota(w, r)
		return
	}
	if _, err := h.contacts.Create(userID, g); err != nil {
		contactGroupFailed(w, r, "create", userID, guid, err)
		return
	}
	bookRedirect(w, r, withFlash(contactGroupURL(guid), flashContactGroupCreated))
}

// RenameContactGroup handles POST /contacts/groups/{guid}/rename.
func (h *ContactsHandler) RenameContactGroup(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")
	name := store.CleanAddressBookText(r.FormValue("name"), maxContactGroupNameLen)

	unlock := h.groupLocks.lock(groupLockKey(userID, guid))
	defer unlock()
	g, ok := h.loadContactGroup(w, r, userID, guid)
	if !ok {
		return
	}
	if name == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errGroupNameRequired")
		return
	}
	raw, err := store.RenameContactGroup(g.VcardRaw, name)
	if err != nil {
		contactGroupFailed(w, r, "rename", userID, guid, err)
		return
	}
	g.VcardRaw, g.DisplayName = raw, name
	if err := h.contacts.Update(userID, guid, g); err != nil {
		contactGroupFailed(w, r, "rename", userID, guid, err)
		return
	}
	bookRedirect(w, r, withFlash(contactGroupURL(guid), flashContactGroupRenamed))
}

// DeleteContactGroup handles POST /contacts/groups/{guid}/delete. It deletes the
// group card only; the contacts in it are kept.
func (h *ContactsHandler) DeleteContactGroup(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	unlock := h.groupLocks.lock(groupLockKey(userID, guid))
	defer unlock()
	if _, ok := h.loadContactGroup(w, r, userID, guid); !ok {
		return
	}
	if err := h.contacts.Delete(userID, guid); err != nil {
		contactGroupFailed(w, r, "delete", userID, guid, err)
		return
	}
	bookRedirect(w, r, withFlash("/contacts", flashContactGroupDeleted))
}

// AddToContactGroup handles POST /contacts/{guid}/add-to-group: the contact in
// the URL joins the group named by the form's "group". Refused when the contact
// is not the user's (404), the group is not (404), the contact is a group or
// the group itself (400), it is filed in another address book (400) or the
// group is full (400).
func (h *ContactsHandler) AddToContactGroup(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	memberGUID := chi.URLParam(r, "guid")
	groupGUID := r.FormValue("group")

	member, err := h.contacts.Get(userID, memberGUID)
	if err != nil {
		bookFail(w, r, http.StatusNotFound, "contacts.errContactNotFound")
		return
	}

	unlock := h.groupLocks.lock(groupLockKey(userID, groupGUID))
	defer unlock()
	g, ok := h.loadContactGroup(w, r, userID, groupGUID)
	if !ok {
		return
	}
	if g.Guid == member.Guid || store.IsContactGroup(member) {
		bookFail(w, r, http.StatusBadRequest, "contacts.errGroupMember")
		return
	}
	if store.ContactBook(member) != store.ContactBook(g) {
		bookFail(w, r, http.StatusBadRequest, "contacts.errGroupBook")
		return
	}

	// The reference is the member's UID. A card with none gets one first, so the
	// reference stays valid whatever name a client files the card under.
	raw := member.VcardRaw
	if len(raw) == 0 {
		syncQuickFieldsToVCard(member)
		raw = member.VcardRaw
	}
	uid, ok2 := store.UUIDFromGUID(member.Guid)
	if !ok2 {
		uid = store.NewContactUID()
	}
	newRaw, gotUID, changed, err := store.EnsureContactUID(raw, uid)
	if err != nil {
		contactGroupFailed(w, r, "add-member", userID, groupGUID, err)
		return
	}
	value := store.ContactMemberValue(gotUID)
	if value == "" {
		// EnsureContactUID never returns an unsafe UID; this keeps a MEMBER value
		// that could start another property from ever being written.
		contactGroupFailed(w, r, "add-member", userID, groupGUID, errors.New("unusable member uid"))
		return
	}
	ref := store.ContactMemberRef(value)

	_, members := store.ContactGroupInfo(g)
	already := false
	for _, m := range members {
		if m == ref {
			already = true
			break
		}
	}
	if !already && len(members) >= store.MaxContactGroupMembers {
		bookFail(w, r, http.StatusBadRequest, "contacts.errGroupFull")
		return
	}

	if changed {
		member.VcardRaw = newRaw
		if err := h.contacts.Update(userID, member.Guid, member); err != nil {
			contactGroupFailed(w, r, "add-member-uid", userID, member.Guid, err)
			return
		}
	}
	if !already {
		groupRaw, err := store.AddContactGroupMember(g.VcardRaw, value)
		if err != nil {
			contactGroupFailed(w, r, "add-member", userID, groupGUID, err)
			return
		}
		g.VcardRaw = groupRaw
		if err := h.contacts.Update(userID, groupGUID, g); err != nil {
			if errors.Is(err, store.ErrContactGroupTooLarge) {
				bookFail(w, r, http.StatusBadRequest, "contacts.errGroupFull")
				return
			}
			contactGroupFailed(w, r, "add-member", userID, groupGUID, err)
			return
		}
	}
	bookRedirect(w, r, withFlash(contactGroupURL(groupGUID), flashContactGroupMemberAdded))
}

// RemoveContactGroupMember handles POST /contacts/groups/{guid}/members/remove:
// the form's "member" is the reference to drop, as the group page lists it.
func (h *ContactsHandler) RemoveContactGroupMember(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")
	ref := store.ContactMemberRef(r.FormValue("member"))

	unlock := h.groupLocks.lock(groupLockKey(userID, guid))
	defer unlock()
	g, ok := h.loadContactGroup(w, r, userID, guid)
	if !ok {
		return
	}
	if ref == "" {
		bookFail(w, r, http.StatusBadRequest, "contacts.errGroupMember")
		return
	}
	raw, changed, err := store.RemoveContactGroupMember(g.VcardRaw, ref)
	if err != nil {
		contactGroupFailed(w, r, "remove-member", userID, guid, err)
		return
	}
	if changed {
		g.VcardRaw = raw
		if err := h.contacts.Update(userID, guid, g); err != nil {
			contactGroupFailed(w, r, "remove-member", userID, guid, err)
			return
		}
	}
	bookRedirect(w, r, withFlash(contactGroupURL(guid), flashContactGroupMemberRemoved))
}

// dropFromGroups removes a deleted contact from the groups of its book that
// list it. It is best-effort and logged: the reference is already inert (an
// unresolved member drops out at read time), this only keeps the card tidy.
func (h *ContactsHandler) dropFromGroups(userID string, c *bongov1.Contact) {
	groups, err := h.contacts.GroupsReferencing(userID, c)
	if err != nil {
		slog.Warn("contact groups: listing groups of a deleted contact failed", "user", userID, "guid", c.Guid, "error", err)
		return
	}
	ref := store.ContactIdentity(c)
	for _, g := range groups {
		func() {
			unlock := h.groupLocks.lock(groupLockKey(userID, g.Guid))
			defer unlock()
			// Read again under the lock: another edit may have landed.
			cur, err := h.contacts.Get(userID, g.Guid)
			if err != nil {
				slog.Warn("contact groups: reading a group to drop a deleted contact failed", "user", userID, "group", g.Guid, "error", err)
				return
			}
			raw, changed, err := store.RemoveContactGroupMember(cur.VcardRaw, ref)
			if err != nil {
				slog.Warn("contact groups: dropping a deleted contact from a group failed", "user", userID, "group", g.Guid, "error", err)
				return
			}
			if !changed {
				return
			}
			cur.VcardRaw = raw
			if err := h.contacts.Update(userID, g.Guid, cur); err != nil {
				slog.Warn("contact groups: removing a deleted contact failed", "user", userID, "group", g.Guid, "error", err)
			}
		}()
	}
}

// groupView resolves a group's members for its page.
func (h *ContactsHandler) groupView(userID string, g *bongov1.Contact) (*templates.ContactGroupView, error) {
	res, err := h.contacts.ResolveContactGroup(userID, g)
	if err != nil {
		return nil, err
	}
	_, members := store.ContactGroupInfo(g)
	v := &templates.ContactGroupView{Nested: res.Nested, Full: len(members) >= store.MaxContactGroupMembers}
	for _, m := range res.Members {
		mv := templates.ContactGroupMemberView{Ref: m.Ref, Email: m.Address}
		if m.Contact != nil {
			mv.Guid = m.Contact.Guid
			mv.Name = m.Contact.DisplayName
			if mv.Name == "" {
				mv.Name = m.Contact.Email
			}
			mv.Email = m.Contact.Email
		}
		v.Members = append(v.Members, mv)
	}
	return v, nil
}

// cardView is the extra data a contact's detail card needs.
func (h *ContactsHandler) cardView(userID string, c *bongov1.Contact) (templates.ContactCardView, error) {
	if store.IsContactGroup(c) {
		gv, err := h.groupView(userID, c)
		return templates.ContactCardView{Group: gv}, err
	}
	groups, err := h.contacts.ListContactGroups(userID, store.ContactBook(c))
	return templates.ContactCardView{Groups: groups, IsSelf: h.contacts.IsSelfContact(userID, c.Guid)}, err
}
