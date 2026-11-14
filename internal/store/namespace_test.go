package store

import "testing"

func TestIMAPNamespacesReserves(t *testing.T) {
	ns := DefaultIMAPNamespaces()
	tests := []struct {
		name string
		want bool
	}{
		{"Shared", true},
		{"SHARED", true},
		{"shared/x", true},
		{"Shared/", true},
		{"Other Users", true},
		{"other users/bob", true},
		{"OTHER USERS", true},
		{"SharedFolder", false},
		{"Shared ", false},
		{"INBOX/Shared", false},
		{"Other Users2", false},
		{"", false},
		{"Share", false},
	}
	for _, tt := range tests {
		if got := ns.Reserves(tt.name); got != tt.want {
			t.Errorf("Reserves(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIMAPNamespacesCustomAndDefaults(t *testing.T) {
	ns := IMAPNamespaces{OtherUsers: "People", Shared: "Team"}
	if !ns.Reserves("team/x") || !ns.Reserves("PEOPLE") {
		t.Error("custom prefixes not reserved")
	}
	if ns.Reserves("Shared") {
		t.Error("default prefix reserved despite custom value")
	}
	if got := (IMAPNamespaces{}).OrDefaults(); got != DefaultIMAPNamespaces() {
		t.Errorf("OrDefaults() = %+v", got)
	}
	if (IMAPNamespaces{}).Reserves("/x") {
		t.Error("zero value must reserve nothing")
	}
}

func TestIMAPNamespacesNonASCII(t *testing.T) {
	ns := IMAPNamespaces{OtherUsers: "Übrige", Shared: "Geteilt"}
	for _, name := range []string{"übrige", "ÜBRIGE/x", "geteilt/a"} {
		if !ns.Reserves(name) {
			t.Errorf("Reserves(%q) = false", name)
		}
	}
	if ns.Reserves("Übrigex") {
		t.Error("lookalike reserved")
	}
}
