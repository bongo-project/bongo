package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mimesafe"
)

func TestEnvelopeAddresses(t *testing.T) {
	tests := []struct {
		name string
		to   string
		want []string // nil: only the count (2) and the absence of line breaks are checked
	}{
		{"q-encoded comma", "=?UTF-8?Q?M=C3=BCller=2C_J=C3=B6rg?= <j@x.com>, b@y.com",
			[]string{`"Müller, Jörg" <j@x.com>`, "b@y.com"}},
		{"b-encoded comma", "=?UTF-8?B?TcO8bGxlciwgSsO2cmc=?= <j@x.com>, b@y.com",
			[]string{`"Müller, Jörg" <j@x.com>`, "b@y.com"}},
		{"encoded word inside quotes", `"=?UTF-8?Q?M=C3=BCller=2C_J=C3=B6rg?=" <j@x.com>, b@y.com`,
			[]string{`"Müller, Jörg" <j@x.com>`, "b@y.com"}},
		{"iso-8859-1", "=?ISO-8859-1?Q?M=FCller=2C_J=F6rg?= <j@x.com>, b@y.com",
			[]string{`"Müller, Jörg" <j@x.com>`, "b@y.com"}},
		{"encoded name without comma", "=?UTF-8?Q?J=C3=B6rg?= <j@x.com>, b@y.com",
			[]string{"Jörg <j@x.com>", "b@y.com"}},
		{"plain quoted comma", `"Smith, John" <j@x.com>, b@y.com`,
			[]string{`"Smith, John" <j@x.com>`, "b@y.com"}},
		{"plain list", "a@x.com, B <b@y.com>", []string{"a@x.com", "B <b@y.com>"}},
		{"invalid list falls back", "a@x.com, <<", []string{"a@x.com", "<<"}},
		{"group as SplitAddressHeader", "undisclosed-recipients:;", []string{"undisclosed-recipients:;"}},
		{"unknown charset keeps count", "=?x-unknown?Q?a=2Cb?= <a@x.com>, b@y.com", nil},
		{"encoded CRLF stripped", "=?UTF-8?Q?J=C3=B6rg=0D=0A_Bcc:_e@evil.com=2C_X?= <j@x.com>, b@y.com", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := "From: s@ext.test\r\nTo: " + tt.to + "\r\nSubject: s\r\n\r\nhi\r\n"
			env, _, err := mimesafe.ReadEnvelope([]byte(raw))
			if err != nil {
				t.Fatalf("ReadEnvelope: %v", err)
			}
			got := EnvelopeAddresses(env, "To")
			if tt.want == nil {
				if len(got) < 2 {
					t.Fatalf("got %q, want 2 entries", got)
				}
				for _, g := range got {
					if strings.ContainsAny(g, "\r\n") {
						t.Fatalf("entry %q holds a line break", g)
					}
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("nil envelope", func(t *testing.T) {
		if got := EnvelopeAddresses(nil, "To"); got != nil {
			t.Errorf("got %q", got)
		}
	})
}
