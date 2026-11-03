package imip

import (
	"mime"
	"strings"

	"github.com/jhillyerd/enmime"
)

// maxParts bounds how many parts of a message are looked at.
const maxParts = 200

// FindPart returns the calendar part of a message and the method
// parameter of its Content-Type. The part inside multipart/alternative has no
// file name, so the part tree is walked rather than the attachment list. An
// inline text/calendar with a method parameter wins; an .ics attachment, or a
// text/calendar without one, is the fallback.
func FindPart(env *enmime.Envelope) (content []byte, ctMethod string, ok bool) {
	p, method := FindCalendarPart(env)
	if p == nil {
		return nil, "", false
	}
	return p.Content, method, true
}

// FindCalendarPart is FindPart returning the part itself. The method is empty
// for a fallback part.
func FindCalendarPart(env *enmime.Envelope) (*enmime.Part, string) {
	if env == nil || env.Root == nil {
		return nil, ""
	}
	var fallback *enmime.Part
	stack := []*enmime.Part{env.Root}
	for seen := 0; len(stack) > 0 && seen < maxParts; seen++ {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p.NextSibling != nil {
			stack = append(stack, p.NextSibling)
		}
		if p.FirstChild != nil {
			stack = append(stack, p.FirstChild)
			continue
		}
		ct := strings.ToLower(p.ContentType)
		method := contentTypeMethod(p)
		switch {
		case ct == "text/calendar" && method != "":
			return p, method
		case fallback == nil && (ct == "text/calendar" || ct == "application/ics" ||
			strings.HasSuffix(strings.ToLower(p.FileName), ".ics")):
			fallback = p
		}
	}
	if fallback != nil {
		return fallback, ""
	}
	return nil, ""
}

// contentTypeMethod reads the method parameter of the part's own Content-Type
// header: enmime keeps only the parameters it uses, and method is not one.
func contentTypeMethod(p *enmime.Part) string {
	_, params, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["method"])
}
