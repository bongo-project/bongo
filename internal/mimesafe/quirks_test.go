package mimesafe

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime"
)

// quirkHeaders are Content-Type spellings enmime nests on that a strict reader
// would miss, each found by FuzzCheck. An attacker can repeat any of them at
// every level, so each must be counted: one missed per level hides the whole
// chain from the depth cap.
var quirkHeaders = map[string]string{
	"plain":                   "Content-Type: multipart/mixed; boundary=%s",
	"no media type":           "Content-Type: BoundArY=%s",
	"space before colon":      "Content-Type :BoundArY=%s",
	"space before equals":     "Content-Type: x; BoundArY =%s",
	"form feed before equals": "Content-Type: x; BoundArY\f=%s",
	"colon-less fold":         "Content-Type:\n;BoundArY=%s",
	"unterminated quote":      "Content-Type: multipart/mixed; boundary=\"%s;",
	"junk before parameter":   "Content-Type:0\"BoundArY=;BoundArY=%s",
	"upper case name":         "CONTENT-TYPE: MULTIPART/MIXED; BOUNDARY=%s",
}

func quirkChain(format string, depth int) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.com\nSubject: quirk\nContent-Type: multipart/mixed; boundary=q0\n\n")
	for i := range depth {
		fmt.Fprintf(&b, "--q%d\n"+format+"\n\n", i, fmt.Sprintf("q%d", i+1))
	}
	fmt.Fprintf(&b, "--q%d\n\nleaf\n", depth)
	return []byte(b.String())
}

// TestCheckCountsQuirkSpellingsAtEveryLevel builds a chain far past MaxDepth out
// of each spelling and requires the pre-scan to reject it.
func TestCheckCountsQuirkSpellingsAtEveryLevel(t *testing.T) {
	for name, format := range quirkHeaders {
		t.Run(name, func(t *testing.T) {
			raw := quirkChain(format, MaxDepth+8)
			if _, _, err := Check(raw); !errors.Is(err, ErrTooComplex) {
				t.Errorf("Check err = %v, want ErrTooComplex", err)
			}
			if _, degraded, _ := ReadEnvelope(raw); !degraded {
				t.Error("ReadEnvelope parsed the chain instead of degrading it")
			}
		})
	}
}

// TestCheckNotBelowEnmimeOnQuirkSpellings is the shallow companion: on chains
// small enough to parse, the pre-scan depth is never below the tree enmime built.
func TestCheckNotBelowEnmimeOnQuirkSpellings(t *testing.T) {
	for name, format := range quirkHeaders {
		t.Run(name, func(t *testing.T) {
			raw := quirkChain(format, 5)
			env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
			if err != nil || env.Root == nil {
				t.Skipf("enmime rejects this spelling: %v", err)
			}
			wantDepth, wantParts := enmimeShape(env.Root)
			gotDepth, gotParts, cerr := Check(raw)
			if cerr != nil {
				t.Fatalf("Check: %v", cerr)
			}
			if gotDepth < wantDepth || gotParts < wantParts {
				t.Errorf("Check %d/%d below enmime %d/%d", gotDepth, gotParts, wantDepth, wantParts)
			}
		})
	}
}
