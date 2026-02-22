# Sprint 12c: Mail Filter Rules

**Phase:** 1 (Foundation)
**Goal:** Add a per-user server-side mail filter rules engine with web UI for creating,
managing, and testing rules that route inbound mail before mailbox placement.

---

## Context

Sprint 12b modified the delivery pipeline to support spam-based routing (Junk vs INBOX).
The `placeInMailbox()` function now reads a target mailbox instead of hardcoding INBOX.
This sprint builds on that by adding user-defined filter rules that run after spam
scoring but before final mailbox placement.

**Existing infrastructure:**
- Delivery pipeline: `internal/smtp/delivery.go` → `Deliver()` with spam scoring hook
- FSM placement: `internal/cluster/fsm.go` → `placeInMailbox()` now accepts target mailbox
- MailMeta: includes spam_score, spf_result, dkim_result, all message headers
- Pebble key patterns: `/user/{id}/...` for per-user data
- Preferences handler: `internal/web/handlers/preferences.go` — pattern for settings UI
- Settings template: `internal/web/templates/settings.templ` — tabs/fieldsets pattern
- Mailbox store: `MailboxStore` with ListMailboxes(), MoveMessage(), CreateMailbox()
- Alpine.js available for dynamic form interactions
- HTMX for partial swaps

---

## Cleanup from Sprint 12b

### 0a. Wire Bayesian blending into delivery pipeline

**Why:** The Bayesian filter trains via Mark as Spam / Not Spam but its `Classify()`
method is never called during inbound scoring. The heuristic score is used alone.

**Modify `internal/smtp/delivery.go` → `DeliverWithSPF()`:**
- After computing heuristic score, check if BayesFilter has been trained (>20 messages):
  - Extract body text, tokenize via `BayesFilter.Tokenize(subject, bodyText)`
  - Call `BayesFilter.Classify(userID, tokens)` → bayesScore
  - Blend: `finalScore = 0.6*heuristicScore + 0.4*bayesScore`
  - If not enough training data: use heuristic score only
- Set `meta.SpamScore = finalScore`

**Done when:** After training 20+ messages, inbound spam scoring uses blended
heuristic + Bayesian score. Without training data, falls back to heuristic only.

### 0b. Add auto-junk info logging in FSM

**Why:** `placeInMailbox()` silently routes to Junk without logging.

**Modify `internal/cluster/fsm.go` → `placeInMailbox()`:**
- When routing to Junk (spam_score >= threshold), log at info level:
  `logger.Info("auto-junk", "score", meta.SpamScore, "from", meta.From, "subject", meta.Subject)`

**Done when:** Junk routing is visible in server logs at info level.

### 0c. Add TestBayesianTraining integration test

**Why:** No integration test verifies that Bayesian training affects scoring.

**Add to `test/integration/antispam_test.go`:**
- `TestBayesianTraining`:
  1. Train BayesFilter with 25+ spam messages (common spam tokens)
  2. Train BayesFilter with 25+ ham messages (normal tokens)
  3. Deliver new message with spam-like content
  4. Verify blended score is higher than heuristic-only score would be

**Done when:** Test passes, confirming Bayesian feedback loop works end-to-end.

---

## Tasks

### 1. FilterRule proto and rule store

Define the rule data model and storage layer.

**Create `proto/bongo/v1/rules.proto`:**
```protobuf
syntax = "proto3";
package bongo.v1;
option go_package = "bongo-go/proto/bongo/v1";

message FilterRule {
  string id = 1;
  string name = 2;
  int32 priority = 3;            // lower = runs first
  bool enabled = 4;
  repeated Condition conditions = 5;
  FilterAction action = 6;
  bool stop_processing = 7;      // skip remaining rules if matched
  int64 created_at = 8;
  int64 updated_at = 9;
}

message Condition {
  string field = 1;              // "from", "to", "subject", "body", "header:X-List-Id", "size", "has_attachment"
  string operator = 2;           // "contains", "equals", "matches", "not_contains", "greater_than", "less_than"
  string value = 3;
}

message FilterAction {
  string type = 1;               // "move", "copy", "flag", "mark_read", "delete"
  string target = 2;             // folder name for move/copy, flag name for flag
}
```

**Run `proto/generate.go`** to regenerate.

**Create `internal/rules/store.go`:**
- `RuleStore` struct wrapping PebbleStore + RaftApplier
- Key pattern: `/user/{userID}/rule/{ruleID}` → FilterRule proto bytes
- `List(userID string) ([]*bongov1.FilterRule, error)` — prefix scan, decode, sort by priority
- `Get(userID, ruleID string) (*bongov1.FilterRule, error)`
- `Save(userID string, rule *bongov1.FilterRule) error` — marshal + Raft apply
- `Delete(userID, ruleID string) error` — Raft apply delete
- `Reorder(userID string, ruleIDs []string) error` — update priority field on each rule

**Done when:** FilterRule proto compiles. RuleStore can CRUD rules in Pebble via Raft.
List returns rules sorted by priority.

### 2. Rule evaluation engine

Evaluate filter rules against inbound messages.

**Create `internal/rules/engine.go`:**
- `Engine` struct (stateless, just methods)
- `Evaluate(rules []*bongov1.FilterRule, meta *bongov1.MailMeta, headers map[string]string, bodyText string) *bongov1.FilterAction`:
  - Iterate rules in priority order (already sorted by store)
  - Skip disabled rules
  - For each rule: check ALL conditions (AND logic)
  - If all conditions match: return rule's action
  - If `stop_processing` is true on matched rule: stop immediately
  - If no rules match: return nil (caller uses default: INBOX or Junk from spam score)

- `matchCondition(cond *bongov1.Condition, meta, headers, bodyText) bool`:
  - Field resolution:
    - `"from"` → meta.From
    - `"to"` → strings.Join(meta.To, ", ")
    - `"cc"` → strings.Join(meta.Cc, ", ")
    - `"subject"` → meta.Subject
    - `"body"` → bodyText
    - `"has_attachment"` → strconv.FormatBool(len(meta.Attachments) > 0)
    - `"size"` → message size as string
    - `"header:X-Foo"` → headers["X-Foo"]
  - Operator matching (case-insensitive for string ops):
    - `"contains"` → strings.Contains(lower(field), lower(value))
    - `"not_contains"` → !strings.Contains(...)
    - `"equals"` → strings.EqualFold(field, value)
    - `"matches"` → regexp.MatchString(value, field) (compile with timeout)
    - `"greater_than"` → numeric comparison (for size)
    - `"less_than"` → numeric comparison (for size)

**Create `internal/rules/engine_test.go`:**
- Rule matches "from contains newsletter" against matching message → returns action
- Multiple conditions all must match (AND)
- Disabled rule skipped
- Stop processing prevents further evaluation
- No matching rules → nil
- Regex matching works
- Case insensitive contains

**Done when:** Engine correctly evaluates rules against messages. AND logic, priority
ordering, stop_processing, and all operators work. Unit tests pass.

### 3. Hook rules into delivery pipeline

Integrate rule evaluation between spam scoring and mailbox placement.

**Modify `internal/smtp/delivery.go` → `Deliver()`:**
- After spam scoring block, before delivery loop:
  1. Load recipient's filter rules: `ruleStore.List(recipientUserID)`
  2. Extract body text from parsed message (for body matching)
  3. Call `engine.Evaluate(rules, meta, headers, bodyText)`
  4. If action returned:
     - `"move"` → set target mailbox to action.Target
     - `"copy"` → set target mailbox to action.Target + flag for INBOX copy
     - `"flag"` → set flag bit on MailMeta.Flags
     - `"mark_read"` → set Seen flag on MailMeta.Flags
     - `"delete"` → set target mailbox to "Trash"
  5. If no action: use spam-based default (Junk if score > threshold, else INBOX)

**Modify delivery to pass target mailbox:**
- Add `TargetMailbox string` to the Raft command or MailMeta delivery context
- `placeInMailbox()` reads target from command instead of computing it

**Wire RuleStore into agent:**
- Add `RuleStore *rules.RuleStore` to Agent struct
- Initialize in `NewAgent()` from PebbleStore + RaftApplier
- Pass to DeliveryPipeline

**Done when:** A rule "From contains newsletter → move to Updates" correctly routes
matching inbound mail to Updates folder. Rules evaluated after spam scoring. Default
routing (INBOX/Junk) used when no rules match.

### 4. Filter rules web UI — list and CRUD

Web interface for managing rules.

**Create `internal/web/handlers/rules.go`:**
- `RulesHandler` struct with RuleStore, MailboxStore references
- `ListRules(w, r)` — GET `/settings/rules`:
  - Load rules for user, load mailbox list (for action dropdowns)
  - Render rules list template
- `NewRule(w, r)` — GET `/settings/rules/new`:
  - Render empty rule form with mailbox list
- `CreateRule(w, r)` — POST `/settings/rules`:
  - Parse form: name, conditions[], action type + target, enabled, stop_processing
  - Generate UUID, set priority = max_existing + 1, set created_at
  - Save via RuleStore
  - HTMX redirect to rules list with flash
- `EditRule(w, r)` — GET `/settings/rules/{id}`:
  - Load rule, render form pre-filled
- `UpdateRule(w, r)` — PUT `/settings/rules/{id}`:
  - Parse form, update fields, set updated_at
  - Save via RuleStore
  - HTMX partial swap
- `DeleteRule(w, r)` — DELETE `/settings/rules/{id}`:
  - Delete via RuleStore
  - HTMX remove row from list
- `ReorderRules(w, r)` — POST `/settings/rules/reorder`:
  - Accept JSON array of rule IDs in new order
  - Update priority on each rule
  - Return updated list

**Register routes in agent.go:**
```
r.Route("/settings/rules", func(r chi.Router) {
    r.Get("/", rulesH.ListRules)
    r.Get("/new", rulesH.NewRule)
    r.Post("/", rulesH.CreateRule)
    r.Get("/{id}", rulesH.EditRule)
    r.Put("/{id}", rulesH.UpdateRule)
    r.Delete("/{id}", rulesH.DeleteRule)
    r.Post("/reorder", rulesH.ReorderRules)
})
```

**Done when:** Admin can navigate to Settings → Rules, see rule list, create a new
rule with conditions and action, edit existing rules, delete rules, reorder via
drag-and-drop.

### 5. Filter rules web UI — templates and interactions

Templates and Alpine.js interactions for the rules UI.

**Create `internal/web/templates/rules.templ`:**

- `RuleList(rules []*RuleInfo, flash string)`:
  - Table: drag handle (⠿), priority #, name, conditions summary (e.g. "From
    contains 'newsletter'"), action summary (e.g. "Move to Updates"), enabled
    toggle, actions (edit, test, delete)
  - Drag-and-drop reordering via Sortable.js (same as message drag-drop)
  - POST `/settings/rules/reorder` on drop with new order
  - Empty state: "No filter rules yet. Create one to automatically organize mail."

- `RuleForm(rule *RuleInfo, mailboxes []string, isNew bool)`:
  - Name input
  - **Conditions section** (Alpine.js `x-data="{conditions: [...]}"`)
    - Dynamic rows: each row has field dropdown, operator dropdown, value input
    - Field options: From, To, CC, Subject, Body, Has Attachment, Size, Header (custom)
    - Operator options change based on field (contains/equals/matches for text,
      greater_than/less_than for size, equals for has_attachment)
    - "Add condition" button (Alpine `@click="conditions.push({...})"`)
    - "Remove" button per row (Alpine `@click="conditions.splice(i, 1)"`)
  - **Action section:**
    - Type dropdown: Move to folder, Copy to folder, Flag, Mark as read, Delete
    - Target: folder dropdown (shown for move/copy), flag selector (for flag)
  - Checkboxes: Enabled (default true), Stop processing after match
  - Save button (POST or PUT)

- `RuleTestResults(matches int, total int, samples []MailSummary)`:
  - "Matched {matches} of {total} recent messages"
  - Sample matches: subject, from, date (clickable to view)

**Add "Rules" link to settings navigation** (modify settings.templ or sidebar):
- Tab/link: Appearance | Display | Composition | Localization | **Rules**

**Done when:** Rule form has dynamic condition rows (add/remove), field-dependent
operator dropdowns, folder/flag target selection. Rules list supports drag reorder.

### 6. Rule testing

Allow users to test rules against existing mail without applying actions.

**Add to `internal/web/handlers/rules.go`:**
- `TestRule(w, r)` — POST `/settings/rules/{id}/test`:
  - Load the rule
  - Load user's recent messages (last 100 from all mailboxes)
  - For each message: load headers from raw blob, extract body text
  - Run `engine.Evaluate([rule], meta, headers, bodyText)`
  - Count matches, collect first 10 as samples
  - Render `RuleTestResults` partial via HTMX

**Add test button to rule list and rule form:**
- List: "Test" button per rule → POST, swap results into inline area
- Form: "Test this rule" button → POST with current form values (preview before save)

**Done when:** User clicks "Test" on a rule → sees "Matched 12 of 100 recent messages"
with sample subjects. Works from both list view and edit form.

### 7. Integration tests

**Create `test/integration/filters_test.go` (build tag `integration`):**

**TestFilterRuleRouting:**
1. Start dev agent, create user
2. Create "Updates" mailbox via web UI
3. Create filter rule: from contains "newsletter" → move to "Updates"
4. Deliver email with `From: newsletter@example.com`
5. Wait for Raft propagation
6. Verify message landed in "Updates" (not INBOX)

**TestFilterRulePriority:**
1. Create two rules:
   - Priority 1: subject contains "urgent" → move to "Important"
   - Priority 2: from contains "boss@" → flag as Starred
2. Deliver email from boss@ with subject "urgent report"
3. Verify: moved to "Important" (rule 1 matched first with stop_processing)

**TestFilterRuleDisabled:**
1. Create rule: from contains "test" → move to Trash (disabled: true)
2. Deliver email from test@example.com
3. Verify message in INBOX (rule was disabled)

**TestFilterRuleCRUD:**
1. Login as admin
2. POST `/settings/rules` → create rule
3. GET `/settings/rules` → verify rule in list
4. PUT `/settings/rules/{id}` → update name
5. GET `/settings/rules/{id}` → verify updated
6. DELETE `/settings/rules/{id}` → verify removed

**TestFilterRuleWithSpam:**
1. Create rule: from contains "news" → move to "News"
2. Deliver email from news@example.com with SPF fail + no DKIM
3. Verify: rule takes precedence over spam routing (goes to "News" not "Junk")
   (user-defined rules override spam routing)

**Done when:** `go test -race -tags=integration ./test/integration/filters_test.go` passes.

---

## Exit Criteria

- [ ] Bayesian blending wired into delivery pipeline (0.6 heuristic + 0.4 Bayes when trained)
- [ ] Auto-junk info logging in placeInMailbox()
- [ ] TestBayesianTraining integration test passes
- [ ] FilterRule proto with conditions, action, priority, stop_processing, timestamps
- [ ] RuleStore CRUD operations via Raft (list, get, save, delete, reorder)
- [ ] Rule engine evaluates conditions in priority order (AND logic within rule)
- [ ] All operators work: contains, not_contains, equals, matches, greater_than, less_than
- [ ] Rules hook into delivery pipeline after spam scoring, before mailbox placement
- [ ] User-defined rules override spam-based routing
- [ ] Filter rules web UI: list with drag-and-drop reorder
- [ ] Rule form: dynamic condition rows (add/remove via Alpine.js)
- [ ] Rule form: field-dependent operator dropdowns
- [ ] Create, edit, delete rules via web UI
- [ ] Rule test: shows match count + samples against recent mail
- [ ] "Rules" tab in settings navigation
- [ ] `go test -race ./...` passes
- [ ] Filter integration tests pass (routing, priority, disabled, CRUD, spam override)

## What NOT to do in this sprint

- No Sieve DSL parser (proto-based rules are simpler and sufficient)
- No auto-reply action (requires outbound relay integration, defer)
- No forward action (requires outbound relay integration, defer)
- No vacation responder (complex, defer to future)
- No rule import/export (Sieve format, future)
- No "apply rules to existing mail" batch operation (future enhancement)
- No OR logic between conditions (AND only for v1; user creates separate rules for OR)
