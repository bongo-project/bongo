package rules

import (
	"fmt"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const nestedPDF = "From: a@example.com\r\n" +
	"Subject: report\r\n" +
	"List-Id: <dev.example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=inner\r\n\r\n" +
	"--inner\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
	"--inner\r\nContent-Type: text/html\r\n\r\n<p>hello</p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: Application/PDF; name=x.pdf\r\n" +
	"Content-Disposition: attachment; filename=x.pdf\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0=\r\n" +
	"--outer\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-Disposition: inline; filename=a.png\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\niVBORw0=\r\n" +
	"--outer--\r\n"

// flatMultipart builds a multipart/mixed message of n children, the last of
// which, when pdfAt > 0, is replaced by a PDF at 1-based child index pdfAt.
func flatMultipart(n, pdfAt int) []byte {
	var b strings.Builder
	b.WriteString("From: a@example.com\r\nSubject: many\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n")
	for i := 1; i <= n; i++ {
		ct := "text/plain"
		if i == pdfAt {
			ct = "application/pdf"
		}
		fmt.Fprintf(&b, "--b\r\nContent-Type: %s\r\n\r\nx\r\n", ct)
	}
	b.WriteString("--b--\r\n")
	return []byte(b.String())
}

func mimeRule(op, value string) *bongov1.FilterRule {
	return &bongov1.FilterRule{
		Id: "r", Enabled: true,
		Conditions: []*bongov1.Condition{{Field: "mime_type", Operator: op, Value: value}},
		Action:     &bongov1.FilterAction{Type: "flag"},
	}
}

func matches(r *bongov1.FilterRule, msg *Message) bool {
	return NewEngine().Evaluate([]*bongov1.FilterRule{r}, msg) != nil
}

func TestMIMETypeCondition(t *testing.T) {
	msg := ParseMessage([]byte(nestedPDF))
	tests := []struct {
		name  string
		op    string
		value string
		want  bool
	}{
		{"nested pdf equals", "equals", "application/pdf", true},
		{"equals folds the condition case", "equals", "APPLICATION/PDF", true},
		{"inline image", "equals", "image/png", true},
		{"html leaf", "equals", "text/html", true},
		{"container is not a leaf", "equals", "multipart/mixed", false},
		{"parameters are stripped", "equals", "application/pdf; name=x.pdf", false},
		{"contains", "contains", "pdf", true},
		{"contains no hit", "contains", "zip", false},
		{"matches", "matches", `^image/(png|gif)$`, true},
		{"matches no hit", "matches", `^video/`, false},
		{"matches bad pattern", "matches", `(`, false},
		{"not_contains is refused", "not_contains", "zip", false},
		{"greater_than is refused", "greater_than", "1", false},
		{"unknown operator is refused", "starts_with", "application", false},
		{"empty operator is refused", "", "application/pdf", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matches(mimeRule(tc.op, tc.value), msg); got != tc.want {
				t.Errorf("mime_type %s %q = %v, want %v", tc.op, tc.value, got, tc.want)
			}
		})
	}
}

func TestParseMessageMIMETypes(t *testing.T) {
	got := ParseMessage([]byte(nestedPDF)).MIMETypes
	want := []string{"text/plain", "text/html", "application/pdf", "image/png"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("MIMETypes = %v, want %v", got, want)
	}

	// A message without a Content-Type is text/plain (RFC 2045).
	got = ParseMessage([]byte("From: a@example.com\r\nSubject: s\r\n\r\nbody\r\n")).MIMETypes
	if len(got) != 1 || got[0] != "text/plain" {
		t.Errorf("default MIMETypes = %v, want [text/plain]", got)
	}
}

// The cap counts every node, containers included: the root is node 1, so the
// 99th child is node 100 and the 100th child is node 101.
func TestMIMETypePartCap(t *testing.T) {
	tests := []struct {
		name  string
		pdfAt int
		want  bool
	}{
		{"first child", 1, true},
		{"99th child is the 100th node", MaxRuleMIMEParts - 1, true},
		{"100th child is the 101st node", MaxRuleMIMEParts, false},
		{"150th child", 150, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := ParseMessage(flatMultipart(150, tc.pdfAt))
			if got := matches(mimeRule("equals", "application/pdf"), msg); got != tc.want {
				t.Errorf("pdf at child %d: match = %v, want %v", tc.pdfAt, got, tc.want)
			}
		})
	}
}

// A message over the mimesafe limits arrives degraded with no parts; the
// condition is false and the message is not rejected.
func TestMIMETypeDegradedMessage(t *testing.T) {
	msg := ParseMessage(flatMultipart(1500, 1))
	if len(msg.MIMETypes) != 0 {
		t.Errorf("degraded MIMETypes = %v, want none", msg.MIMETypes)
	}
	if matches(mimeRule("equals", "application/pdf"), msg) {
		t.Error("degraded message must not match a mime_type condition")
	}
	if msg.Meta.Subject != "many" {
		t.Errorf("Subject = %q, want headers kept", msg.Meta.Subject)
	}
}

func TestHeaderConditionCaseInsensitive(t *testing.T) {
	// The second message makes enmime fail, exercising the fallback scanner.
	fallback := []byte("List-ID: <dev.example.com>\r\nList-Id: <second>\r\nSubject: s\r\n\r\nbody")
	tests := []struct {
		name  string
		raw   []byte
		field string
		want  bool
	}{
		{"enmime exact", []byte(nestedPDF), "header:List-Id", true},
		{"enmime lower", []byte(nestedPDF), "header:list-id", true},
		{"enmime upper", []byte(nestedPDF), "header:LIST-ID", true},
		{"enmime prefix folded", []byte(nestedPDF), "Header:List-Id", true},
		{"enmime other header", []byte(nestedPDF), "header:X-Other", false},
		{"fallback lower", fallback, "header:list-id", true},
		{"fallback upper", fallback, "header:LIST-ID", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := ParseMessage(tc.raw)
			r := &bongov1.FilterRule{Id: "r", Enabled: true, Action: &bongov1.FilterAction{Type: "flag"},
				Conditions: []*bongov1.Condition{{Field: tc.field, Operator: "contains", Value: "dev.example.com"}}}
			if got := matches(r, msg); got != tc.want {
				t.Errorf("%s = %v, want %v", tc.field, got, tc.want)
			}
		})
	}
}

func TestExtractBasicHeadersFirstValueWins(t *testing.T) {
	subject, headers := extractBasicHeaders([]byte("Subject: one\r\nSubject: two\r\nX-A: 1\r\nx-a: 2\r\n\r\nbody"))
	if subject != "one" || headers["subject"] != "one" || headers["x-a"] != "1" {
		t.Errorf("subject=%q headers=%v, want the first value of each header", subject, headers)
	}
}

// The plain-text fallback feeds the same lowercased map, so header conditions
// behave the same when enmime cannot parse a message.
func TestHeaderConditionOnFallbackHeaders(t *testing.T) {
	subject, headers := extractBasicHeaders([]byte("List-ID: <dev.example.com>\r\nSubject: s\r\n\r\nbody"))
	msg := &Message{Meta: &bongov1.MailMeta{Subject: subject}, Headers: headers}
	for _, field := range []string{"header:List-Id", "header:list-id", "header:LIST-ID"} {
		r := &bongov1.FilterRule{Id: "r", Enabled: true, Action: &bongov1.FilterAction{Type: "flag"},
			Conditions: []*bongov1.Condition{{Field: field, Operator: "contains", Value: "dev.example.com"}}}
		if !matches(r, msg) {
			t.Errorf("%s did not match the fallback headers %v", field, headers)
		}
	}
}
