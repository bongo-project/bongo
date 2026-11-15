package handlers

import (
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// newGroupComposeEnv returns a compose env with alice, bob, carol and a group
// team@example.com of bob and carol, wired through a real GroupStore.
func newGroupComposeEnv(t *testing.T) (*composeSendAsEnv, *store.GroupStore, *bongov1.Group) {
	t.Helper()
	env, _, _ := newPipelineEnv(t)
	env.addLocalUser(t, "u-dave", "dave@example.com")
	if err := env.ps.Set([]byte("/system/domain/example.com"), []byte("enabled")); err != nil {
		t.Fatal(err)
	}
	gs := store.NewGroupStore(env.ps, env.applier)
	g, err := gs.CreateGroup(&bongov1.Group{
		Address: "team@example.com", DisplayName: "Team",
		MemberIds: []string{"u-bob", "u-carol"},
	})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	env.handler.SetGroups(gs)
	return env, gs, g
}

func postGroupSend(env *composeSendAsEnv, id string, to ...string) (code int, body string) {
	w := postSendForm(env, "u-alice", "alice@example.com", id, url.Values{
		"to": {strings.Join(to, ", ")}, "subject": {"group"}, "body": {"hello"},
	})
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(b)
}

func envelopeRecipients(env *composeSendAsEnv) []string {
	var out []string
	for _, e := range env.spool.envelopes() {
		out = append(out, e.Recipients...)
	}
	slices.Sort(out)
	return out
}

func blobText(t *testing.T, env *composeSendAsEnv, hash string) string {
	t.Helper()
	rc, err := env.blobs.Get(hash)
	if err != nil {
		t.Fatalf("blob %s: %v", hash, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestComposeToGroupSpoolsOneEnvelopePerMember(t *testing.T) {
	env, _, g := newGroupComposeEnv(t)
	code, body := postGroupSend(env, sendID(), "team@example.com")
	if code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", code, body)
	}
	if got, want := envelopeRecipients(env), []string{"bob@example.com", "carol@example.com"}; !slices.Equal(got, want) {
		t.Fatalf("envelopes for %v, want %v", got, want)
	}
	envs := env.spool.envelopes()
	if envs[0].BlobHash != envs[1].BlobHash {
		t.Error("members do not share one blob")
	}
	memberBlob := blobText(t, env, envs[0].BlobHash)
	if !strings.HasPrefix(memberBlob, store.GroupLoopHeader+": "+g.Id+"\r\n") {
		t.Errorf("member blob is not stamped:\n%.200s", memberBlob)
	}
	// The group is never an external recipient.
	if pending, _ := env.qs.ListPending(10); len(pending) != 0 {
		t.Errorf("outbound queue holds %d entries, a group must not be relayed", len(pending))
	}
	// The Sent copy has no loop header.
	sent, _ := env.mailboxes.ListMessages("u-alice", "Sent")
	if len(sent) != 1 {
		t.Fatalf("Sent = %d, want 1", len(sent))
	}
	meta := env.mailMetaForSent(t, "u-alice")
	if blob := blobText(t, env, meta.RawMessage.Hash); strings.Contains(blob, store.GroupLoopHeader) {
		t.Error("the Sent copy carries the loop header")
	}
}

func TestComposeGroupDedupAndMaxRecipients(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	env.handler.SetMaxRecipients(2)
	// The group counts as one recipient, so group + one direct fits a cap of 2;
	// bob is also direct and gets a single copy.
	code, body := postGroupSend(env, sendID(), "team@example.com", "bob@example.com")
	if code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", code, body)
	}
	if got, want := envelopeRecipients(env), []string{"bob@example.com", "carol@example.com"}; !slices.Equal(got, want) {
		t.Fatalf("envelopes for %v, want %v", got, want)
	}
}

func TestComposeGroupOverQuotaMemberSkipped(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	env.handler.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{"u-carol": 0}})
	code, body := postGroupSend(env, sendID(), "team@example.com")
	if code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", code, body)
	}
	if got := envelopeRecipients(env); !slices.Equal(got, []string{"bob@example.com"}) {
		t.Fatalf("envelopes for %v, want bob only", got)
	}
}

func TestComposeGroupFanoutCap(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	lim := mail.NewFanoutLimiter(3)
	env.handler.SetGroupFanoutLimiter(lim)

	id := sendID()
	if code, body := postGroupSend(env, id, "team@example.com"); code != http.StatusSeeOther {
		t.Fatalf("first send %d: %s", code, body)
	}
	if got := lim.Remaining(groupFanoutKey("u-alice")); got != 1 {
		t.Fatalf("Remaining = %d, want 1 after two member copies", got)
	}
	// A retry of the same send places nothing and spends nothing.
	if code, body := postGroupSend(env, id, "team@example.com"); code != http.StatusSeeOther {
		t.Fatalf("retry %d: %s", code, body)
	}
	if got := lim.Remaining(groupFanoutKey("u-alice")); got != 1 {
		t.Errorf("retry spent cap: Remaining = %d, want 1", got)
	}
	if n := len(env.spool.envelopes()); n != 2 {
		t.Errorf("retry duplicated envelopes: %d, want 2", n)
	}

	// A new send needing two more copies with one left is refused, translated,
	// and writes nothing.
	sentBefore, _ := env.mailboxes.ListMessages("u-alice", "Sent")
	code, body := postGroupSend(env, sendID(), "team@example.com")
	if code != http.StatusTooManyRequests {
		t.Fatalf("over the cap: status %d, want 429", code)
	}
	if !strings.Contains(body, "group addresses") {
		t.Errorf("body = %q, want the translated fan-out message", body)
	}
	if n := len(env.spool.envelopes()); n != 2 {
		t.Errorf("a refused send spooled %d envelopes, want still 2", n)
	}
	if sentAfter, _ := env.mailboxes.ListMessages("u-alice", "Sent"); len(sentAfter) != len(sentBefore) {
		t.Error("a refused send stored a Sent copy")
	}
	if got := lim.Remaining(groupFanoutKey("u-alice")); got != 1 {
		t.Errorf("a refused send changed Remaining to %d", got)
	}
}

func TestComposeGroupWithoutExpanderIsRefusedNotRelayed(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	env.handler.SetGroups(nil)
	code, _ := postGroupSend(env, sendID(), "team@example.com")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", code)
	}
	if pending, _ := env.qs.ListPending(10); len(pending) != 0 {
		t.Errorf("a group was queued for relay: %d entries", len(pending))
	}
	if n := len(env.spool.envelopes()); n != 0 {
		t.Errorf("spooled %d envelopes", n)
	}
}

func TestComposeGroupEmptyIsRefused(t *testing.T) {
	env, gs, g := newGroupComposeEnv(t)
	if _, err := gs.UpdateGroup(&bongov1.Group{Id: g.Id, MemberIds: nil}); err != nil {
		t.Fatal(err)
	}
	code, body := postGroupSend(env, sendID(), "team@example.com")
	if code != http.StatusBadRequest || !strings.Contains(body, "no members") {
		t.Fatalf("status %d body %q, want 400 with the empty-group message", code, body)
	}
}

func TestSplitRecipientsClassesGroup(t *testing.T) {
	env, _, g := newGroupComposeEnv(t)
	local, external, groups := env.handler.splitRecipients([]string{"bob@example.com", "Team <team@example.com>", "far@remote.example"})
	if !slices.Equal(local, []string{"bob@example.com"}) || !slices.Equal(external, []string{"far@remote.example"}) ||
		len(groups) != 1 || groups[0].id != g.Id || groups[0].addr != "team@example.com" {
		t.Fatalf("local=%v external=%v groups=%v", local, external, groups)
	}
}

func TestDeliverGeneratedGroup(t *testing.T) {
	env, _, g := newGroupComposeEnv(t)
	lim := mail.NewFanoutLimiter(2)
	env.handler.SetGroupFanoutLimiter(lim)

	notReached, err := env.handler.DeliverGenerated("u-alice", "alice@example.com", "alice@example.com",
		[]string{"team@example.com", "bob@example.com"}, []byte(generatedRaw))
	if err != nil || len(notReached) != 0 {
		t.Fatalf("notReached=%v err=%v", notReached, err)
	}
	// bob is direct and a member: one copy; carol comes from the group.
	if got, want := envelopeRecipients(env), []string{"bob@example.com", "carol@example.com"}; !slices.Equal(got, want) {
		t.Fatalf("envelopes for %v, want %v", got, want)
	}
	if pending, _ := env.qs.ListPending(10); len(pending) != 0 {
		t.Errorf("a group was relayed: %d queue entries", len(pending))
	}
	for _, e := range env.spool.envelopes() {
		if e.Recipients[0] == "carol@example.com" &&
			!strings.HasPrefix(blobText(t, env, e.BlobHash), store.GroupLoopHeader+": "+g.Id) {
			t.Error("the member copy is not stamped")
		}
	}

	// One copy of cap was used; another group post needing two is not reached.
	notReached, err = env.handler.DeliverGenerated("u-alice", "alice@example.com", "alice@example.com",
		[]string{"team@example.com"}, []byte(generatedRaw))
	if err == nil || !slices.Equal(notReached, []string{"team@example.com"}) {
		t.Fatalf("over the cap: notReached=%v err=%v, want the group", notReached, err)
	}
	// And with no expander the group is not reached, not relayed.
	env.handler.SetGroups(nil)
	notReached, _ = env.handler.DeliverGenerated("u-alice", "alice@example.com", "alice@example.com",
		[]string{"team@example.com"}, []byte(generatedRaw))
	if !slices.Equal(notReached, []string{"team@example.com"}) {
		t.Fatalf("no expander: notReached=%v", notReached)
	}
	if pending, _ := env.qs.ListPending(10); len(pending) != 0 {
		t.Errorf("a group was relayed: %d queue entries", len(pending))
	}
}

// A group none of whose members can take the message is refused like a direct
// recipient whose mailbox is full, and nothing is sent or stored.
func TestComposeGroupAllMembersOverQuotaIsRefused(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	env.handler.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{"u-bob": 0, "u-carol": 0}})
	w := postSendForm(env, "u-alice", "alice@example.com", sendID(), url.Values{
		"to": {"team@example.com"}, "subject": {"group"}, "body": {"hello"},
	})
	body, _ := io.ReadAll(w.Result().Body)
	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("status %d, want 507: %s", w.Code, body)
	}
	if want := "Every member of team@example.com who can receive mail has a full mailbox"; !strings.Contains(string(body), want) {
		t.Errorf("body = %q, want the translated group quota message", body)
	}
	if n := len(env.spool.envelopes()); n != 0 {
		t.Errorf("spooled %d envelopes, want none", n)
	}
	if sent, _ := env.mailboxes.ListMessages("u-alice", "Sent"); len(sent) != 0 {
		t.Error("a refused send stored a Sent copy")
	}
}

// A member over quota who is also addressed directly is refused as that direct
// recipient; a group whose other members are reached by another route is not
// refused for the over-quota member alone.
func TestComposeGroupOverQuotaMemberCoveredElsewhere(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	env.handler.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{"u-carol": 0}})
	code, body := postGroupSend(env, sendID(), "bob@example.com", "team@example.com")
	if code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", code, body)
	}
	if got := envelopeRecipients(env); !slices.Equal(got, []string{"bob@example.com"}) {
		t.Fatalf("envelopes for %v, want bob only", got)
	}
}

// The fan-out cap is charged after every other refusal, so a send the message
// rate limiter refuses spends no fan-out allowance.
func TestComposeGroupRateLimitedSendSpendsNoFanout(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	lim := mail.NewFanoutLimiter(5)
	env.handler.SetGroupFanoutLimiter(lim)
	rl := mail.NewRateLimiter(1)
	rl.Allow("alice@example.com") // the hour's only message is spent
	env.handler.SetRateLimiter(rl)

	code, body := postGroupSend(env, sendID(), "team@example.com", "far@remote.example")
	if code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429 from the message rate limiter: %s", code, body)
	}
	if got := lim.Remaining(groupFanoutKey("u-alice")); got != 5 {
		t.Errorf("fan-out Remaining = %d, want 5: a refused send spent the allowance", got)
	}
	if n := len(env.spool.envelopes()); n != 0 {
		t.Errorf("spooled %d envelopes, want none", n)
	}
}

func TestDeliverGeneratedGroupAllMembersOverQuota(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	env.handler.SetQuotaChecker(&fakeQuota{headroom: map[string]int64{"u-bob": 0, "u-carol": 0}})
	notReached, err := env.handler.DeliverGenerated("u-alice", "alice@example.com", "alice@example.com",
		[]string{"team@example.com", "dave@example.com"}, []byte(generatedRaw))
	if err == nil || !slices.Equal(notReached, []string{"team@example.com"}) {
		t.Fatalf("notReached=%v err=%v, want the group", notReached, err)
	}
	if got := envelopeRecipients(env); !slices.Equal(got, []string{"dave@example.com"}) {
		t.Errorf("envelopes for %v, want dave only", got)
	}
}
