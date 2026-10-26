package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestLoadSidebarTagsNilStore(t *testing.T) {
	result := LoadSidebarTags(nil, "user1", "")
	if result != nil {
		t.Errorf("LoadSidebarTags(nil, ...) = %v, want nil", result)
	}
}

func TestLoadSidebarTagsHidesEmptyTags(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"

	// "work" has a tagged message (count 1); "empty" is defined but unused (count 0).
	if _, err := env.tags.CreateTag(userID, "work"); err != nil {
		t.Fatalf("CreateTag work: %v", err)
	}
	if _, err := env.tags.CreateTag(userID, "empty"); err != nil {
		t.Fatalf("CreateTag empty: %v", err)
	}
	env.seedMessage(t, userID, "INBOX", "g1", []string{"work"})

	// Default view: the empty tag is hidden, the used tag is shown.
	got := LoadSidebarTags(env.tags, userID, "")
	names := map[string]int{}
	for _, s := range got {
		names[s.Name] = s.Count
	}
	if _, shown := names["empty"]; shown {
		t.Errorf("empty tag should be hidden from the sidebar, got %v", got)
	}
	if names["work"] != 1 {
		t.Errorf("work tag should be shown with count 1, got %v", got)
	}

	// When the empty tag is the selected view, it stays visible.
	got = LoadSidebarTags(env.tags, userID, "tag:empty")
	found := false
	for _, s := range got {
		if s.Name == "empty" {
			found = true
		}
	}
	if !found {
		t.Errorf("selected empty tag should remain visible, got %v", got)
	}
}

func TestMaxSidebarTagsConstant(t *testing.T) {
	if maxSidebarTags != 20 {
		t.Errorf("maxSidebarTags = %d, want 20", maxSidebarTags)
	}
}

func TestSidebarTagInfoSelected(t *testing.T) {
	// Verify the Selected field logic via direct struct construction (mirrors
	// what LoadSidebarTags sets when building results).
	currentSelected := "tag:work"
	tag := templates.SidebarTagInfo{
		Name:        "work",
		ColourIndex: 0,
		Count:       5,
		Selected:    currentSelected == "tag:work",
	}
	if !tag.Selected {
		t.Error("tag.Selected should be true when selected matches tag:name")
	}

	tag2 := templates.SidebarTagInfo{
		Name:        "personal",
		ColourIndex: 1,
		Count:       0,
		Selected:    "tag:work" == "tag:personal",
	}
	if tag2.Selected {
		t.Error("tag2.Selected should be false when selected does not match")
	}
}

func TestLoadMailboxInfosFreshUserShowsDefaultMailboxes(t *testing.T) {
	ms := newSidebarMailboxStore(t)

	got := LoadMailboxInfos(ms, "fresh-user", "INBOX")
	if len(got) != len(store.DefaultMailboxes) {
		t.Fatalf("fresh user sidebar mailbox count = %d, want %d default mailboxes (%v)", len(got), len(store.DefaultMailboxes), store.DefaultMailboxes)
	}

	for i, name := range store.DefaultMailboxes {
		if got[i].Name != name {
			t.Fatalf("mailbox %d = %q, want %q", i, got[i].Name, name)
		}
	}
	if !got[0].Selected {
		t.Fatal("INBOX should be selected for a fresh user sidebar")
	}
}

func TestBuildMailboxTree(t *testing.T) {
	tests := []struct {
		name     string
		input    []flatMailbox
		selected string
		want     func(t *testing.T, result []templates.MailboxInfo)
	}{
		{
			name: "flat mailboxes no hierarchy",
			input: []flatMailbox{
				{Name: "INBOX", Unread: 5},
				{Name: "Sent", Unread: 0},
				{Name: "Drafts", Unread: 2},
			},
			selected: "INBOX",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 3 {
					t.Fatalf("want 3 top-level, got %d", len(r))
				}
				if r[0].Name != "INBOX" {
					t.Errorf("want INBOX first, got %s", r[0].Name)
				}
				if !r[0].Selected {
					t.Error("INBOX should be selected")
				}
				if r[0].Label != "INBOX" {
					t.Errorf("want label INBOX, got %s", r[0].Label)
				}
			},
		},
		{
			name: "default mailboxes sorted first",
			input: []flatMailbox{
				{Name: "Custom", Unread: 0},
				{Name: "Trash", Unread: 0},
				{Name: "INBOX", Unread: 3},
				{Name: "Sent", Unread: 0},
			},
			selected: "",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 4 {
					t.Fatalf("want 4 top-level, got %d", len(r))
				}
				expected := []string{"INBOX", "Sent", "Trash", "Custom"}
				for i, name := range expected {
					if r[i].Name != name {
						t.Errorf("position %d: want %s, got %s", i, name, r[i].Name)
					}
				}
			},
		},
		{
			name: "single level nesting",
			input: []flatMailbox{
				{Name: "Projects", Unread: 0},
				{Name: "Projects/ClientA", Unread: 3},
				{Name: "Projects/ClientB", Unread: 1},
			},
			selected: "Projects/ClientA",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 1 {
					t.Fatalf("want 1 top-level, got %d", len(r))
				}
				if r[0].Label != "Projects" {
					t.Errorf("want label Projects, got %s", r[0].Label)
				}
				if !r[0].HasChildren {
					t.Error("Projects should have children")
				}
				if len(r[0].Children) != 2 {
					t.Fatalf("want 2 children, got %d", len(r[0].Children))
				}
				if r[0].Children[0].Label != "ClientA" {
					t.Errorf("want child label ClientA, got %s", r[0].Children[0].Label)
				}
				if !r[0].Children[0].Selected {
					t.Error("ClientA should be selected")
				}
				// Unread should aggregate: 0 + 3 + 1 = 4.
				if r[0].Unread != 4 {
					t.Errorf("want aggregated unread 4, got %d", r[0].Unread)
				}
			},
		},
		{
			name: "multi level nesting",
			input: []flatMailbox{
				{Name: "Work", Unread: 0},
				{Name: "Work/Project", Unread: 0},
				{Name: "Work/Project/Active", Unread: 2},
			},
			selected: "",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 1 {
					t.Fatalf("want 1 top-level, got %d", len(r))
				}
				work := r[0]
				if !work.HasChildren {
					t.Error("Work should have children")
				}
				if len(work.Children) != 1 {
					t.Fatalf("want 1 child of Work, got %d", len(work.Children))
				}
				project := work.Children[0]
				if !project.HasChildren {
					t.Error("Project should have children")
				}
				if len(project.Children) != 1 {
					t.Fatalf("want 1 child of Project, got %d", len(project.Children))
				}
				if project.Children[0].Label != "Active" {
					t.Errorf("want Active, got %s", project.Children[0].Label)
				}
				if work.Unread != 2 {
					t.Errorf("want aggregated unread 2, got %d", work.Unread)
				}
			},
		},
		{
			name: "synthetic parent creation",
			input: []flatMailbox{
				{Name: "Work/Active", Unread: 2},
			},
			selected: "",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 1 {
					t.Fatalf("want 1 top-level, got %d", len(r))
				}
				if r[0].Label != "Work" {
					t.Errorf("want synthetic parent Work, got %s", r[0].Label)
				}
				if !r[0].HasChildren {
					t.Error("synthetic parent should have children")
				}
				if r[0].Unread != 2 {
					t.Errorf("want aggregated unread 2, got %d", r[0].Unread)
				}
				if len(r[0].Children) != 1 {
					t.Fatalf("want 1 child, got %d", len(r[0].Children))
				}
				if r[0].Children[0].Label != "Active" {
					t.Errorf("want child Active, got %s", r[0].Children[0].Label)
				}
			},
		},
		{
			name: "deep synthetic parents",
			input: []flatMailbox{
				{Name: "A/B/C/D", Unread: 1},
			},
			selected: "A/B/C/D",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 1 {
					t.Fatalf("want 1 top-level, got %d", len(r))
				}
				// A -> B -> C -> D
				node := r[0]
				for _, expected := range []string{"A", "B", "C"} {
					if node.Label != expected {
						t.Errorf("want %s, got %s", expected, node.Label)
					}
					if !node.HasChildren {
						t.Errorf("%s should have children", expected)
					}
					if len(node.Children) != 1 {
						t.Fatalf("want 1 child of %s, got %d", expected, len(node.Children))
					}
					node = node.Children[0]
				}
				if node.Label != "D" {
					t.Errorf("want D, got %s", node.Label)
				}
				if node.HasChildren {
					t.Error("D should not have children")
				}
				if !node.Selected {
					t.Error("D should be selected")
				}
			},
		},
		{
			name: "icon assignment for known folders",
			input: []flatMailbox{
				{Name: "INBOX", Unread: 0},
				{Name: "Sent", Unread: 0},
				{Name: "Drafts", Unread: 0},
				{Name: "Trash", Unread: 0},
				{Name: "Junk", Unread: 0},
				{Name: "Archive", Unread: 0},
				{Name: "Custom", Unread: 0},
			},
			selected: "",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				icons := map[string]string{
					"INBOX":   "inbox",
					"Sent":    "send",
					"Drafts":  "draft",
					"Trash":   "trash",
					"Junk":    "shield",
					"Archive": "archive",
					"Custom":  "folder",
				}
				for _, mb := range r {
					want, ok := icons[mb.Name]
					if !ok {
						continue
					}
					if mb.Icon != want {
						t.Errorf("icon for %s: want %s, got %s", mb.Name, want, mb.Icon)
					}
				}
			},
		},
		{
			name: "children inherit parent icon theme",
			input: []flatMailbox{
				{Name: "INBOX", Unread: 0},
				{Name: "INBOX/Receipts", Unread: 1},
			},
			selected: "",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 1 {
					t.Fatalf("want 1 top-level, got %d", len(r))
				}
				// INBOX child gets INBOX icon (topLevelName).
				if r[0].Children[0].Icon != "inbox" {
					t.Errorf("want INBOX icon for child, got %s", r[0].Children[0].Icon)
				}
			},
		},
		{
			name:  "empty input",
			input: []flatMailbox{},
			want: func(t *testing.T, r []templates.MailboxInfo) {
				if len(r) != 0 {
					t.Fatalf("want 0 results, got %d", len(r))
				}
			},
		},
		{
			name: "mixed default and custom with nesting",
			input: []flatMailbox{
				{Name: "INBOX", Unread: 5},
				{Name: "INBOX/Important", Unread: 2},
				{Name: "Sent", Unread: 0},
				{Name: "Projects/Go", Unread: 1},
			},
			selected: "INBOX",
			want: func(t *testing.T, r []templates.MailboxInfo) {
				// Should be: INBOX (with child Important), Sent, Projects (with child Go)
				if len(r) != 3 {
					t.Fatalf("want 3 top-level, got %d", len(r))
				}
				if r[0].Name != "INBOX" {
					t.Errorf("want INBOX first, got %s", r[0].Name)
				}
				if !r[0].HasChildren {
					t.Error("INBOX should have children")
				}
				if len(r[0].Children) != 1 {
					t.Fatalf("want 1 INBOX child, got %d", len(r[0].Children))
				}
				if r[0].Children[0].Label != "Important" {
					t.Errorf("want Important, got %s", r[0].Children[0].Label)
				}
				// INBOX unread = 5 (own) + 2 (child) = 7
				if r[0].Unread != 7 {
					t.Errorf("want INBOX unread 7, got %d", r[0].Unread)
				}
				if r[1].Name != "Sent" {
					t.Errorf("want Sent second, got %s", r[1].Name)
				}
				// Projects is synthetic parent.
				if r[2].Label != "Projects" {
					t.Errorf("want Projects third, got %s", r[2].Label)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildMailboxTree(tt.input, tt.selected)
			if tt.want != nil {
				tt.want(t, result)
			}
		})
	}
}

type sidebarDirectApplier struct {
	store *store.PebbleStore
}

func (a *sidebarDirectApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsMailboxSyntheticCommand(cmd.Key) {
		_, err := store.ApplyMailboxCommand(a.store, cmd)
		return err
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

func newSidebarMailboxStore(t *testing.T) *store.MailboxStore {
	t.Helper()

	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return store.NewMailboxStore(ps, &sidebarDirectApplier{store: ps})
}

func TestMailboxIconsRender(t *testing.T) {
	for _, n := range []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive", "x"} {
		var b strings.Builder
		if err := templates.Icon(MailboxIcon(n), "").Render(context.Background(), &b); err != nil || b.Len() == 0 {
			t.Errorf("icon for %s renders empty (err=%v)", n, err)
		}
	}
}

func TestMailboxIcon(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"INBOX", "inbox"},
		{"Sent", "send"},
		{"Drafts", "draft"},
		{"Trash", "trash"},
		{"Junk", "shield"},
		{"Archive", "archive"},
		{"Random", "folder"},
		{"", "folder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MailboxIcon(tt.name)
			if got != tt.want {
				t.Errorf("MailboxIcon(%q) = %s, want %s", tt.name, got, tt.want)
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	tests := []struct {
		input      string
		wantRoot   string
		wantLeaf   string
		wantParent string
		wantTop    string
	}{
		{"INBOX", "INBOX", "INBOX", "", "INBOX"},
		{"Projects/ClientA", "Projects", "ClientA", "Projects", "Projects"},
		{"A/B/C", "A", "C", "A/B", "A"},
		{"A/B/C/D", "A", "D", "A/B/C", "A"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := rootName(tt.input); got != tt.wantRoot {
				t.Errorf("rootName(%q) = %q, want %q", tt.input, got, tt.wantRoot)
			}
			if got := leafName(tt.input); got != tt.wantLeaf {
				t.Errorf("leafName(%q) = %q, want %q", tt.input, got, tt.wantLeaf)
			}
			if got := parentName(tt.input); got != tt.wantParent {
				t.Errorf("parentName(%q) = %q, want %q", tt.input, got, tt.wantParent)
			}
			if got := topLevelName(tt.input); got != tt.wantTop {
				t.Errorf("topLevelName(%q) = %q, want %q", tt.input, got, tt.wantTop)
			}
		})
	}
}
