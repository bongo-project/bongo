package contactio

import (
	"errors"
	"testing"
)

func TestSplitVCards(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
		err   bool
	}{
		{"empty input", "", 0, false},
		{"single vcard", "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD\n", 1, false},
		{"two vcards", "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD\n\nBEGIN:VCARD\nVERSION:3.0\nFN:Bob\nEND:VCARD\n", 2, false},
		{"garbage before and after", "junk\nBEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD\nmore junk\n", 1, false},
		{"case insensitive", "begin:vcard\nVERSION:3.0\nFN:Alice\nend:vcard\n", 1, false},
		{"crlf", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice\r\nEND:VCARD\r\n", 1, false},
		{"missing END is an error", "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\n", 0, true},
		{"trailing BEGIN is an error", "BEGIN:VCARD\nFN:A\nEND:VCARD\nBEGIN:VCARD\nFN:B\n", 1, true},
		{"BEGIN inside a card is an error", "BEGIN:VCARD\nFN:A\nBEGIN:VCARD\nFN:B\nEND:VCARD\n", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocks, err := SplitVCards([]byte(tc.input))
			if len(blocks) != tc.want {
				t.Errorf("got %d blocks, want %d", len(blocks), tc.want)
			}
			var pe *ParseError
			if tc.err != errors.As(err, &pe) {
				t.Errorf("err = %v, want error %v", err, tc.err)
			}
			if tc.err && pe.Reason != ReasonIncomplete {
				t.Errorf("reason = %q", pe.Reason)
			}
		})
	}
}
