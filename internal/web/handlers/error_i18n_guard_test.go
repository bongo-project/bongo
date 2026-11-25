package handlers

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
)

// v016NewHandlerFiles are the handler files added in v0.16. They may not carry
// a literal English http.Error body: every body goes through i18n.T with the
// request locale, or is one of the fixed constants (sharedBookNotFound,
// sharedNotFoundBody, sharedForbiddenBody, sharesNotFound). Those are
// identifiers rather than literals, so the scan does not flag them, and they
// must stay one body whatever the locale (no existence oracle).
var v016NewHandlerFiles = []string{
	"admin_groups.go",
	"admin_shared.go",
	"admin_twofactor.go",
	"compose_contact_groups.go",
	"compose_groups.go",
	"compose_selfcard.go",
	"compose_shared.go",
	"contacts_books.go",
	"contacts_books_share.go",
	"contacts_groups.go",
	"contacts_io.go",
	"contacts_self.go",
	"contacts_shared.go",
	"fetch.go",
	"folder_share.go",
	"mail_shared.go",
	"share_invite.go",
	"share_offer.go",
	"shares.go",
}

type literalKey struct{ file, text string }

// literalBodyAllowlist holds the literal bodies that are deliberately left
// untranslated. Every entry needs a justification.
var literalBodyAllowlist = map[literalKey]string{
	// A domain admin must get byte-identical 403 bodies for an absent group
	// and an out-of-scope one, or the body becomes an existence oracle. This
	// literal must stay equal to adminScope's "Forbidden" in admin_scope.go,
	// which is not localised.
	{"admin_groups.go", "Forbidden"}: "must match adminScope's Forbidden body",
}

// legacyLiteralCeilings are modified pre-v0.16 files that still hold literal
// bodies. The number is the count after the v0.16 bodies were converted. It is
// pre-v0.16 debt and must only go down: a new literal pushes the file over.
var legacyLiteralCeilings = map[string]int{
	"admin.go":      94,
	"contacts.go":   8,
	"mail_views.go": 1,
}

// scanHandlerFile returns the text of every http.Error call whose body is a
// string literal or a fmt.Sprintf with a literal format, and the key of every
// i18n.T call with a literal key.
func scanHandlerFile(t *testing.T, name string) (literals, keys []string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	str := func(e ast.Expr) (string, bool) {
		bl, ok := e.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(bl.Value)
		return s, err == nil
	}
	sel := func(e ast.Expr, pkg, fn string) bool {
		s, ok := e.(*ast.SelectorExpr)
		if !ok || s.Sel.Name != fn {
			return false
		}
		id, ok := s.X.(*ast.Ident)
		return ok && id.Name == pkg
	}
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch {
		case sel(c.Fun, "http", "Error") && len(c.Args) >= 2:
			if s, ok := str(c.Args[1]); ok {
				literals = append(literals, s)
			} else if sp, ok := c.Args[1].(*ast.CallExpr); ok && sel(sp.Fun, "fmt", "Sprintf") && len(sp.Args) > 0 {
				if s, ok := str(sp.Args[0]); ok {
					literals = append(literals, s)
				}
			}
		case sel(c.Fun, "i18n", "T") && len(c.Args) >= 2:
			if s, ok := str(c.Args[1]); ok {
				keys = append(keys, s)
			}
		}
		return true
	})
	return literals, keys
}

func TestHandlerErrorBodiesAreLocalized(t *testing.T) {
	raw, err := os.ReadFile("../../i18n/messages/en.json")
	if err != nil {
		t.Fatal(err)
	}
	en := map[string]string{}
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	checkKeys := func(t *testing.T, name string, keys []string) {
		for _, k := range keys {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: i18n key %q is not in en.json (T would render the key)", name, k)
			}
		}
	}

	for _, name := range v016NewHandlerFiles {
		t.Run(name, func(t *testing.T) {
			literals, keys := scanHandlerFile(t, name)
			for _, s := range literals {
				if _, ok := literalBodyAllowlist[literalKey{name, s}]; !ok {
					t.Errorf("%s: literal http.Error body %q: use i18n.T with the request locale, or justify it in literalBodyAllowlist", name, s)
				}
			}
			checkKeys(t, name, keys)
		})
	}

	for name, ceiling := range legacyLiteralCeilings {
		t.Run(name, func(t *testing.T) {
			literals, keys := scanHandlerFile(t, name)
			if len(literals) > ceiling {
				t.Errorf("%s: %d literal http.Error bodies, ceiling %d: new bodies must use i18n.T", name, len(literals), ceiling)
			}
			checkKeys(t, name, keys)
		})
	}

	// A stale allowlist entry could hide a later regression.
	for k := range literalBodyAllowlist {
		literals, _ := scanHandlerFile(t, k.file)
		found := false
		for _, s := range literals {
			found = found || s == k.text
		}
		if !found {
			t.Errorf("allowlist entry %q in %s matches no literal body", k.text, k.file)
		}
	}
}
