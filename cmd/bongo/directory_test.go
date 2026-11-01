package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const directoryTestSecret = "directory-cli-test-secret-0123456789"

func directoryTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return directoryTestServerWith(t, nil)
}

// directoryTestServerWith seeds the base fixture, then calls extra to add
// more records before the handler starts.
func directoryTestServerWith(t *testing.T, extra func(ps *store.PebbleStore)) *httptest.Server {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	prof, _ := proto.Marshal(&bongov1.UserProfile{Email: "amy@example.com", DisplayName: "Amy", IsAdmin: true, PasswordHash: []byte("HASHBYTES")})
	for k, v := range map[string]string{
		"/system/domain/example.com":         "enabled",
		"/system/domain/alias.test":          "enabled",
		"/system/domain/alias.test/alias_of": "example.com",
		"/system/alias/sales@example.com":    "amy@example.com",
		"/system/alias/x@other.test":         "amy@example.com",
	} {
		if err := ps.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := ps.Set([]byte("/user/u1/profile"), prof); err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		extra(ps)
	}
	h := handlers.NewDirectoryListHandler(ps, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/users", h.Users)
	mux.HandleFunc("GET /internal/domains", h.Domains)
	mux.HandleFunc("GET /internal/aliases", h.AddressAliases)
	srv := httptest.NewServer(middleware.RequireInternalAuth([]byte(directoryTestSecret))(mux))
	t.Cleanup(srv.Close)
	return srv
}

// runDirectoryCLI executes the root command with explicit values for every
// list flag, because flag values persist across Execute calls in one process.
func runDirectoryCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func listArgs(cmd []string, addr, secret, domain string, jsonOut bool) []string {
	args := append(append([]string{}, cmd...), "--http-addr="+addr, "--session-secret="+secret, "--domain="+domain)
	if domain == "" && cmd[0] == "domain" {
		args = args[:len(args)-1]
	}
	if jsonOut {
		return append(args, "--json")
	}
	return append(args, "--json=false")
}

func TestDirectoryCLITextAndJSON(t *testing.T) {
	srv := directoryTestServer(t)
	addr := strings.TrimPrefix(srv.URL, "http://")

	tests := []struct {
		name   string
		cmd    []string
		domain string
		addr   string
		text   []string
		fields []string
		rows   int
	}{
		{"users", []string{"user", "list"}, "", addr, []string{"EMAIL", "amy@example.com", "Amy", "enabled", "yes"}, []string{"id", "email", "display_name", "disabled", "admin", "admin_domains"}, 1},
		{"users url form", []string{"user", "list"}, "", srv.URL, []string{"amy@example.com"}, nil, 1},
		{"users filtered out", []string{"user", "list"}, "nope.test", addr, []string{"EMAIL"}, nil, 0},
		{"domains", []string{"domain", "list"}, "", addr, []string{"DOMAIN", "alias.test", "example.com", "unlimited"}, []string{"name", "enabled", "alias_of", "catch_all", "max_users", "max_storage_bytes", "usage_bytes", "dkim"}, 2},
		{"aliases", []string{"alias", "list"}, "example.com", addr, []string{"sales@example.com", "amy@example.com"}, []string{"address", "target"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runDirectoryCLI(t, listArgs(tc.cmd, tc.addr, directoryTestSecret, tc.domain, false)...)
			if err != nil {
				t.Fatalf("text: %v\n%s", err, out)
			}
			for _, want := range tc.text {
				if !strings.Contains(out, want) {
					t.Errorf("text output missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "HASHBYTES") {
				t.Error("secret in output")
			}

			out, err = runDirectoryCLI(t, listArgs(tc.cmd, tc.addr, directoryTestSecret, tc.domain, true)...)
			if err != nil {
				t.Fatalf("json: %v\n%s", err, out)
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				t.Fatalf("--json is not an array: %v\n%s", err, out)
			}
			if len(rows) != tc.rows {
				t.Fatalf("rows = %d, want %d", len(rows), tc.rows)
			}
			for _, f := range tc.fields {
				if _, ok := rows[0][f]; !ok {
					t.Errorf("json row lacks field %q: %v", f, rows[0])
				}
			}
		})
	}
}

func TestDirectoryCLIAuthAndReachability(t *testing.T) {
	srv := directoryTestServer(t)
	addr := strings.TrimPrefix(srv.URL, "http://")

	_, err := runDirectoryCLI(t, listArgs([]string{"user", "list"}, addr, "wrong-secret-wrong-secret-0000000", "", false)...)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong secret: err = %v, want a 401 message", err)
	}

	// A port nothing listens on: close a listener to free it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	_ = ln.Close()

	out, err := runDirectoryCLI(t, listArgs([]string{"domain", "list"}, dead, directoryTestSecret, "", false)...)
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "cannot reach bongo control listener at "+dead+": ") || strings.Contains(msg, "\n") {
		t.Errorf("message = %q", msg)
	}
	if strings.Contains(msg, directoryTestSecret) || strings.Contains(out, "Usage:") {
		t.Errorf("secret or usage leaked: msg=%q out=%q", msg, out)
	}
}

func TestControlAddr(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8081":          "127.0.0.1:8081",
		"http://127.0.0.1:8081":   "127.0.0.1:8081",
		" http://127.0.0.1:8081/": "127.0.0.1:8081",
	} {
		if got, err := controlAddr(in); err != nil || got != want {
			t.Errorf("controlAddr(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	// The control listener is plain HTTP; another scheme is a clear error,
	// not a malformed http://https://... URL.
	for _, in := range []string{"https://127.0.0.1:8081", "ftp://127.0.0.1:8081"} {
		if got, err := controlAddr(in); err == nil {
			t.Errorf("controlAddr(%q) = %q, want an error", in, got)
		}
	}
}

func TestDirectoryCLITruncatedExitsNonZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"count":1,"truncated":true,"items":[{"address":"a@b.c","target":"t"}]}`))
	}))
	defer srv.Close()
	out, err := runDirectoryCLI(t, listArgs([]string{"alias", "list"}, strings.TrimPrefix(srv.URL, "http://"), "", "", false)...)
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("err = %v, want a truncation error", err)
	}
	if !strings.Contains(out, "a@b.c") {
		t.Errorf("rows should still be printed: %q", out)
	}
}

// tableRow returns the whitespace-separated tokens of the first line of out
// that starts with prefix.
func tableRow(t *testing.T, out, prefix string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.Fields(line)
		}
	}
	t.Fatalf("no line starting %q in:\n%s", prefix, out)
	return nil
}

func TestDirectoryCLITableRenderings(t *testing.T) {
	srv := directoryTestServerWith(t, func(ps *store.PebbleStore) {
		off, _ := proto.Marshal(&bongov1.UserProfile{Email: "bob@example.com", Disabled: true, PasswordHash: []byte("HASHBYTES")})
		q, _ := proto.Marshal(&bongov1.DomainQuota{MaxUsers: 5, MaxStorageBytes: 1536})
		usage := make([]byte, 8)
		binary.LittleEndian.PutUint64(usage, 1048575)
		for _, kv := range []struct {
			k string
			v []byte
		}{
			{"/user/u2/profile", off},
			{"/system/domain_admin/example.com/u1", []byte("1")},
			{string(store.CatchAllKey("example.com")), []byte("u1")},
			{string(store.DomainQuotaKey("example.com")), q},
			{string(store.DomainUsageKey("example.com")), usage},
			{"/system/domain/example.com/dkim", []byte("DKIMPRIVATEBYTES")},
		} {
			if err := ps.Set([]byte(kv.k), kv.v); err != nil {
				t.Fatal(err)
			}
		}
	})
	addr := strings.TrimPrefix(srv.URL, "http://")

	out, err := runDirectoryCLI(t, listArgs([]string{"user", "list"}, addr, directoryTestSecret, "", false)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.SplitN(out, "\n", 2)[0], "ADMIN DOMAINS") {
		t.Errorf("header lacks ADMIN DOMAINS:\n%s", out)
	}
	// EMAIL NAME STATE ADMIN ADMIN-DOMAINS
	amy := tableRow(t, out, "amy@example.com")
	if len(amy) < 5 || amy[len(amy)-1] != "example.com" || amy[len(amy)-3] != "enabled" {
		t.Errorf("amy row = %v", amy)
	}
	bob := tableRow(t, out, "bob@example.com")
	if len(bob) < 5 || bob[len(bob)-3] != "disabled" || bob[len(bob)-1] != "-" {
		t.Errorf("bob row = %v", bob)
	}

	out, err = runDirectoryCLI(t, listArgs([]string{"domain", "list"}, addr, directoryTestSecret, "", false)...)
	if err != nil {
		t.Fatal(err)
	}
	ex := tableRow(t, out, "example.com")
	// DOMAIN STATE ALIAS-OF CATCH-ALL MAX-USERS MAX-STORAGE USAGE DKIM
	want := []string{"example.com", "enabled", "-", "amy@example.com", "5", "1.5", "KiB", "1.0", "MiB", "yes"}
	if strings.Join(ex, " ") != strings.Join(want, " ") {
		t.Errorf("example.com row = %v, want %v", ex, want)
	}
	alias := tableRow(t, out, "alias.test")
	if alias[len(alias)-1] != "no" {
		t.Errorf("alias.test row = %v, want DKIM no", alias)
	}
	if strings.Contains(out, "DKIMPRIVATEBYTES") || strings.Contains(out, "HASHBYTES") {
		t.Error("secret in table output")
	}
	out, err = runDirectoryCLI(t, listArgs([]string{"user", "list"}, addr, directoryTestSecret, "", true)...)
	if err != nil || strings.Contains(out, "HASHBYTES") {
		t.Errorf("json: err=%v leak=%v", err, strings.Contains(out, "HASHBYTES"))
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		// One byte short of a MiB rounds to the next unit, not 1024.0 KiB.
		{1048575, "1.0 MiB"},
		{1047552, "1023.0 KiB"},
		{1048576, "1.0 MiB"},
		{1572864, "1.5 MiB"},
	}
	for _, tc := range tests {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestQuotaBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{-1, "unlimited"},
		{0, "unlimited"},
		{1023, "1023 B"},
		{1536, "1.5 KiB"},
	}
	for _, tc := range tests {
		if got := quotaBytes(tc.in); got != tc.want {
			t.Errorf("quotaBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
