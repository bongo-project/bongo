package handlers

import (
	"sort"
	"strings"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/templates"
)

// maxSidebarTags is the maximum number of tags shown in the sidebar.
// Tags beyond this limit are reachable via the "Show all" link.
const maxSidebarTags = 20

// LoadSidebarTags converts store.TagWithCount entries into SidebarTagInfo
// slices suitable for rendering. selected is the currently active view key
// (e.g. "tag:work"). At most maxSidebarTags entries are returned.
func LoadSidebarTags(ts *store.TagStore, userID, selected string) []templates.SidebarTagInfo {
	if ts == nil {
		return nil
	}
	twcs, err := ts.ListTags(userID)
	if err != nil || len(twcs) == 0 {
		return nil
	}

	limit := len(twcs)
	if limit > maxSidebarTags {
		limit = maxSidebarTags
	}

	result := make([]templates.SidebarTagInfo, 0, limit)
	for i := 0; i < limit; i++ {
		twc := twcs[i]
		result = append(result, templates.SidebarTagInfo{
			Name:     twc.Tag.Name,
			Colour:   templates.TagDotColour(twc.Tag.ColourIndex),
			Count:    twc.Count,
			Selected: selected == "tag:"+twc.Tag.Name,
		})
	}
	return result
}

// MailboxIcon returns a Unicode HTML entity for the given mailbox name.
func MailboxIcon(name string) string {
	switch name {
	case "INBOX":
		return "&#128229;" // inbox tray
	case "Sent":
		return "&#9992;" // airplane
	case "Drafts":
		return "&#9998;" // pencil
	case "Trash":
		return "&#128465;" // wastebasket
	case "Junk":
		return "&#9888;" // warning
	case "Archive":
		return "&#128451;" // file cabinet
	default:
		return "&#128193;" // folder
	}
}

// flatMailbox holds intermediate data before tree building.
type flatMailbox struct {
	Name   string
	Unread int
}

// BuildMailboxTree converts a flat list of mailboxes into a tree based on
// the '/' hierarchy delimiter. Default mailboxes are sorted first in the
// standard order, followed by custom mailboxes alphabetically.
func BuildMailboxTree(flats []flatMailbox, selected string) []templates.MailboxInfo {
	// Sort: default mailboxes first (in order), then alphabetical.
	defaultOrder := make(map[string]int, len(store.DefaultMailboxes))
	for i, name := range store.DefaultMailboxes {
		defaultOrder[name] = i
	}

	sort.SliceStable(flats, func(i, j int) bool {
		iRoot := rootName(flats[i].Name)
		jRoot := rootName(flats[j].Name)
		iIdx, iDefault := defaultOrder[iRoot]
		jIdx, jDefault := defaultOrder[jRoot]

		// Both default: sort by default order, then full path.
		if iDefault && jDefault {
			if iIdx != jIdx {
				return iIdx < jIdx
			}
			return flats[i].Name < flats[j].Name
		}
		// One default, one not: default first.
		if iDefault {
			return true
		}
		if jDefault {
			return false
		}
		// Neither default: alphabetical.
		return flats[i].Name < flats[j].Name
	})

	// Build tree using a map for O(1) parent lookup.
	nodeMap := make(map[string]*templates.MailboxInfo)
	var roots []*templates.MailboxInfo

	for _, f := range flats {
		node := &templates.MailboxInfo{
			Name:     f.Name,
			Label:    leafName(f.Name),
			Unread:   f.Unread,
			Selected: f.Name == selected,
			Icon:     MailboxIcon(topLevelName(f.Name)),
		}
		nodeMap[f.Name] = node

		parentPath := parentName(f.Name)
		if parentPath == "" {
			// Top-level mailbox.
			roots = append(roots, node)
		} else {
			// Find or create parent chain.
			parent := ensureParent(parentPath, nodeMap, &roots, selected)
			parent.Children = append(parent.Children, *node)
			parent.HasChildren = true
			// Update nodeMap to point to the child within the parent's slice
			// so further nested children can find it.
			nodeMap[f.Name] = &parent.Children[len(parent.Children)-1]
		}
	}

	// Aggregate unread counts from children to parents.
	for i := range roots {
		aggregateUnread(roots[i])
	}

	// Convert to value slice.
	result := make([]templates.MailboxInfo, len(roots))
	for i, r := range roots {
		result[i] = *r
	}
	return result
}

// ensureParent finds or creates the parent node (and its ancestors) in the tree.
func ensureParent(path string, nodeMap map[string]*templates.MailboxInfo, roots *[]*templates.MailboxInfo, selected string) *templates.MailboxInfo {
	if existing, ok := nodeMap[path]; ok {
		return existing
	}

	// Create synthetic parent.
	node := &templates.MailboxInfo{
		Name:     path,
		Label:    leafName(path),
		Selected: path == selected,
		Icon:     MailboxIcon(topLevelName(path)),
	}

	parentPath := parentName(path)
	if parentPath == "" {
		*roots = append(*roots, node)
		nodeMap[path] = node
		return node
	}

	parent := ensureParent(parentPath, nodeMap, roots, selected)
	parent.Children = append(parent.Children, *node)
	parent.HasChildren = true
	created := &parent.Children[len(parent.Children)-1]
	nodeMap[path] = created
	return created
}

// aggregateUnread adds child unread counts to parent nodes.
func aggregateUnread(node *templates.MailboxInfo) int {
	total := node.Unread
	for i := range node.Children {
		total += aggregateUnread(&node.Children[i])
	}
	node.Unread = total
	return total
}

// rootName returns the first segment of a path.
func rootName(name string) string {
	if idx := strings.IndexByte(name, '/'); idx >= 0 {
		return name[:idx]
	}
	return name
}

// leafName returns the last segment of a path.
func leafName(name string) string {
	if idx := strings.LastIndexByte(name, '/'); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// parentName returns the parent path, or "" for top-level names.
func parentName(name string) string {
	if idx := strings.LastIndexByte(name, '/'); idx >= 0 {
		return name[:idx]
	}
	return ""
}

// topLevelName returns the root-level name for icon assignment.
// For "INBOX/Receipts" it returns "INBOX".
func topLevelName(name string) string {
	if idx := strings.IndexByte(name, '/'); idx >= 0 {
		return name[:idx]
	}
	return name
}

// LoadMailboxInfos builds a tree of mailbox info for the sidebar.
// It is shared between MailHandler and ComposeHandler.
func LoadMailboxInfos(ms *store.MailboxStore, userID, selected string) []templates.MailboxInfo {
	mbs, _ := ms.ListMailboxes(userID)
	flats := make([]flatMailbox, 0, len(mbs))
	for _, mb := range mbs {
		_, _, unseen, _ := ms.CountMessages(userID, mb.Name)
		flats = append(flats, flatMailbox{
			Name:   mb.Name,
			Unread: int(unseen),
		})
	}
	return BuildMailboxTree(flats, selected)
}
