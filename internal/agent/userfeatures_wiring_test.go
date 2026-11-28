package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// notFoundBody is what http.NotFound writes, which is what RequireFeature
// answers with.
const notFoundBody = "404 page not found\n"

// setDisabledFeatures writes the user's switches through Raft, as the admin
// page does.
func setDisabledFeatures(t *testing.T, a *Agent, userID string, features ...string) {
	t.Helper()
	key := []byte("/user/" + userID + "/profile")
	data, err := a.Store.Get(key)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	p.DisabledFeatures = features
	out, err := proto.Marshal(&p)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := a.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: out}, 5*time.Second); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// loginClient returns a client signed in to the web interface as email.
func loginClient(t *testing.T, base, email, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:           jar,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.PostForm(base+"/login", url.Values{"email": {email}, "password": {password}})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Fatalf("login %s status = %d", email, resp.StatusCode)
	}
	return client
}

// TestUserFeatureSwitchesWiredIntoRun starts a real dev Agent and checks that
// Run gates the rules, vacation and forwarding settings routes with
// RequireFeature, and that the pipeline Run builds gives the rules and deliver
// stages the per-user switches. Each check has a control user with every
// feature on, so a route that is missing or a stage that does nothing for
// everyone cannot pass as gated.
func TestUserFeatureSwitchesWiredIntoRun(t *testing.T) {
	a, admin, base := composePipelineAgent(t)

	const password = "pass1234"
	onID, err := auth.CreateUser(a, a.Metadata, "on@localhost", "On", password, false)
	if err != nil {
		t.Fatalf("CreateUser on: %v", err)
	}
	offID, err := auth.CreateUser(a, a.Metadata, "off@localhost", "Off", password, false)
	if err != nil {
		t.Fatalf("CreateUser off: %v", err)
	}
	setDisabledFeatures(t, a, offID, store.FeatureRules, store.FeatureForwarding, store.FeatureVacation)

	t.Run("settings routes", func(t *testing.T) {
		on := loginClient(t, base, "on@localhost", password)
		off := loginClient(t, base, "off@localhost", password)
		routes := []struct{ method, path string }{
			{http.MethodGet, "/settings/rules"},
			{http.MethodGet, "/settings/rules/new"},
			{http.MethodPost, "/settings/rules"},
			{http.MethodPost, "/settings/rules/reorder"},
			{http.MethodGet, "/settings/rules/r1"},
			{http.MethodPost, "/settings/rules/r1"},
			{http.MethodDelete, "/settings/rules/r1"},
			{http.MethodPost, "/settings/rules/r1/test"},
			{http.MethodGet, "/settings/vacation"},
			{http.MethodPost, "/settings/vacation"},
			{http.MethodPost, "/settings/forwarding/request"},
			{http.MethodPost, "/settings/forwarding/verify"},
		}
		do := func(c *http.Client, method, path string) (int, string) {
			t.Helper()
			req, err := http.NewRequest(method, base+path, strings.NewReader(""))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := c.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return resp.StatusCode, string(body)
		}
		for _, rt := range routes {
			// Control: the route is mounted and reaches its handler.
			if code, body := do(on, rt.method, rt.path); code == http.StatusNotFound && body == notFoundBody {
				t.Errorf("%s %s for a user with every feature on = plain 404: route not mounted", rt.method, rt.path)
			}
			if code, body := do(off, rt.method, rt.path); code != http.StatusNotFound || body != notFoundBody {
				t.Errorf("%s %s with the feature off = %d %.40q, want the plain 404", rt.method, rt.path, code, body)
			}
		}
	})

	t.Run("pipeline", func(t *testing.T) {
		for id, target := range map[string]string{onID: "ext-on@remote.example", offID: "ext-off@remote.example"} {
			if err := a.Rules.Save(id, &bongov1.FilterRule{
				Id: "r1", Name: "invoices", Enabled: true, Priority: 1,
				Conditions: []*bongov1.Condition{{Field: "subject", Operator: "contains", Value: "invoice"}},
				Action:     &bongov1.FilterAction{Type: "move", Target: "Invoices"},
			}); err != nil {
				t.Fatalf("Save rule: %v", err)
			}
			if err := a.forwardTargetStore().SetState(id, target, store.ForwardStateVerified); err != nil {
				t.Fatalf("SetState: %v", err)
			}
			prefs := store.DefaultPreferences()
			prefs.ForwardAddress = target
			prefs.ForwardKeepCopy = true
			if err := a.Preferences.Set(id, prefs); err != nil {
				t.Fatalf("Set prefs: %v", err)
			}
		}

		resp, err := admin.PostForm(base+"/compose/send", url.Values{
			"send_id": {composeSendID()},
			"to":      {"on@localhost"}, "cc": {"off@localhost"}, "subject": {"invoice 7"}, "body": {"please pay"},
		})
		if err != nil {
			t.Fatalf("POST /compose/send: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("compose send status = %d, want 303", resp.StatusCode)
		}

		forwardsTo := func(target string) int {
			n := 0
			pending, _ := a.QueueStore.ListPending(100)
			for _, m := range pending {
				for _, r := range m.Recipients {
					if r == target {
						n++
					}
				}
			}
			return n
		}
		// Controls: the rule files the copy and the forward is queued.
		waitFor(t, "on's Invoices copy", func() bool { return mailCount(a, onID, "Invoices") == 1 })
		waitFor(t, "on's forward", func() bool { return forwardsTo("ext-on@remote.example") == 1 })
		waitFor(t, "off's INBOX copy", func() bool { return mailCount(a, offID, "INBOX") == 1 })
		// A forward would be queued right after delivery; keep sampling.
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if n := forwardsTo("ext-off@remote.example"); n != 0 {
				t.Fatalf("forward queued for a user with forwarding off: %d", n)
			}
		}
		if n := mailCount(a, offID, "Invoices"); n != 0 {
			t.Errorf("off's Invoices = %d: a rule ran with rules switched off", n)
		}
	})
}
