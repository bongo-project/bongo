package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/rules"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// condForm is a rule form with one flag action and the given condition rows,
// each {field, operator, value}.
func condForm(rows ...[3]string) url.Values {
	form := formFor(row{"flag", "", "", ""})
	for _, r := range rows {
		form.Add("cond_field", r[0])
		form.Add("cond_operator", r[1])
		form.Add("cond_value", r[2])
	}
	return form
}

func TestParseRuleFormConditions(t *testing.T) {
	longName := strings.Repeat("a", maxHeaderName)
	tests := []struct {
		name    string
		form    url.Values
		wantErr string
	}{
		{"known fields", condForm(
			[3]string{"from", "contains", "a"}, [3]string{"to", "equals", "b"}, [3]string{"cc", "matches", "c"},
			[3]string{"subject", "not_contains", "d"}, [3]string{"body", "contains", "e"},
			[3]string{"has_attachment", "equals", "true"}, [3]string{"size", "greater_than", "10"},
		), ""},
		{"mime type", condForm([3]string{"mime_type", "equals", "application/pdf"}), ""},
		{"mime type contains", condForm([3]string{"mime_type", "contains", "pdf"}), ""},
		{"mime type matches", condForm([3]string{"mime_type", "matches", "^image/"}), ""},
		{"header", condForm([3]string{"header:List-Id", "contains", "dev"}), ""},
		{"header with symbols", condForm([3]string{"header:X_My.Header+1", "equals", "v"}), ""},
		{"header at the length cap", condForm([3]string{"header:" + longName, "equals", "v"}), ""},
		{"header over the length cap", condForm([3]string{"header:" + longName + "a", "equals", "v"}), "rules.errHeaderName"},
		{"unknown field", condForm([3]string{"envelope", "contains", "x"}), "rules.errConditionField"},
		{"empty field", condForm([3]string{"", "contains", "x"}), "rules.errConditionField"},
		{"upper case field", condForm([3]string{"From", "contains", "x"}), "rules.errConditionField"},
		{"header without name", condForm([3]string{"header:", "contains", "x"}), "rules.errHeaderName"},
		{"header with space", condForm([3]string{"header:Bad Name", "contains", "x"}), "rules.errHeaderName"},
		{"header with colon", condForm([3]string{"header:A:B", "contains", "x"}), "rules.errHeaderName"},
		{"header with CR LF", condForm([3]string{"header:A\r\nBcc", "contains", "x"}), "rules.errHeaderName"},
		{"header with NUL", condForm([3]string{"header:A\x00B", "contains", "x"}), "rules.errHeaderName"},
		{"header with DEL", condForm([3]string{"header:A\x7fB", "contains", "x"}), "rules.errHeaderName"},
		{"header non-ASCII", condForm([3]string{"header:Näme", "contains", "x"}), "rules.errHeaderName"},
		{"header upper prefix", condForm([3]string{"Header:List-Id", "contains", "x"}), "rules.errConditionField"},
		{"header bad operator", condForm([3]string{"header:List-Id", "greater_than", "1"}), "rules.errConditionField"},
		{"mime not_contains", condForm([3]string{"mime_type", "not_contains", "pdf"}), "rules.errConditionField"},
		{"mime greater_than", condForm([3]string{"mime_type", "greater_than", "1"}), "rules.errConditionField"},
		{"size contains", condForm([3]string{"size", "contains", "1"}), "rules.errConditionField"},
		{"from greater_than", condForm([3]string{"from", "greater_than", "1"}), "rules.errConditionField"},
		{"unknown operator", condForm([3]string{"from", "starts_with", "x"}), "rules.errConditionField"},
		{"has_attachment contains", condForm([3]string{"has_attachment", "contains", "true"}), "rules.errConditionField"},
		{"has_attachment value", condForm([3]string{"has_attachment", "equals", "maybe"}), "rules.errConditionField"},
		{"value too long", condForm([3]string{"from", "contains", strings.Repeat("x", maxConditionValue+1)}), "rules.errConditionLimit"},
		{"value at the cap", condForm([3]string{"from", "contains", strings.Repeat("x", maxConditionValue)}), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, errKey := parseForm(t, tc.form)
			if errKey != tc.wantErr {
				t.Fatalf("error = %q, want %q", errKey, tc.wantErr)
			}
			if tc.wantErr == "" && rule == nil {
				t.Fatal("no rule returned")
			}
		})
	}
}

func TestParseRuleFormConditionLists(t *testing.T) {
	mismatched := func(drop string) url.Values {
		form := condForm([3]string{"from", "contains", "a"}, [3]string{"to", "contains", "b"})
		form[drop] = form[drop][:1]
		return form
	}
	many := func(n int) url.Values {
		rows := make([][3]string, n)
		for i := range rows {
			rows[i] = [3]string{"from", "contains", "a"}
		}
		return condForm(rows...)
	}
	tests := []struct {
		name    string
		form    url.Values
		wantErr string
	}{
		{"operators short", mismatched("cond_operator"), "rules.errConditionField"},
		{"values short", mismatched("cond_value"), "rules.errConditionField"},
		{"fields short", mismatched("cond_field"), "rules.errConditionField"},
		{"at the cap", many(maxRuleConditions), ""},
		{"over the cap", many(maxRuleConditions + 1), "rules.errConditionLimit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, errKey := parseForm(t, tc.form); errKey != tc.wantErr {
				t.Fatalf("error = %q, want %q", errKey, tc.wantErr)
			}
		})
	}
}

// A rule stored with header:List-Id must open in the editor as Header plus a
// name and save back as header:List-Id, not as the first select option.
func TestHeaderRuleRoundTripsThroughEditor(t *testing.T) {
	stored := &bongov1.FilterRule{
		Id: "r1", Name: "lists", Enabled: true, Match: "all",
		Conditions: []*bongov1.Condition{
			{Field: "header:List-Id", Operator: "contains", Value: "dev.example.com"},
			{Field: "mime_type", Operator: "equals", Value: "application/pdf"},
			{Field: "subject", Operator: "contains", Value: "x"},
		},
	}
	rules.SetActions(stored, []*bongov1.FilterAction{{Type: "flag"}})

	var buf bytes.Buffer
	page := templates.RuleFormPage(ruleToInfo(stored), []string{"INBOX"}, false, "a@example.com", "")
	if err := page.Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`data-conditions="([^"]*)"`).FindStringSubmatch(buf.String())
	if m == nil {
		t.Fatal("no data-conditions attribute")
	}
	var rows []struct{ Field, Header, Operator, Value string }
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Field != "header" || rows[0].Header != "List-Id" {
		t.Fatalf("first row = %+v, want Header with name List-Id", rows[0])
	}

	// Post what the editor's postedField produces.
	form := formFor(row{"flag", "", "", ""})
	for _, r := range rows {
		field := r.Field
		if field == "header" {
			field = "header:" + r.Header
		}
		form.Add("cond_field", field)
		form.Add("cond_operator", r.Operator)
		form.Add("cond_value", r.Value)
	}
	got, errKey := parseForm(t, form)
	if errKey != "" {
		t.Fatalf("error %s", errKey)
	}
	for i, c := range got.Conditions {
		if !proto.Equal(c, stored.Conditions[i]) {
			t.Errorf("condition %d = %v, want %v", i, c, stored.Conditions[i])
		}
	}
}

// The tester evaluates the raw message exactly as delivery does, so header and
// mime_type conditions match a stored message although its stored meta carries
// neither. A message with no readable blob is not counted.
func TestRuleTesterMatchesHeaderAndMIMEType(t *testing.T) {
	env := newMailTagsTestEnv(t)
	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := NewRulesHandler(rules.NewRuleStore(env.ps, env.applier), env.mailboxes, env.ps, blobs)

	raw := "From: a@example.com\r\nSubject: report\r\nList-Id: <dev.example.com>\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nJVBERi0=\r\n--b--\r\n"
	hash, err := blobs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	store1 := func(guid string, ref *bongov1.BlobRef) {
		meta := &bongov1.MailMeta{Guid: guid, From: "stored@example.com", Subject: "stored", ReceivedAt: time.Now().Unix(), RawMessage: ref}
		b, _ := proto.Marshal(meta)
		if err := env.applier.ApplyCommand(&bongov1.RaftCommand{
			Op: bongov1.Op_OP_PUT, Key: []byte(fmt.Sprintf("/user/u1/mail/%s", guid)), Value: b,
		}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := env.mailboxes.DeliverMessage("u1", "INBOX", meta, 0); err != nil {
			t.Fatal(err)
		}
	}
	_ = env.mailboxes.CreateMailbox("u1", "INBOX")
	store1("g1", &bongov1.BlobRef{Hash: hash, Size: int64(len(raw))})
	store1("g2", &bongov1.BlobRef{Hash: strings.Repeat("0", 64), Size: 10}) // blob missing

	tests := []struct {
		field, op, value string
		wantMatches      int
	}{
		{"header:list-id", "contains", "dev.example.com", 1},
		{"header:LIST-ID", "contains", "dev.example.com", 1},
		{"header:X-Other", "contains", "dev.example.com", 0},
		{"mime_type", "equals", "application/pdf", 1},
		{"mime_type", "equals", "image/png", 0},
		{"has_attachment", "equals", "true", 1},
	}
	for _, tc := range tests {
		t.Run(tc.field+" "+tc.op+" "+tc.value, func(t *testing.T) {
			rule := &bongov1.FilterRule{Id: "r", Enabled: true,
				Conditions: []*bongov1.Condition{{Field: tc.field, Operator: tc.op, Value: tc.value}}}
			rules.SetActions(rule, []*bongov1.FilterAction{{Type: "flag"}})
			got := h.testRuleAgainstMail("u1", rule)
			if got.Matches != tc.wantMatches {
				t.Errorf("matches = %d, want %d", got.Matches, tc.wantMatches)
			}
			if got.Total != 1 {
				t.Errorf("total = %d, want 1 (the message without a blob is not counted)", got.Total)
			}
		})
	}
}
