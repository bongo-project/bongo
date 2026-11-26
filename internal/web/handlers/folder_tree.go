package handlers

import (
	"slices"
	"strconv"
	"strings"

	"github.com/bongo-project/bongo/internal/web/templates"
)

// folderTreeBuilder holds the work of one buildFolderTree call.
type folderTreeBuilder struct {
	folders  map[string]*templates.FolderShareFolder
	children map[string][]string
	seq      int
}

// buildFolderTree arranges folders by their "/" hierarchy. A folder whose
// parent is missing gets placeholder ancestors (Folder nil, no actions), at any
// depth. Siblings sort by path in byte order. Every input name appears exactly
// once as a real node, including legacy names with empty segments.
func buildFolderTree(folders []templates.FolderShareFolder) []templates.FolderTreeNode {
	b := &folderTreeBuilder{
		folders:  make(map[string]*templates.FolderShareFolder, len(folders)),
		children: map[string][]string{},
	}
	known := map[string]bool{}
	var link func(path string)
	link = func(path string) {
		if known[path] {
			return
		}
		known[path] = true
		parent := folderParent(path)
		b.children[parent] = append(b.children[parent], path)
		if parent != "" {
			link(parent)
		}
	}
	for i := range folders {
		f := &folders[i]
		if _, dup := b.folders[f.Name]; dup {
			continue
		}
		b.folders[f.Name] = f
		link(f.Name)
	}
	return b.nodes("", 0)
}

// folderParent is the path up to the last "/", or "" for a top-level name.
func folderParent(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	return path[:i]
}

func (b *folderTreeBuilder) nodes(parent string, depth int) []templates.FolderTreeNode {
	paths := b.children[parent]
	if len(paths) == 0 {
		return nil
	}
	slices.Sort(paths)
	out := make([]templates.FolderTreeNode, 0, len(paths))
	for _, p := range paths {
		n := templates.FolderTreeNode{
			Path:     p,
			Label:    p[strings.LastIndex(p, "/")+1:],
			Depth:    depth,
			Folder:   b.folders[p],
			Expanded: depth == 0,
		}
		if len(b.children[p]) > 0 {
			// Pre-order counter: never derived from the name.
			b.seq++
			n.ChildrenID = "folder-children-" + strconv.Itoa(b.seq)
			n.Children = b.nodes(p, depth+1)
		}
		out = append(out, n)
	}
	return out
}
