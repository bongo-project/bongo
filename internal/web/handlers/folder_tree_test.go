package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/web/templates"
)

func treeFolders(names ...string) []templates.FolderShareFolder {
	out := make([]templates.FolderShareFolder, len(names))
	for i, n := range names {
		out[i] = templates.FolderShareFolder{Name: n}
	}
	return out
}

// shape renders a tree as "path[*]" with * for a placeholder, nested in ().
func shape(nodes []templates.FolderTreeNode) string {
	var parts []string
	for _, n := range nodes {
		s := n.Path
		if n.Folder == nil {
			s += "*"
		}
		if len(n.Children) > 0 {
			s += "(" + shape(n.Children) + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func TestBuildFolderTree(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"flat", []string{"B", "A", "C"}, "A B C"},
		{"deep", []string{"A", "A/b", "A/b/c", "A/b/c/d"}, "A(A/b(A/b/c(A/b/c/d)))"},
		{"missing parent depth 1", []string{"C/z"}, "C*(C/z)"},
		{"missing parent depth 2", []string{"A", "A/b/c"}, "A(A/b*(A/b/c))"},
		{"missing both", []string{"A/b/c"}, "A*(A/b*(A/b/c))"},
		{"child before parent", []string{"A/x", "A"}, "A(A/x)"},
		{"A-b vs A/x", []string{"A/x", "A-b", "A"}, "A(A/x) A-b"},
		{"INBOX child", []string{"INBOX", "INBOX/x"}, "INBOX(INBOX/x)"},
		{"unicode", []string{"Überblick", "Überblick/Café", "日本"}, "Überblick(Überblick/Café) 日本"},
		{"legacy double slash", []string{"A//b"}, "A*(A/*(A//b))"},
		{"legacy leading slash", []string{"/A"}, "/A"},
		{"legacy trailing slash", []string{"A/"}, "A*(A/)"},
		{"duplicate input", []string{"A", "A"}, "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shape(buildFolderTree(treeFolders(tc.in...)))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildFolderTreeAttributes(t *testing.T) {
	tree := buildFolderTree(treeFolders("A", "A/b", "A/b/c", "B", "C/z"))
	seen := map[string]bool{}
	var walk func(ns []templates.FolderTreeNode)
	walk = func(ns []templates.FolderTreeNode) {
		for _, n := range ns {
			if n.Expanded != (n.Depth == 0) {
				t.Errorf("%s: Expanded %v at depth %d", n.Path, n.Expanded, n.Depth)
			}
			if (n.ChildrenID != "") != (len(n.Children) > 0) {
				t.Errorf("%s: ChildrenID %q with %d children", n.Path, n.ChildrenID, len(n.Children))
			}
			if n.ChildrenID != "" {
				if seen[n.ChildrenID] {
					t.Errorf("duplicate id %s", n.ChildrenID)
				}
				seen[n.ChildrenID] = true
			}
			walk(n.Children)
		}
	}
	walk(tree)
	if len(seen) != 3 {
		t.Errorf("want 3 ids (A, A/b, C), got %d: %v", len(seen), seen)
	}
	if tree[0].Label != "A" || tree[0].Children[0].Label != "b" || tree[0].Children[0].Depth != 1 {
		t.Errorf("labels or depth wrong: %+v", tree[0].Children[0])
	}
}

func TestBuildFolderTreeInvariant(t *testing.T) {
	in := []string{"A/x/y", "B", "C/z", "A", "A//b", "/Q", "R/", "A-b", "Ü/ö", "INBOX/Sub/Deep"}
	tree := buildFolderTree(treeFolders(in...))
	real := map[string]int{}
	placeholders := map[string]bool{}
	var walk func(ns []templates.FolderTreeNode)
	walk = func(ns []templates.FolderTreeNode) {
		for _, n := range ns {
			if n.Folder != nil {
				real[n.Path]++
			} else {
				placeholders[n.Path] = true
			}
			walk(n.Children)
		}
	}
	walk(tree)
	want := map[string]bool{}
	for _, n := range in {
		want[n] = true
		if real[n] != 1 {
			t.Errorf("%q appears %d times", n, real[n])
		}
	}
	if len(real) != len(in) {
		t.Errorf("%d real nodes for %d inputs", len(real), len(in))
	}
	expected := map[string]bool{}
	for _, n := range in {
		for p := folderParent(n); p != ""; p = folderParent(p) {
			if !want[p] {
				expected[p] = true
			}
		}
	}
	if fmt.Sprint(placeholders) != fmt.Sprint(expected) {
		t.Errorf("placeholders %v, want %v", placeholders, expected)
	}
}
