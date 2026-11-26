package js_test

import (
	"strings"
	"testing"
)

func TestFolderTreeComponent(t *testing.T) {
	js := readJS(t, "bongo-components.js")
	start := strings.Index(js, "Alpine.data('folderTree'")
	if start < 0 {
		t.Fatal("folderTree is missing")
	}
	end := strings.Index(js[start:], "let galSeq")
	if end < 0 {
		t.Fatal("galSeq marker is missing")
	}
	component := js[start : start+end]
	for _, want := range []string{
		"data-folder-tree-toggle", "data-count-one", "data-count-many", "aria-controls",
		"aria-expanded", "snapshot", "toggle(e)", "filter()", "clear()",
	} {
		if !strings.Contains(component, want) {
			t.Errorf("folderTree lacks %q", want)
		}
	}
	if strings.Contains(component, "this.$el") || !strings.Contains(component, "this.$root") {
		t.Error("folderTree must read the x-data root through this.$root: in the CSP build $el is the element carrying the directive (the search input)")
	}
	if strings.Contains(component, "innerHTML") || strings.Contains(component, "document.addEventListener") {
		t.Error("folderTree must not use innerHTML or document listeners")
	}
}
