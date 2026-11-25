package templates

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// The fetch page is in every settings sidebar and marks itself active.
func TestSettingsSidebarLinksToFetch(t *testing.T) {
	ctx, _ := csrfRenderCtx(t)
	var vac, page bytes.Buffer
	if err := VacationPage(VacationView{Interval: 7}, "u@example.com").Render(ctx, &vac); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(vac.String(), `href="/settings/fetch"`) {
		t.Error("vacation sidebar has no link to /settings/fetch")
	}
	if err := FetchPage(FetchPageData{Email: "u@example.com", Form: FetchFormView{Interval: 60}}).Render(ctx, &page); err != nil {
		t.Fatal(err)
	}
	i := strings.Index(page.String(), `href="/settings/fetch"`)
	if i < 0 || !strings.Contains(page.String()[i:i+200], "active") {
		t.Error("the fetch sidebar entry is not active on its own page")
	}
}

// Every form on the page carries the CSRF token and the password input never
// has a value, whatever the form view holds.
func TestFetchPageFormsCarryTokenAndNoPasswordValue(t *testing.T) {
	ctx, token := csrfRenderCtx(t)
	d := FetchPageData{
		Email: "u@example.com", Form: FetchFormView{Protocol: "imap", Security: "tls", Interval: 60},
		Accounts: []FetchAccountView{{GUID: strings.Repeat("a", 32), Title: "Work", Port: 993}},
	}
	var buf bytes.Buffer
	if err := FetchPage(d).Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	forms := regexp.MustCompile(`<form[^>]*action="/settings/fetch[^"]*"`).FindAllString(out, -1)
	tokens := strings.Count(out, `name="_csrf" value="`+token+`"`)
	if len(forms) != 3 || tokens < len(forms) {
		t.Errorf("%d fetch forms, %d tokens", len(forms), tokens)
	}
	if strings.Contains(out, `name="password"`) && !strings.Contains(out, `autocomplete="new-password"`) {
		t.Error("password input lacks autocomplete=new-password")
	}
}
