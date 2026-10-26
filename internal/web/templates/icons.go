package templates

import (
	"strings"

	"github.com/a-h/templ"
)

// Icon data ported from the design handoff (Lucide/Feather-shaped paths). Do not
// build these from user input; the map holds constants only.

type iconDef struct {
	viewBox string
	attrs   [][2]string
	inner   string
}

var iconSet = map[string]iconDef{
	"inbox":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`},
	"send":          {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/>`},
	"draft":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>`},
	"star":          {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>`},
	"star_fill":     {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "currentColor"}, {"stroke", "currentColor"}, {"stroke-width", "1.5"}, {"stroke-linejoin", "round"}}, inner: `<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>`},
	"archive":       {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5"/><line x1="10" y1="12" x2="14" y2="12"/>`},
	"trash":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>`},
	"shield":        {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>`},
	"search":        {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>`},
	"reply":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="9 17 4 12 9 7"/><path d="M20 18v-2a4 4 0 0 0-4-4H4"/>`},
	"reply_all":     {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="7 17 2 12 7 7"/><polyline points="12 17 7 12 12 7"/><path d="M22 18v-2a4 4 0 0 0-4-4H7"/>`},
	"forward":       {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="15 17 20 12 15 7"/><path d="M4 18v-2a4 4 0 0 1 4-4h12"/>`},
	"more":          {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/>`},
	"refresh":       {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>`},
	"settings":      {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>`},
	"users":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>`},
	"calendar":      {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>`},
	"tag":           {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M20.59 13.41l-7.17 7.17a2 2 0 0 1-2.83 0L2 12V2h10l8.59 8.59a2 2 0 0 1 0 2.82z"/><line x1="7" y1="7" x2="7.01" y2="7"/>`},
	"plus":          {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`},
	"plus_sm":       {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "2"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`},
	"bell":          {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>`},
	"sliders":       {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<line x1="4" y1="21" x2="4" y2="14"/><line x1="4" y1="10" x2="4" y2="3"/><line x1="12" y1="21" x2="12" y2="12"/><line x1="12" y1="8" x2="12" y2="3"/><line x1="20" y1="21" x2="20" y2="16"/><line x1="20" y1="12" x2="20" y2="3"/><line x1="1" y1="14" x2="7" y2="14"/><line x1="9" y1="8" x2="15" y2="8"/><line x1="17" y1="16" x2="23" y2="16"/>`},
	"folder":        {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>`},
	"chevron_left":  {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="15 18 9 12 15 6"/>`},
	"chevron_right": {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<polyline points="9 18 15 12 9 6"/>`},
	"paperclip":     {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/>`},
	"attach_sm":     {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "2"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/>`},
	"arrow_right":   {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/>`},
	"check":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "3"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}, {"width", "10"}, {"height", "10"}}, inner: `<polyline points="20 6 9 17 4 12"/>`},
	"x":             {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>`},
	"alert":         {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>`},
	"moon":          {viewBox: "0 0 24 24", attrs: [][2]string{{"fill", "none"}, {"stroke", "currentColor"}, {"stroke-width", "1.8"}, {"stroke-linecap", "round"}, {"stroke-linejoin", "round"}}, inner: `<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>`},
	"logo":          {viewBox: "0 0 32 32", attrs: [][2]string{{"fill", "none"}}, inner: `<ellipse cx="10" cy="10.5" rx="6" ry="1.6" fill="currentColor" opacity=".18"/> <path d="M4 10.5 C4 9.4 6.7 8.5 10 8.5 S16 9.4 16 10.5 V21 C16 22.2 13.3 23 10 23 S4 22.2 4 21 Z" fill="currentColor"/> <ellipse cx="10" cy="10.5" rx="6" ry="1.6" fill="#fff" opacity=".35"/> <ellipse cx="22" cy="13" rx="7" ry="1.8" fill="currentColor" opacity=".18"/> <path d="M15 13 C15 11.7 18.1 10.7 22 10.7 S29 11.7 29 13 V24 C29 25.4 25.9 26.3 22 26.3 S15 25.4 15 24 Z" fill="currentColor"/> <ellipse cx="22" cy="13" rx="7" ry="1.8" fill="#fff" opacity=".35"/> <rect x="13.5" y="15.5" width="4" height="1.2" rx=".6" fill="currentColor" opacity=".5"/>`},
}

// iconClass returns the sizing class for an icon plus any caller classes.
// check and logo carry their own geometry and get no base class.
func iconClass(name, extra string) string {
	base := "icon"
	switch {
	case name == "check" || name == "logo":
		base = ""
	case strings.HasSuffix(name, "_sm"):
		base = "icon-sm"
	}
	return strings.TrimSpace(base + " " + extra)
}

func iconAttrs(def iconDef) templ.Attributes {
	attrs := templ.Attributes{}
	for _, kv := range def.attrs {
		attrs[kv[0]] = kv[1]
	}
	return attrs
}
