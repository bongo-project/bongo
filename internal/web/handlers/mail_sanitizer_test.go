package handlers

import (
	"strings"
	"testing"
)

// TestMailSanitizerStripsDangerousCSS verifies the email HTML sanitizer strips
// CSS that enables tracking-pixel loading (url()) and full-page overlay
// phishing (position:fixed / z-index) while preserving benign presentation.
func TestMailSanitizerStripsDangerousCSS(t *testing.T) {
	h := NewMailHandler(nil, nil, nil, discardLogger())

	cases := []struct {
		name       string
		in         string
		mustDrop   []string
		mustKeep   []string
	}{
		{
			name:     "remote background url stripped",
			in:       `<div style="background:url(http://evil/track.png)">x</div>`,
			mustDrop: []string{"url(", "evil"},
		},
		{
			name:     "fixed-position overlay stripped",
			in:       `<div style="position:fixed;top:0;width:100%;height:100%;z-index:9999">x</div>`,
			mustDrop: []string{"position", "z-index"},
		},
		{
			name:     "benign color/font preserved",
			in:       `<span style="color:#ff0000;font-weight:bold">x</span>`,
			mustKeep: []string{"color", "font-weight"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := h.sanitizer.Sanitize(tc.in)
			for _, d := range tc.mustDrop {
				if strings.Contains(strings.ToLower(out), strings.ToLower(d)) {
					t.Errorf("sanitized output still contains %q: %s", d, out)
				}
			}
			for _, k := range tc.mustKeep {
				if !strings.Contains(out, k) {
					t.Errorf("sanitized output dropped benign %q: %s", k, out)
				}
			}
		})
	}
}
