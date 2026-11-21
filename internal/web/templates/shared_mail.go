package templates

import (
	"context"
	"net/url"
	"strings"
	"sync"
)

// SharedMailFolder is one entry of the "Shared with me" sidebar section: a
// folder another user shared with the viewer.
type SharedMailFolder struct {
	// OwnerLabel is the owner's primary address, the group heading.
	OwnerLabel string
	OwnerID    string
	// ID is the folder's stable MailboxMeta.id, which the URL carries.
	ID   string
	Name string
	// Unread is meaningful only when Counted: the count is computed only for a
	// viewer holding the read right, so a lookup-only grant shows no number.
	Unread  int
	Counted bool
}

// SharedSidebarData is everything the sidebar section needs.
type SharedSidebarData struct {
	Folders []SharedMailFolder
	// PendingInvitations is the number of share invitations waiting on /shares.
	PendingInvitations int
}

// sharedSidebarState holds a loader that runs at most once per request.
type sharedSidebarState struct {
	load func() SharedSidebarData
	once sync.Once
	data SharedSidebarData
}

type sharedSidebarKey struct{}

func sharedState(ctx context.Context) *sharedSidebarState {
	s, _ := ctx.Value(sharedSidebarKey{}).(*sharedSidebarState)
	return s
}

// WithSharedSidebar installs a loader the sidebar calls when it renders. The
// loader runs at most once per request and nothing is cached beyond it, so a
// revoked share is gone from the next page.
func WithSharedSidebar(ctx context.Context, load func() SharedSidebarData) context.Context {
	return context.WithValue(ctx, sharedSidebarKey{}, &sharedSidebarState{load: load})
}

// WithSharedMailFolders sets the shared folders directly, for a handler that
// already has them.
func WithSharedMailFolders(ctx context.Context, folders []SharedMailFolder) context.Context {
	return WithSharedSidebar(ctx, func() SharedSidebarData {
		return SharedSidebarData{Folders: folders, PendingInvitations: pendingShareCount(ctx)}
	})
}

type sharedSelectedKey struct{}

type sharedSelection struct{ owner, id string }

// WithSharedSelected marks the shared folder the page shows, so the sidebar
// highlights it. It never selects an own folder.
func WithSharedSelected(ctx context.Context, ownerID, folderID string) context.Context {
	return context.WithValue(ctx, sharedSelectedKey{}, sharedSelection{ownerID, folderID})
}

func (s *sharedSidebarState) dataOnce() SharedSidebarData {
	s.once.Do(func() {
		if s.load != nil {
			s.data = s.load()
		}
	})
	return s.data
}

// sharedSidebar returns the data for the section, loading it on first use.
func sharedSidebar(ctx context.Context) SharedSidebarData {
	var d SharedSidebarData
	if st := sharedState(ctx); st != nil {
		d = st.dataOnce()
	}
	if d.PendingInvitations == 0 {
		d.PendingInvitations = pendingShareCount(ctx)
	}
	return d
}

func sharedSelected(ctx context.Context, f SharedMailFolder) bool {
	sel, _ := ctx.Value(sharedSelectedKey{}).(sharedSelection)
	return sel.id != "" && sel.owner == f.OwnerID && sel.id == f.ID
}

// sharedOwnerGroup is one owner's folders in the sidebar.
type sharedOwnerGroup struct {
	Label   string
	Folders []SharedMailFolder
}

// groupSharedByOwner groups folders by owner, ordered by label then owner id,
// with the folders of each owner ordered by name.
func groupSharedByOwner(folders []SharedMailFolder) []sharedOwnerGroup {
	var groups []sharedOwnerGroup
	idx := map[string]int{}
	for _, f := range folders {
		i, ok := idx[f.OwnerID]
		if !ok {
			i = len(groups)
			idx[f.OwnerID] = i
			groups = append(groups, sharedOwnerGroup{Label: f.OwnerLabel})
		}
		groups[i].Folders = append(groups[i].Folders, f)
	}
	return groups
}

// SharedFolderView names the shared folder a page shows. Rights are the
// viewer's RFC 4314 letters, re-read for this request.
type SharedFolderView struct {
	OwnerID    string
	OwnerEmail string
	ID         string
	Name       string
	Rights     string
}

// BaseURL is the folder's URL on the grantee side.
func (v SharedFolderView) BaseURL() string {
	return SharedFolderURL(v.OwnerID, v.ID)
}

// SharedFolderURL is the grantee-side URL of a shared folder.
func SharedFolderURL(ownerID, folderID string) string {
	return "/shared/mail/" + url.PathEscape(ownerID) + "/" + url.PathEscape(folderID)
}

func sharedHas(rights string, letters string) bool {
	for _, l := range letters {
		if !strings.ContainsRune(rights, l) {
			return false
		}
	}
	return letters != ""
}

// SharedCanSeen reports whether the rights allow changing \Seen.
func SharedCanSeen(rights string) bool { return sharedHas(rights, "s") }

// SharedCanFlag reports whether the rights allow \Flagged and \Answered.
func SharedCanFlag(rights string) bool { return sharedHas(rights, "w") }

// SharedCanDelete reports whether the rights allow permanent deletion, which
// sets \Deleted (t) and expunges (e).
func SharedCanDelete(rights string) bool { return sharedHas(rights, "te") }

// SharedCanRead reports whether the rights allow reading messages.
func SharedCanRead(rights string) bool { return sharedHas(rights, "r") }

// SharedMessagePage is a message shown in a shared folder.
type SharedMessagePage struct {
	Folder  SharedFolderView
	Message MessageDetail
}
