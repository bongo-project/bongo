package handlers

import (
	"net/http"
	"testing"
)

// A reused send ID with a different From must be refused. Otherwise a
// restored form could switch to another verified identity and the second send
// would be reported as sent under the first one.
func TestComposeSameSendIDChangedFromConflicts(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	const alt = "alice@d2.com"
	env.seedAddress(t, alt, uid, "d2.com", true, true)

	id := sendID()
	first := idemFields("from check")
	first.Set("from", email)
	if w := postSendForm(env, uid, email, id, first); w.Code != http.StatusSeeOther {
		t.Fatalf("first status=%d: %s", w.Code, w.Body.String())
	}
	assertOneCopyEach(t, env, uid)
	spoolBefore := len(env.spool.envelopes())

	second := idemFields("from check")
	second.Set("from", alt)
	w := postSendForm(env, uid, email, id, second)
	if w.Code != http.StatusConflict {
		t.Fatalf("changed-From resubmit status=%d want 409: %s", w.Code, w.Body.String())
	}
	if n := len(env.spool.envelopes()); n != spoolBefore {
		t.Errorf("envelopes changed %d -> %d", spoolBefore, n)
	}
	assertOneCopyEach(t, env, uid)
}

// A local recipient listed in both To and Cc is delivered once.
func TestComposeLocalRecipientInToAndCcGetsOneEnvelope(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	f := idemFields("dup rcpt")
	f.Set("to", "bob@example.com")
	f.Set("cc", "bob@example.com")
	if w := postSendForm(env, uid, email, sendID(), f); w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	envs := env.spool.envelopes()
	if len(envs) != 1 {
		t.Fatalf("envelopes=%d want 1", len(envs))
	}
	if len(envs[0].Recipients) != 1 || envs[0].Recipients[0] != "bob@example.com" {
		t.Errorf("recipients=%v", envs[0].Recipients)
	}
}
