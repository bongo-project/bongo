package main

import "testing"

func TestDarkApplied(t *testing.T) {
	tests := []struct {
		name  string
		state themeState
		want  bool
	}{
		{"explicit dark", themeState{Theme: "dark", PrefersDark: true, Background: "rgb(10, 11, 13)"}, true},
		{"explicit dark ignores media", themeState{Theme: "dark", Background: "rgb(10, 11, 13)"}, true},
		{"auto, dark preference, dark page", themeState{Theme: "auto", PrefersDark: true, Background: "rgb(10, 11, 13)"}, true},
		{"auto, dark preference, light page (the bug)", themeState{Theme: "auto", PrefersDark: true, Background: "rgb(252, 252, 253)"}, false},
		{"auto, light preference, dark page", themeState{Theme: "auto", PrefersDark: false, Background: "rgb(10, 11, 13)"}, false},
		{"auto, transparent background", themeState{Theme: "auto", PrefersDark: true, Background: "rgba(0, 0, 0, 0)"}, false},
		{"auto, unparsable background", themeState{Theme: "auto", PrefersDark: true, Background: "oklch(0.1 0 0)"}, false},
		{"light", themeState{Theme: "light", PrefersDark: true, Background: "rgb(10, 11, 13)"}, false},
		{"unset", themeState{PrefersDark: true, Background: "rgb(10, 11, 13)"}, false},
		{"auto, mid grey is not dark", themeState{Theme: "auto", PrefersDark: true, Background: "rgb(140, 140, 140)"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := darkApplied(tt.state); got != tt.want {
				t.Errorf("darkApplied(%+v) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}
