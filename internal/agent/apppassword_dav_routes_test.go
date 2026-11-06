package agent

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// davRouteEnv is a running dev agent with one user whose DAV routes are
// reached over real HTTP. davBasicAuth tests build their own LocalAuth and so
// cannot see whether the agent hands its app password service to the
// handlers it serves.
type davRouteEnv struct {
	a    *Agent
	base string
	uid  string
}

const (
	davRouteUser     = "dav-app@localhost"
	davRoutePassword = "pass1234"
)

func newDAVRouteEnv(t *testing.T) *davRouteEnv {
	t.Helper()
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)
	// The last-used touch is detached; wait for it before the agent stops.
	// Cleanups run last in, first out, so this runs before the agent's stop.
	t.Cleanup(func() { a.AppPasswords.Wait() })

	uid, err := auth.CreateUser(a, a.Metadata, davRouteUser, "Dav", davRoutePassword, false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return &davRouteEnv{a: a, base: "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port), uid: uid}
}

// propfind issues a Depth 0 PROPFIND for the current user principal.
func (e *davRouteEnv) propfind(t *testing.T, path, password string) (int, string) {
	t.Helper()
	body := `<?xml version="1.0"?><propfind xmlns="DAV:"><prop><current-user-principal/></prop></propfind>`
	req, err := http.NewRequest("PROPFIND", e.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(davRouteUser, password)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PROPFIND %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// setFlags rewrites the account's two-factor flag and its app-password-only
// switch through the same Raft path the web handlers use.
func (e *davRouteEnv) setFlags(t *testing.T, totp, require bool) {
	t.Helper()
	key := []byte("/user/" + e.uid + "/profile")
	raw, err := e.a.GetKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p.TotpEnabled, p.RequireAppPasswords = totp, require
	val, err := proto.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: val}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

var davRoutes = []string{"/caldav/", "/carddav/"}

// An app password must open both real DAV routes, a wrong secret and a revoked
// credential must get exactly 401 (not 429, which would hide a missing check
// behind the throttle), and the answer must be about the right user.
func TestAppPasswordOpensRealDAVRoutes(t *testing.T) {
	e := newDAVRouteEnv(t)
	for _, route := range davRoutes {
		t.Run(route, func(t *testing.T) {
			pw, info, err := e.a.AppPasswords.Create(e.uid, "dav"+strings.Trim(route, "/"))
			if err != nil {
				t.Fatal(err)
			}
			code, body := e.propfind(t, route, pw)
			if code != http.StatusMultiStatus {
				t.Fatalf("app password on %s = %d, want 207: %s", route, code, body)
			}
			if !strings.Contains(body, e.uid) {
				t.Errorf("the multistatus body does not name the user %q: %s", e.uid, body)
			}

			wrong := pw[:6] + "-aaaaaa-aaaaaa-aaaaaa"
			if code, _ := e.propfind(t, route, wrong); code != http.StatusUnauthorized {
				t.Errorf("wrong secret on %s = %d, want exactly 401", route, code)
			}

			if err := e.a.AppPasswords.Revoke(e.uid, info.ID); err != nil {
				t.Fatal(err)
			}
			if code, _ := e.propfind(t, route, pw); code != http.StatusUnauthorized {
				t.Errorf("revoked credential on %s = %d, want exactly 401", route, code)
			}
			// Control: the same route still serves the account password, so
			// the 401s above are about the credential and not the route.
			if code, _ := e.propfind(t, route, davRoutePassword); code != http.StatusMultiStatus {
				t.Errorf("account password on %s = %d, want 207", route, code)
			}
		})
	}
}

// The per-account switch on the real routes: with two-factor and the switch on
// the account password is refused, an app password still works, and a switch
// left on without two-factor is not enforced.
func TestRequireAppPasswordsOnRealDAVRoutes(t *testing.T) {
	e := newDAVRouteEnv(t)
	pw, _, err := e.a.AppPasswords.Create(e.uid, "enforced")
	if err != nil {
		t.Fatal(err)
	}

	e.setFlags(t, false, true) // flag without 2FA
	for _, route := range davRoutes {
		if code, _ := e.propfind(t, route, davRoutePassword); code != http.StatusMultiStatus {
			t.Errorf("switch without 2FA, account password on %s = %d, want 207", route, code)
		}
	}

	e.setFlags(t, true, true)
	for _, route := range davRoutes {
		if code, _ := e.propfind(t, route, pw); code != http.StatusMultiStatus {
			t.Errorf("enforced, app password on %s = %d, want 207", route, code)
		}
		if code, _ := e.propfind(t, route, davRoutePassword); code != http.StatusUnauthorized {
			t.Errorf("enforced, account password on %s = %d, want exactly 401", route, code)
		}
	}
}
