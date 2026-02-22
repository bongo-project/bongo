package handlers

import (
	"strings"
	"testing"
)

func TestBlockRemoteImages(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBlocked bool
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "no images",
			input:       `<p>Hello world</p>`,
			wantBlocked: false,
			wantContain: "Hello world",
		},
		{
			name:        "http remote image blocked",
			input:       `<img src="http://example.com/tracker.gif">`,
			wantBlocked: true,
			wantContain: `class="blocked-image"`,
			wantAbsent:  `<img`,
		},
		{
			name:        "https remote image blocked",
			input:       `<img src="https://example.com/image.png" alt="test">`,
			wantBlocked: true,
			wantContain: `data-src="https://example.com/image.png"`,
			wantAbsent:  `<img`,
		},
		{
			name:        "cid inline image preserved",
			input:       `<img src="cid:abc123@example.com">`,
			wantBlocked: false,
			wantContain: `<img`,
		},
		{
			name:        "data uri inline image preserved",
			input:       `<img src="data:image/png;base64,abc123">`,
			wantBlocked: false,
			wantContain: `<img`,
		},
		{
			name:        "mixed: remote blocked cid preserved",
			input:       `<img src="cid:part1"><img src="https://tracker.example.com/px.gif">`,
			wantBlocked: true,
			wantContain: `cid:part1`,
		},
		{
			name:        "single-quoted src",
			input:       `<img src='https://example.com/image.png'>`,
			wantBlocked: true,
			wantContain: `class="blocked-image"`,
		},
		{
			name:        "original url stored in data-src",
			input:       `<img src="https://example.com/track.gif">`,
			wantBlocked: true,
			wantContain: `[Remote image blocked]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, blocked := blockRemoteImages(tc.input)
			if blocked != tc.wantBlocked {
				t.Errorf("blocked = %v, want %v", blocked, tc.wantBlocked)
			}
			if tc.wantContain != "" && !strings.Contains(got, tc.wantContain) {
				t.Errorf("output does not contain %q\ngot: %s", tc.wantContain, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("output should not contain %q\ngot: %s", tc.wantAbsent, got)
			}
		})
	}
}
