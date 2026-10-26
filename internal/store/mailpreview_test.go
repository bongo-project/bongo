package store

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bongo-project/bongo/internal/mimesafe"
)

func previewOf(t *testing.T, raw string) (string, bool) {
	t.Helper()
	env, _, err := mimesafe.ReadEnvelope([]byte(raw))
	if err != nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	return MailPreview(env)
}

func TestMailPreview(t *testing.T) {
	hdr := "From: a@example.com\r\nTo: b@example.com\r\nSubject: s\r\nMIME-Version: 1.0\r\n"
	long := strings.Repeat("€", 170) // 3-byte runes, 510 bytes; 160 runes = 480 bytes

	tests := []struct {
		name    string
		raw     string
		want    string
		wantHas bool
		check   func(t *testing.T, got string)
	}{
		{
			name: "plain",
			raw:  hdr + "Content-Type: text/plain; charset=utf-8\r\n\r\nHello there\r\nsecond line\r\n",
			want: "Hello there second line",
		},
		{
			name: "whitespace collapse",
			raw:  hdr + "Content-Type: text/plain\r\n\r\n  a\t\tb \r\n\r\n\r\n   c   \r\n",
			want: "a b c",
		},
		{
			name: "quoted reply skipped",
			raw:  hdr + "Content-Type: text/plain\r\n\r\nThanks, that works.\r\n\r\n> earlier text\r\n>> older text\r\n  > indented quote\r\nBye\r\n",
			want: "Thanks, that works. Bye",
		},
		{
			name: "html only strips style script and entities",
			raw: hdr + "Content-Type: text/html; charset=utf-8\r\n\r\n" +
				"<html><head><title>T</title><style>p{color:red}</style></head><body>" +
				"<script>alert('x')</script><p>Fish &amp; chips</p><p>a&nbsp;b</p><a href=\"http://e.x/\">link</a></body></html>\r\n",
			want: "Fish & chips a b link",
			check: func(t *testing.T, got string) {
				for _, bad := range []string{"<", "color", "alert", "http://"} {
					if strings.Contains(got, bad) {
						t.Errorf("preview %q contains %q", got, bad)
					}
				}
			},
		},
		{
			name: "alternative prefers plain",
			raw: hdr + "Content-Type: multipart/alternative; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/plain\r\n\r\nplain version\r\n" +
				"--B\r\nContent-Type: text/html\r\n\r\n<p>html version</p>\r\n--B--\r\n",
			want: "plain version",
		},
		{
			name: "attachment disposition",
			raw: hdr + "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/plain\r\n\r\nsee attached\r\n" +
				"--B\r\nContent-Type: text/plain; name=n.txt\r\nContent-Disposition: attachment; filename=n.txt\r\n\r\nfile body\r\n--B--\r\n",
			want:    "see attached",
			wantHas: true,
		},
		{
			name: "pdf without disposition",
			raw: hdr + "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
				"--B\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n--B--\r\n",
			want:    "body",
			wantHas: true,
		},
		{
			name: "inline cid image not counted",
			raw: hdr + "Content-Type: multipart/related; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/html\r\n\r\n<p>hi <img src=\"cid:x\"></p>\r\n" +
				"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\nContent-Disposition: inline\r\nContent-Transfer-Encoding: base64\r\n\r\niVBORw0KGgo=\r\n--B--\r\n",
			want:    "hi",
			wantHas: false,
		},
		{
			name: "html body-level style and script dropped",
			raw: hdr + "Content-Type: text/html; charset=utf-8\r\n\r\n" +
				"<html><body><style>.x{color:red}</style><p>Visible</p><div><script>var secret=1;</script>text</div></body></html>\r\n",
			want: "Visible text",
			check: func(t *testing.T, got string) {
				for _, bad := range []string{"color", "secret", "var"} {
					if strings.Contains(got, bad) {
						t.Errorf("preview %q contains %q", got, bad)
					}
				}
			},
		},
		{
			name: "inline tags do not split words",
			raw:  hdr + "Content-Type: text/html\r\n\r\n<p><b>He</b>llo <i>wor</i>ld</p>\r\n",
			want: "Hello world",
		},
		{
			name: "html mail with attached txt previews the html",
			raw: hdr + "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/html\r\n\r\n<p>html <a href=\"http://e.x/\">body</a></p>\r\n" +
				"--B\r\nContent-Type: text/plain; name=n.txt\r\nContent-Disposition: attachment; filename=n.txt\r\n\r\nattachment text\r\n--B--\r\n",
			want:    "html body",
			wantHas: true,
		},
		{
			name: "cid image without disposition not counted",
			raw: hdr + "Content-Type: multipart/related; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/html\r\n\r\n<p>hi <img src=\"cid:x\"></p>\r\n" +
				"--B\r\nContent-Type: image/png\r\nContent-ID: <x>\r\nContent-Transfer-Encoding: base64\r\n\r\niVBORw0KGgo=\r\n--B--\r\n",
			want:    "hi",
			wantHas: false,
		},
		{
			name: "inline-disposition pdf not counted",
			raw: hdr + "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
				"--B\r\nContent-Type: application/pdf\r\nContent-Disposition: inline; filename=a.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n--B--\r\n",
			want:    "body",
			wantHas: false,
		},
		{
			name: "text calendar part not an attachment",
			raw: hdr + "Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
				"--B\r\nContent-Type: text/plain\r\n\r\nmeeting\r\n" +
				"--B\r\nContent-Type: text/calendar; method=REQUEST\r\n\r\nBEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n--B--\r\n",
			want:    "meeting",
			wantHas: false,
		},
		{
			name: "quoted-printable utf8",
			raw:  hdr + "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nCaf=C3=A9 =E2=82=AC5 caf=\r\n=C3=A9\r\n",
			want: "Café €5 café",
		},
		{
			name: "8bit utf8",
			raw:  hdr + "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\nÜber grüße 日本語\r\n",
			want: "Über grüße 日本語",
		},
		{
			name: "truncated to 160 runes",
			raw:  hdr + "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + long + "\r\n",
			check: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Fatalf("invalid UTF-8")
				}
				if n := utf8.RuneCountInString(got); n != 160 {
					t.Fatalf("rune count = %d, want 160", n)
				}
			},
			want: strings.Repeat("€", 160),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, has := previewOf(t, tc.raw)
			if got != tc.want {
				t.Errorf("preview = %q, want %q", got, tc.want)
			}
			if has != tc.wantHas {
				t.Errorf("hasAttachments = %v, want %v", has, tc.wantHas)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}
