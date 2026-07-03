package mail_test

import (
	"testing"

	"github.com/bongo-go/bongo-go/internal/mail"
)

func TestSanitizeHeaderValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain value passes through",
			input: "Hello World",
			want:  "Hello World",
		},
		{
			name:  "CRLF injection stripped",
			input: "Hello\r\nBcc: evil@x.com",
			want:  "HelloBcc: evil@x.com",
		},
		{
			name:  "LF only stripped",
			input: "line1\nline2",
			want:  "line1line2",
		},
		{
			name:  "CR only stripped",
			input: "line1\rline2",
			want:  "line1line2",
		},
		{
			name:  "null byte stripped",
			input: "val\x00ue",
			want:  "value",
		},
		{
			name:  "other control chars stripped",
			input: "val\x01\x1f ue",
			want:  "val ue",
		},
		{
			name:  "DEL stripped",
			input: "val\x7fue",
			want:  "value",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "non-ASCII preserved",
			input: "Héllo wörld",
			want:  "Héllo wörld",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mail.SanitizeHeaderValue(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeHeaderValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeHeaderFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain filename passes through",
			input: "report.pdf",
			want:  "report.pdf",
		},
		{
			name:  "CRLF stripped",
			input: "file\r\n.pdf",
			want:  "file.pdf",
		},
		{
			name:  "double quote stripped",
			input: `file"name.pdf`,
			want:  "filename.pdf",
		},
		{
			name:  "CRLF and double quote combined",
			input: "att\r\n\".pdf",
			want:  "att.pdf",
		},
		{
			name:  "control char stripped",
			input: "file\x00name.txt",
			want:  "filename.txt",
		},
		{
			name:  "non-ASCII preserved",
			input: "résumé.pdf",
			want:  "résumé.pdf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mail.SanitizeHeaderFilename(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeHeaderFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
