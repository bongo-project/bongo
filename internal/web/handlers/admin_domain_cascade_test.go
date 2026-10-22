package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	internalmail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
	"google.golang.org/protobuf/proto"
)

const cascadeDomain = "cascade.test"

func cascadeCreateDomain(t *testing.T, env *adminScopeTestEnv, domain string) *httptest.ResponseRecorder {
	t.Helper()
	return postToHandler(t, env.h, env.h.CreateDomain, http.MethodPost, "/admin/domains",
		url.Values{"domain": {domain}}, nil,
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) })
}

func cascadeDeleteDomain(t *testing.T, env *adminScopeTestEnv, domain string) *httptest.ResponseRecorder {
	t.Helper()
	return postToHandler(t, env.h, env.h.DeleteDomain, http.MethodPost, "/admin/domains/"+domain+"/delete",
		nil, map[string]string{"domain": domain},
		func(r *http.Request) *http.Request { return withGlobalAdmin(r, env.globalAdminID) })
}

func cascadePut(t *testing.T, env *adminScopeTestEnv, key string, val []byte) {
	t.Helper()
	if err := env.applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(key), Value: val}, time.Second); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func cascadeHas(env *adminScopeTestEnv, key string) bool {
	_, err := env.ps.Get([]byte(key))
	return err == nil
}

func cascadeDomainRows(t *testing.T, env *adminScopeTestEnv, domain string) int {
	t.Helper()
	var n int
	if err := env.md.System.DB().QueryRow("SELECT COUNT(*) FROM domains WHERE name = ?", domain).Scan(&n); err != nil {
		t.Fatalf("count domains: %v", err)
	}
	return n
}

func cascadeAddrRecord(t *testing.T, userID, domain, kind string) []byte {
	t.Helper()
	b, err := proto.Marshal(&bongov1.AddressRecord{UserId: userID, Domain: domain, Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDeleteDomain_CascadesConfig_RecreateDoesNotInherit(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	reg := internalmail.NewDKIMRegistry()
	env.h.SetDKIMRegistry(reg)

	if w := cascadeCreateDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d", w.Code)
	}
	oldAdmin, err := auth.CreateUser(env.applier, env.md, "oldadmin@"+testDomain1, "Old Admin", "pass123!", false)
	if err != nil {
		t.Fatal(err)
	}
	oldOwner, err := auth.CreateUser(env.applier, env.md, "oldowner@"+testDomain1, "Old Owner", "pass123!", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.das.Grant(cascadeDomain, oldAdmin); err != nil {
		t.Fatal(err)
	}
	if err := env.das.SetOwner(cascadeDomain, oldOwner); err != nil {
		t.Fatal(err)
	}
	if err := env.catchAll.Set(cascadeDomain, oldAdmin); err != nil {
		t.Fatal(err)
	}
	cascadePut(t, env, "/system/domain/"+cascadeDomain+"/dkim", []byte("k"))
	cascadePut(t, env, "/system/domain/"+cascadeDomain+"/quota", []byte("q"))
	cascadePut(t, env, "/system/address/ghost@"+cascadeDomain, cascadeAddrRecord(t, "no-such-user", cascadeDomain, "primary"))
	// A sibling domain sharing the name prefix must survive.
	cascadePut(t, env, "/system/domain/"+cascadeDomain+".au", []byte("enabled"))
	cascadePut(t, env, "/system/catchall/"+cascadeDomain+".au", []byte("x"))
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	reg.Add(cascadeDomain, internalmail.NewDKIMSignerFromKey("s", cascadeDomain, key))

	if w := cascadeDeleteDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d body=%s", w.Code, w.Body.String())
	}

	// Between delete and re-create the old scope is already gone.
	for _, id := range []string{oldAdmin, oldOwner} {
		doms, err := env.das.AuthorizedDomains(id, false)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(doms, cascadeDomain) {
			t.Errorf("user %s still authorised for deleted domain", id)
		}
	}

	if reg.Get(cascadeDomain) != nil {
		t.Error("DKIM signer survived delete")
	}

	if w := cascadeCreateDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("recreate status = %d", w.Code)
	}
	for _, id := range []string{oldAdmin, oldOwner} {
		doms, _ := env.das.AuthorizedDomains(id, false)
		if slices.Contains(doms, cascadeDomain) {
			t.Errorf("user %s regained control of re-created domain", id)
		}
	}
	if _, ok, _ := env.catchAll.Get(cascadeDomain); ok {
		t.Error("catch-all survived delete")
	}
	if _, ok, _ := env.das.GetOwner(cascadeDomain); ok {
		t.Error("owner survived delete")
	}
	for _, k := range []string{
		"/system/domain/" + cascadeDomain + "/dkim",
		"/system/domain/" + cascadeDomain + "/quota",
		"/system/domain_admin/" + cascadeDomain + "/" + oldAdmin,
		"/system/address/ghost@" + cascadeDomain,
	} {
		if cascadeHas(env, k) {
			t.Errorf("key %s survived delete", k)
		}
	}
	if !cascadeHas(env, "/system/domain/"+cascadeDomain+".au") || !cascadeHas(env, "/system/catchall/"+cascadeDomain+".au") {
		t.Error("sibling domain was purged")
	}
}

func TestDeleteDomain_RefusedWhileDependentsExist(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, env *adminScopeTestEnv, owner string)
	}{
		{"user", func(t *testing.T, env *adminScopeTestEnv, _ string) {
			if _, err := auth.CreateUser(env.applier, env.md, "u@"+cascadeDomain, "U", "pass123!", false); err != nil {
				t.Fatal(err)
			}
		}},
		{"alias", func(t *testing.T, env *adminScopeTestEnv, owner string) {
			cascadePut(t, env, "/system/alias/sales@"+cascadeDomain, []byte("x@"+testDomain1))
		}},
		{"mixed-case alias", func(t *testing.T, env *adminScopeTestEnv, owner string) {
			cascadePut(t, env, "/system/alias/Sales@Cascade.Test", []byte("x@"+testDomain1))
		}},
		{"identity", func(t *testing.T, env *adminScopeTestEnv, owner string) {
			cascadePut(t, env, "/system/address/me@"+cascadeDomain, cascadeAddrRecord(t, owner, cascadeDomain, "identity"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminScopeTestEnv(t)
			cascadeCreateDomain(t, env, cascadeDomain)
			if err := env.das.Grant(cascadeDomain, env.d1UserID); err != nil {
				t.Fatal(err)
			}
			if err := env.catchAll.Set(cascadeDomain, env.d1UserID); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, env, env.d1UserID)

			w := cascadeDeleteDomain(t, env, cascadeDomain)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", w.Code)
			}
			loc := w.Header().Get("HX-Redirect")
			if u, _ := url.Parse(loc); u == nil || u.Query().Get("flash") == "" || u.Query().Get("flash") == "Domain deleted" {
				t.Errorf("expected refusal flash, got %q", loc)
			}
			for _, k := range []string{
				"/system/domain/" + cascadeDomain,
				"/system/domain_admin/" + cascadeDomain + "/" + env.d1UserID,
				"/system/catchall/" + cascadeDomain,
			} {
				if !cascadeHas(env, k) {
					t.Errorf("%s deleted despite refusal", k)
				}
			}
			if cascadeDomainRows(t, env, cascadeDomain) != 1 {
				t.Error("sqlite domain row removed despite refusal")
			}
		})
	}
}

func TestDeleteDomain_SubdomainUserDoesNotBlock(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	if _, err := auth.CreateUser(env.applier, env.md, "u@sub."+cascadeDomain, "U", "pass123!", false); err != nil {
		t.Fatal(err)
	}
	cascadeDeleteDomain(t, env, cascadeDomain)
	if cascadeHas(env, "/system/domain/"+cascadeDomain) || cascadeDomainRows(t, env, cascadeDomain) != 0 {
		t.Error("subdomain user blocked deletion")
	}
}

func TestCreateDomain_ExistingDomainNotPurged(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	if err := env.das.Grant(cascadeDomain, env.d1UserID); err != nil {
		t.Fatal(err)
	}
	if err := env.catchAll.Set(cascadeDomain, env.d1UserID); err != nil {
		t.Fatal(err)
	}
	w := cascadeCreateDomain(t, env, "Cascade.TEST")
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if !cascadeHas(env, "/system/domain_admin/"+cascadeDomain+"/"+env.d1UserID) {
		t.Error("grant purged by re-adding a live domain")
	}
	if _, ok, _ := env.catchAll.Get(cascadeDomain); !ok {
		t.Error("catch-all purged by re-adding a live domain")
	}
}

func TestCreateDomain_ClearsStaleLeftovers(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	d := "stale.test"
	cascadePut(t, env, "/system/domain_admin/"+d+"/"+env.d1UserID, []byte("1"))
	cascadePut(t, env, "/system/domain/"+d+"/owner", []byte(env.d1UserID))
	cascadePut(t, env, "/system/catchall/"+d, []byte(env.d1UserID))
	cascadePut(t, env, "/system/address/old@"+d, cascadeAddrRecord(t, "gone-user", d, "primary"))
	// A live identity record must survive the purge.
	cascadePut(t, env, "/system/address/live@"+d, cascadeAddrRecord(t, env.d1UserID, d, "identity"))

	if w := cascadeCreateDomain(t, env, d); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	for _, k := range []string{
		"/system/domain_admin/" + d + "/" + env.d1UserID,
		"/system/domain/" + d + "/owner",
		"/system/catchall/" + d,
		"/system/address/old@" + d,
	} {
		if cascadeHas(env, k) {
			t.Errorf("stale key %s survived create", k)
		}
	}
	if !cascadeHas(env, "/system/address/live@"+d) {
		t.Error("live address record was purged")
	}
	if !cascadeHas(env, "/system/domain/"+d) {
		t.Error("domain key missing after create")
	}
}

// failDeleteApplier fails OP_DELETE for keys under failPrefix, and OP_PUT for
// keys equal to failPutKey; everything else goes to the wrapped applier.
type failDeleteApplier struct {
	*directApplierTags
	failPrefix string
	failPutKey string
}

func (a *failDeleteApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if cmd.Op == bongov1.Op_OP_DELETE && a.failPrefix != "" && strings.HasPrefix(string(cmd.Key), a.failPrefix) {
		return errors.New("injected delete failure")
	}
	if cmd.Op == bongov1.Op_OP_PUT && a.failPutKey != "" && string(cmd.Key) == a.failPutKey {
		return errors.New("injected put failure")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

func TestDeleteDomain_ScanErrorFailsClosed(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	if err := env.das.Grant(cascadeDomain, env.d1UserID); err != nil {
		t.Fatal(err)
	}
	if err := env.catchAll.Set(cascadeDomain, env.d1UserID); err != nil {
		t.Fatal(err)
	}
	// An undecodable profile fails only the dependents scan (the purge never
	// reads profiles), so a fail-open handler would go on to delete.
	cascadePut(t, env, "/user/corrupt/profile", []byte{0xff, 0xff, 0xff})

	w := cascadeDeleteDomain(t, env, cascadeDomain)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	for _, k := range []string{
		"/system/domain/" + cascadeDomain,
		"/system/domain_admin/" + cascadeDomain + "/" + env.d1UserID,
		"/system/catchall/" + cascadeDomain,
	} {
		if !cascadeHas(env, k) {
			t.Errorf("%s deleted despite scan failure", k)
		}
	}
}

func TestDeleteDomain_PurgeFailureKeepsDomainKey(t *testing.T) {
	for _, prefix := range []string{"/system/domain_admin/", "/system/catchall/"} {
		t.Run(prefix, func(t *testing.T) {
			env := newAdminScopeTestEnv(t)
			cascadeCreateDomain(t, env, cascadeDomain)
			if err := env.das.Grant(cascadeDomain, env.d1UserID); err != nil {
				t.Fatal(err)
			}
			if err := env.catchAll.Set(cascadeDomain, env.d1UserID); err != nil {
				t.Fatal(err)
			}
			env.h.applier = &failDeleteApplier{directApplierTags: env.applier, failPrefix: prefix}

			w := cascadeDeleteDomain(t, env, cascadeDomain)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
			if loc := w.Header().Get("HX-Redirect"); loc != "" {
				t.Errorf("unexpected redirect/flash %q on failure", loc)
			}
			if !cascadeHas(env, "/system/domain/"+cascadeDomain) {
				t.Error("domain key deleted despite purge failure")
			}
			if cascadeDomainRows(t, env, cascadeDomain) != 1 {
				t.Error("sqlite domain row removed despite purge failure")
			}
		})
	}
}

func TestCreateDomain_PurgeFailureDoesNotCreate(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	d := "stale2.test"
	cascadePut(t, env, "/system/catchall/"+d, []byte(env.d1UserID))
	env.h.applier = &failDeleteApplier{directApplierTags: env.applier, failPrefix: "/system/catchall/"}

	w := cascadeCreateDomain(t, env, d)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if cascadeHas(env, "/system/domain/"+d) {
		t.Error("domain key written despite failed purge")
	}
}

func TestDeleteDomain_RefusedForProfileWithoutAddressRecord(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	// A user created before the address store existed: profile only.
	prof, err := proto.Marshal(&bongov1.UserProfile{Email: "legacy@" + cascadeDomain})
	if err != nil {
		t.Fatal(err)
	}
	cascadePut(t, env, "/user/legacy-user/profile", prof)

	w := cascadeDeleteDomain(t, env, cascadeDomain)
	loc := w.Header().Get("HX-Redirect")
	if u, _ := url.Parse(loc); u == nil || !strings.Contains(u.Query().Get("flash"), "1 users") {
		t.Errorf("expected refusal flash naming 1 users, got %q", loc)
	}
	if !cascadeHas(env, "/system/domain/"+cascadeDomain) {
		t.Error("domain deleted despite profile on it")
	}
}

func TestDeleteDomain_SQLiteRowsStayInStep(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	db := env.md.System.DB()
	for _, a := range []string{"a@" + cascadeDomain, "b@CASCADE.test", "c@" + cascadeDomain + ".au", "d@xcascade.test"} {
		if _, err := db.Exec("INSERT INTO aliases (address, target_user_id, enabled) VALUES (?, ?, 1)", a, env.d1UserID); err != nil {
			t.Fatalf("seed alias %s: %v", a, err)
		}
		if _, err := db.Exec("INSERT INTO addresses (address, user_id, domain, kind) VALUES (?, ?, ?, 'identity')", a, "gone", "x"); err != nil {
			t.Fatalf("seed address %s: %v", a, err)
		}
	}
	// Foreign dependents are only in SQLite, so Pebble does not refuse.
	if w := cascadeDeleteDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	for _, table := range []string{"aliases", "addresses"} {
		var got []string
		rows, err := db.Query("SELECT address FROM " + table + " WHERE lower(address) LIKE '%cascade.test%' ORDER BY address")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var a string
			_ = rows.Scan(&a)
			got = append(got, a)
		}
		rows.Close()
		want := []string{"c@" + cascadeDomain + ".au", "d@xcascade.test"}
		if !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", table, got, want)
		}
	}
}

func TestDeleteDomain_PurgesLegacyMixedCaseDKIM(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	cascadePut(t, env, "/system/domain/Cascade.Test/dkim", []byte("old-private-key"))

	if w := cascadeDeleteDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if cascadeHas(env, "/system/domain/Cascade.Test/dkim") {
		t.Error("mixed-case DKIM key survived delete")
	}

	// Same via CreateDomain leftovers.
	cascadePut(t, env, "/system/domain/Cascade.Test/dkim", []byte("old-private-key"))
	if w := cascadeCreateDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d", w.Code)
	}
	if cascadeHas(env, "/system/domain/Cascade.Test/dkim") {
		t.Error("mixed-case DKIM key survived re-create")
	}
}

func TestCreateDomain_LegacyMixedCaseDomainRefusedNotPurged(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	// A live domain stored as typed by an older release.
	cascadePut(t, env, "/system/domain/Cascade.Test", []byte("enabled"))
	cascadePut(t, env, "/system/domain/Cascade.Test/dkim", []byte("live-private-key"))
	cascadePut(t, env, "/system/catchall/"+cascadeDomain, []byte(env.d1UserID))
	cascadePut(t, env, "/system/address/ghost@"+cascadeDomain, cascadeAddrRecord(t, "no-such-user", cascadeDomain, "primary"))

	w := cascadeCreateDomain(t, env, cascadeDomain)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	for _, k := range []string{
		"/system/domain/Cascade.Test",
		"/system/domain/Cascade.Test/dkim",
		"/system/catchall/" + cascadeDomain,
		"/system/address/ghost@" + cascadeDomain,
	} {
		if !cascadeHas(env, k) {
			t.Errorf("%s purged by re-adding a live legacy domain", k)
		}
	}
	if cascadeHas(env, "/system/domain/"+cascadeDomain) {
		t.Error("lower-case duplicate key created")
	}
}

func TestCreateDomain_ExistenceCheckErrorFailsClosed(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	d := "failclosed.test"
	cascadePut(t, env, "/system/catchall/"+d, []byte(env.d1UserID))
	cascadePut(t, env, "/system/domain_admin/"+d+"/"+env.d1UserID, []byte("1"))
	env.h.domainKeyExistsFn = func(string) (bool, error) { return false, errors.New("injected scan failure") }
	rec := &recordingApplier{inner: env.applier}
	env.h.applier = rec

	w := cascadeCreateDomain(t, env, d)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if len(rec.cmds) != 0 {
		t.Errorf("applied %d commands despite existence-check failure", len(rec.cmds))
	}
	for _, k := range []string{"/system/catchall/" + d, "/system/domain_admin/" + d + "/" + env.d1UserID} {
		if !cascadeHas(env, k) {
			t.Errorf("%s purged despite existence-check failure", k)
		}
	}
	if cascadeHas(env, "/system/domain/"+d) {
		t.Error("domain key written despite existence-check failure")
	}
}

// recordingApplier records every command before passing it on.
type recordingApplier struct {
	inner store.RaftApplier
	cmds  []*bongov1.RaftCommand
}

func (a *recordingApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	a.cmds = append(a.cmds, cmd)
	return a.inner.ApplyCommand(cmd, d)
}

func TestDeleteDomain_URLCaseDiffersFromStoredKey(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadeCreateDomain(t, env, cascadeDomain)
	if !cascadeHas(env, "/system/domain/"+cascadeDomain) {
		t.Fatal("setup: lower-case domain key missing")
	}

	if w := cascadeDeleteDomain(t, env, "Cascade.TEST"); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if cascadeHas(env, "/system/domain/"+cascadeDomain) {
		t.Error("lower-case domain key survived a delete with different capitalisation")
	}
	if ok, err := store.DomainKeyExists(env.ps, cascadeDomain); err != nil || ok {
		t.Errorf("DomainKeyExists = %v, %v after delete", ok, err)
	}
	if cascadeDomainRows(t, env, cascadeDomain) != 0 {
		t.Error("sqlite domain row survived")
	}
}

func TestDeleteDomain_RemovesRawMixedCaseKey(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	cascadePut(t, env, "/system/domain/Cascade.Test", []byte("enabled"))

	if w := cascadeDeleteDomain(t, env, "Cascade.Test"); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if cascadeHas(env, "/system/domain/Cascade.Test") {
		t.Error("raw mixed-case domain key survived delete")
	}
}

func TestCreateDomain_LowerCasesName(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	if w := cascadeCreateDomain(t, env, "Mixed.Test"); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if !cascadeHas(env, "/system/domain/mixed.test") {
		t.Error("lower-case domain key missing")
	}
	if cascadeHas(env, "/system/domain/Mixed.Test") {
		t.Error("mixed-case domain key written")
	}
	if err := env.das.Grant("mixed.test", env.d1UserID); err != nil {
		t.Errorf("Grant on lower-cased domain: %v", err)
	}
	if err := env.das.SetOwner("mixed.test", env.d1UserID); err != nil {
		t.Errorf("SetOwner on lower-cased domain: %v", err)
	}
	doms, err := env.das.AuthorizedDomains(env.d1UserID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(doms, "mixed.test") {
		t.Errorf("AuthorizedDomains = %v, want mixed.test", doms)
	}
}

func TestCreateDomain_ClearsStaleDKIMSigner(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	reg := internalmail.NewDKIMRegistry()
	env.h.SetDKIMRegistry(reg)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	reg.Add(cascadeDomain, internalmail.NewDKIMSignerFromKey("s", cascadeDomain, key))

	if w := cascadeCreateDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if reg.Get(cascadeDomain) != nil {
		t.Error("stale in-memory signer survived re-create")
	}
}

func TestDeleteAndRecreate_ConfigSeededSignerKept(t *testing.T) {
	env := newAdminScopeTestEnv(t)
	reg := internalmail.NewDKIMRegistry()
	env.h.SetDKIMRegistry(reg)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer := internalmail.NewDKIMSignerFromKey("s", cascadeDomain, key)
	cascadeCreateDomain(t, env, cascadeDomain)
	reg.Seed(cascadeDomain, signer)

	if w := cascadeDeleteDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d", w.Code)
	}
	if reg.Get(cascadeDomain) != signer {
		t.Error("config-seeded signer dropped by delete")
	}
	if w := cascadeCreateDomain(t, env, cascadeDomain); w.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d", w.Code)
	}
	if reg.Get(cascadeDomain) != signer {
		t.Error("config-seeded signer dropped by re-create")
	}
}
