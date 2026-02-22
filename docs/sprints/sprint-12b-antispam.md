# Sprint 12b: Antispam Scoring

**Phase:** 1 (Foundation)
**Goal:** Add inbound spam scoring with DKIM verification, heuristic analysis, Bayesian
learning, and automatic Junk folder routing. Also add "Mark as Spam / Not Spam" actions
to the web UI.

---

## Context

Sprint 12a added POP3 server. The delivery pipeline currently places all inbound mail
into INBOX with no scoring or classification. SPF checking exists on inbound (result
stored in Received-SPF header). DKIM signing exists for outbound but no inbound
verification. The Junk mailbox is auto-created per user but never used.

**Existing infrastructure:**
- SPF checker: `internal/mail/spf.go` — integrated in SMTP session, result in header
- DKIM signer: `internal/mail/dkim.go` — outbound only
- Delivery pipeline: `internal/smtp/delivery.go` → `Deliver()` — parses message, creates
  MailMeta, delivers via Raft. Filter hook point is between parsing and delivery loop.
- FSM placement: `internal/cluster/fsm.go` → `placeInMailbox()` — hardcodes INBOX
- MailMeta proto: fields 1–13, no spam fields
- Junk mailbox: in `DefaultMailboxes`, auto-created per user
- Bleve search: `internal/store/search.go` — `MailDocument` struct, `IndexMail()`
- Web UI mail handler: `internal/web/handlers/mail.go` — bulk actions, context menu
- Antispam config exists: `AntispamConfig{Builtin{Enabled:true}, Rspamd{...}}`

---

## Tasks

### 1. Spam scoring fields in MailMeta proto

Add spam-related fields so scoring results are persisted and searchable.

**Modify `proto/bongo/v1/mail.proto`:**
```protobuf
float spam_score = 14;       // 0.0 (ham) to 1.0 (spam)
string spf_result = 15;      // "pass", "fail", "softfail", "neutral", "none"
string dkim_result = 16;     // "pass", "fail", "none"
string dkim_domain = 17;     // signing domain from d= tag
```

**Run `proto/generate.go`** to regenerate `.pb.go`.

**Modify `internal/store/search.go`:**
- Add `SpamScore float64 json:"spam_score"` to `MailDocument` struct
- Include in index mapping as numeric field
- Update `IndexMail()` to populate SpamScore from MailMeta

**Done when:** MailMeta proto has all four spam fields. Bleve indexes spam_score
as a numeric field. Queries like `spam_score:[0.5 TO 1.0]` work.

### 2. Inbound DKIM verification

Verify DKIM signatures on inbound messages.

**Create `internal/antispam/dkim_verify.go`:**
- `VerifyDKIM(rawMessage []byte) (result string, domain string, err error)`
- Parse `DKIM-Signature` header from message:
  - Extract `d=` (domain), `s=` (selector), `bh=` (body hash), `b=` (signature),
    `h=` (signed headers), `c=` (canonicalization), `a=` (algorithm)
- If no DKIM-Signature header: return `("none", "", nil)`
- Fetch public key via DNS TXT: `{selector}._domainkey.{domain}`
- Parse `p=` base64 public key from TXT record
- Verify body hash: canonicalize body per `c=` tag, compute hash, compare to `bh=`
- Verify header signature: canonicalize signed headers per `c=` tag, verify `b=`
  against public key using algorithm from `a=` (rsa-sha256 or ed25519-sha256)
- Return `("pass", domain, nil)` or `("fail", domain, nil)`

**Create `internal/antispam/dkim_verify_test.go`:**
- Unit test with a known-good DKIM-signed message (generate test fixture)
- Test missing DKIM-Signature → "none"
- Test tampered body → "fail"

**Done when:** `VerifyDKIM()` correctly validates real DKIM signatures via DNS lookup
and returns pass/fail/none with the signing domain.

### 3. Heuristic spam scorer

Score inbound messages based on multiple signals.

**Create `internal/antispam/scorer.go`:**
- `Scorer` struct: threshold float32 (default 0.5), weights configurable
- `ScoringInput` struct: SPFResult, DKIMResult, Headers map[string]string,
  Subject, BodySample string (first 4KB), EnvelopeSender, EnvelopeRecipient
- `ScoringResult` struct: Score float32, Reasons []string
- `Score(input *ScoringInput) ScoringResult` — accumulate score from checks:
  - SPF fail: +0.20, softfail: +0.10
  - DKIM fail: +0.20, none: +0.10
  - Missing Message-ID header: +0.15
  - Suspicious Date (>24h future or >7d past): +0.10
  - Empty subject: +0.05
  - Envelope sender domain ≠ From header domain: +0.15
  - Known spam phrases in subject (configurable word list): +0.10 each (cap at +0.30)
  - Cap total score at 1.0
- Each triggered check appends to Reasons for transparency

**Create `internal/antispam/scorer_test.go`:**
- Clean message (SPF pass, DKIM pass, proper headers) → score < 0.2
- Suspicious message (SPF fail, no DKIM, missing Message-ID) → score > 0.5
- Edge cases: empty input, all checks triggered

**Done when:** Scorer produces 0.0–1.0 scores. Clean mail scores low, suspicious
mail scores high. Reasons list explains the score.

### 4. Hook scoring into delivery pipeline

Integrate DKIM verification and spam scoring into inbound mail delivery.

**Modify `internal/smtp/delivery.go` → `Deliver()`:**
- After message parsing (~line 87), before the recipient delivery loop (~line 131):
  1. Call `antispam.VerifyDKIM(rawBytes)` → dkimResult, dkimDomain
  2. Get SPF result from SMTP session (passed through delivery context)
  3. Build `ScoringInput` from parsed headers + SPF + DKIM results
  4. Call `scorer.Score(input)` → ScoringResult
  5. Set on MailMeta: `SpamScore`, `SpfResult`, `DkimResult`, `DkimDomain`

**Modify `internal/cluster/fsm.go` → `placeInMailbox()`:**
- Instead of hardcoding `INBOX`:
  - Read `meta.SpamScore` from MailMeta
  - If `spam_score >= threshold` (from config, default 0.5): target = `"Junk"`
  - Else: target = `"INBOX"`
- Log at info level when auto-junking: `"auto-junk: score=%.2f from=%s"`

**Pass SPF result through delivery context:**
- Add `SPFResult string` field to `DeliveryPipeline` or pass via context.Context
- Set from SMTP session's SPF check result

**Done when:** Inbound mail with SPF fail + no DKIM + missing Message-ID gets
score > 0.5 and lands in Junk. Clean mail lands in INBOX. Spam score persisted
in MailMeta and indexed in Bleve.

### 5. Mark as Spam / Not Spam with Bayesian learning

Add spam actions to web UI and train a per-user Bayesian filter.

**Create `internal/antispam/bayes.go`:**
- `BayesFilter` struct with PebbleStore reference
- Storage keys: `/user/{id}/bayes/ham/{token}` → count (varint),
  `/user/{id}/bayes/spam/{token}` → count (varint)
- `Tokenize(subject, bodyText string) []string`:
  - Lowercase, split on whitespace + punctuation
  - Filter tokens < 3 chars and common stopwords
  - Deduplicate, return sorted
- `Train(userID string, tokens []string, isSpam bool) error`:
  - For each token: increment spam or ham count via Raft apply
- `Classify(userID string, tokens []string) float64`:
  - For each token: compute P(spam|token) using token counts
  - Combine using naive Bayes (log probabilities to avoid underflow)
  - Return probability 0.0–1.0
- Blended final score in delivery: `final = 0.6*heuristic + 0.4*bayesian`
  (only if user has trained > 20 messages, otherwise heuristic only)

**Add to `internal/web/handlers/mail.go`:**
- `MarkSpam(w, r)` — POST `/mail/{mailbox}/{uid}/spam`:
  - Move message to Junk via `MoveMessage()`
  - Load raw message, extract subject + body text, tokenize
  - Call `BayesFilter.Train(userID, tokens, true)`
  - Update MailMeta.SpamScore to 1.0 via Raft
  - HTMX: remove row from message list (OOB swap)
- `MarkNotSpam(w, r)` — POST `/mail/{mailbox}/{uid}/not-spam`:
  - Move message to INBOX via `MoveMessage()`
  - Tokenize, call `BayesFilter.Train(userID, tokens, false)`
  - Update MailMeta.SpamScore to 0.0 via Raft
  - HTMX: remove row from message list

**Modify mail list template:**
- In Junk folder: show "Not Spam" button on each message row
- In other folders: add "Mark as Spam" to context menu and bulk actions dropdown
- Show spam score badge on messages in Junk view (e.g. "Score: 0.72")

**Done when:** "Mark as Spam" moves to Junk + trains Bayes. "Not Spam" moves to
INBOX + trains Bayes. Spam score badge visible in Junk. Over time, Bayesian
classification improves routing accuracy.

### 6. Integration tests

**Create `test/integration/antispam_test.go` (build tag `integration`):**

**TestSpamScoringAutoJunk:**
1. Start dev agent
2. Deliver email via SMTP with: SPF fail (use external sender domain),
   no DKIM, missing Message-ID header
3. Wait for Raft propagation
4. Verify message in Junk (not INBOX)
5. Verify MailMeta.SpamScore > 0.5

**TestCleanMailToInbox:**
1. Start dev agent
2. Deliver email from admin@localhost (SPF pass for localhost, proper headers)
3. Verify message in INBOX
4. Verify MailMeta.SpamScore < 0.5

**TestMarkAsSpamAndNotSpam:**
1. Deliver email to INBOX
2. Login as admin, POST `/mail/INBOX/{uid}/spam`
3. Verify message now in Junk, SpamScore updated to 1.0
4. POST `/mail/Junk/{uid}/not-spam`
5. Verify message back in INBOX, SpamScore updated to 0.0

**TestBayesianTraining:**
1. Train Bayes with 25+ spam messages (common spam tokens)
2. Train Bayes with 25+ ham messages (normal tokens)
3. Deliver new message with spam-like content
4. Verify blended score is higher than heuristic-only score

**Done when:** `go test -race -tags=integration ./test/integration/antispam_test.go` passes.

---

## Exit Criteria

- [ ] MailMeta proto has spam_score, spf_result, dkim_result, dkim_domain fields
- [ ] Inbound DKIM verification via DNS TXT lookup (pass/fail/none)
- [ ] Heuristic scorer produces 0.0–1.0 score with reasons
- [ ] Spam score > threshold auto-routes to Junk folder
- [ ] `placeInMailbox()` no longer hardcodes INBOX (uses spam score to decide)
- [ ] Spam score indexed in Bleve as numeric field
- [ ] "Mark as Spam" button in mail UI (moves to Junk + trains Bayes)
- [ ] "Not Spam" button in Junk view (moves to INBOX + trains Bayes)
- [ ] Spam score badge visible on messages in Junk folder
- [ ] Bayesian filter trained by user actions, blended with heuristic score
- [ ] SPF result passed from SMTP session into delivery pipeline
- [ ] `go test -race ./...` passes
- [ ] Antispam integration tests pass (auto-junk, clean mail, mark spam, Bayes)

## What NOT to do in this sprint

- No Rspamd integration (config exists, defer to future)
- No ClamAV virus scanning (defer to future)
- No DMARC checking (builds on SPF + DKIM, defer to future)
- No per-sender whitelist/blacklist (future enhancement)
- No spam reporting to external services
- No mail filter rules (Sprint 12c)
