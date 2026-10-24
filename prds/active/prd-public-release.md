# PRD: Public release of the repository

## 1. Introduction / Overview

`bongo-project/bongo` is private. The operator decided on 2026-10-24 to make
it public: it unblocks CI (GitHub-hosted runners are free for public
repositories) and opens the project. This PRD is the preparation. It runs
alongside v0.12.0 and does not change the server's behaviour beyond a source
link that AGPL asks for.

A check of all 624 commits on 2026-10-24 found no private keys, no GitHub,
Slack or AWS tokens, and no real secret in any committed config
(`deploy/bongo.prod.hcl` has only ever held `example.com` placeholders).
What it did find is in section 3.

| Phase | Stories | TASKS rows |
|-------|---------|------------|
| Public release | US-001 to US-007 | 26.1 to 26.7 |

## 2. Goals

- The repository carries a licence: AGPL-3.0.
- No third-party personal data and no operational detail of the production
  deployment remains in the tree or in the rewritten history.
- The open security items that would be published are known and accepted.
- CI runs safely on a public repository.

## 3. User Stories

### US-001: The repository is licensed AGPL-3.0
**Description:** there is no `LICENSE` file, which on a public repository
means all rights reserved. Decided 2026-10-24: AGPL-3.0, so anyone who runs
a modified Bongo as a network service must offer its source.

**Acceptance Criteria:**
- [ ] `LICENSE` holds the unmodified AGPL-3.0 text.
- [ ] README states the licence and links the file.
- [ ] The web UI shows a "Source" link (footer or About) to the repository,
      as AGPL section 13 expects of a network service; the URL is a config
      value with the public repository as default, so a modified deployment
      can point at its own source.
- [ ] A dependency licence check (every module in `go.mod` and every
      vendored static asset in `internal/web/static/`) finds nothing
      incompatible with AGPL-3.0; the result is recorded in the PR.

### US-002: Demo data is synthetic
**Description:** `testdata/demo/` (66 mail files and mboxes, calendars,
contacts) comes from the original Bongo project: 2006 Ximian mailing-list
mail with real people's names and addresses. Decided 2026-10-24: replace it.

**Acceptance Criteria:**
- [ ] New demo mail, calendars and contacts use invented people and
      `example.com`/`example.org` addresses, written for this project.
- [ ] They exercise the same paths: threads, attachments, HTML and plain
      text, mbox and single files, recurring and all-day events, vCard 2.1
      and 3.0, enough volume for the UI (20+ mails, 10 contacts, 5 events).
- [ ] `bongo-import directory testdata/demo/` imports them cleanly, and
      every test and script that reads `testdata/demo` passes.
- [ ] The original files are removed from history in US-004.

### US-003: Operational and personal details are scrubbed from the tree
**Description:** `prds/` and `docs/` record operational detail from the
production deployment: the host name and how it is reached, real sender and
recipient addresses, internal user and message ids, paths in the private
infra repository.

**Acceptance Criteria:**
- [ ] A list of every occurrence (file, line, what it is) is made first and
      kept in the PR description.
- [ ] Each is replaced with a neutral description ("the production host",
      `user@example.com`, "an internal message id") that keeps the meaning
      of the record.
- [ ] A grep for the production host name, the operator's address, hosted
      customer domains and the infra repository name finds nothing outside
      `.git`.
- [ ] The same patterns are written down as the replacement list for
      US-004.

### US-004: History is rewritten
**Description:** the scrub must also hold for history. Decided 2026-10-24:
rewrite in place and force-push, accepting that GitHub keeps pull-request
refs (`refs/pull/*/head`) to the old commits, so anyone who knows an old
commit SHA can still read it. What those commits hold is the operator's own
address, a customer sender address, an internal user id and message id, the
host name, and the 2006 demo mail; no secrets or sign-in tokens.

**Acceptance Criteria:**
- [ ] Done with `git filter-repo` on a fresh mirror clone: remove the old
      `testdata/demo` files from every commit, and apply the US-003
      replacement list to every blob and commit message.
- [ ] Open work is merged or rebased first; the operator confirms before
      the force-push of `main`, branches and tags.
- [ ] After the push, a fresh clone passes the US-003 grep across
      `git log --all -p`, builds and passes `make test-all`.
- [ ] Release tags are re-pointed at their rewritten commits; the published
      container images keep their old revision labels (note in README or
      release notes).
- [ ] Local worktrees and branches from before the rewrite are deleted.

### US-005: The open security items are reviewed before publishing
**Description:** `docs/security-review-*.md` and section 8.2 of the
hardening PRDs list open findings while production faces the internet.

**Acceptance Criteria:**
- [ ] Every open item is listed with one of: fixed (commit), not
      exploitable in production (reason), accepted for now (reason), or to
      fix before publishing.
- [ ] The unverified report that the web session middleware lets ordinary
      routes through when the account state read fails is checked, and
      fixed before publishing if true.
- [ ] `SECURITY.md` names the reporting channel and is correct for a public
      repository.

### US-006: CI is safe on a public repository
**Acceptance Criteria:**
- [ ] No workflow uses `pull_request_target` or exposes secrets to pull
      requests from forks; checked and recorded.
- [ ] `CI_RUNNER` stays unset, so pull requests run on GitHub-hosted
      runners, not on the self-hosted runner.
- [ ] The operator sets "Require approval for all outside collaborators"
      for fork pull-request workflows (repository setting).

### US-007: The README reads as a public project page
**Acceptance Criteria:**
- [ ] The name is consistent: the project is Bongo, the binaries stay
      `bongo-go` and `bongo-import` (renaming binaries is out of scope).
- [ ] It says what Bongo is, its status, how to run it, the licence, how to
      report security issues, and links to the website.
- [ ] Nothing in it refers to private repositories or internal sessions.

## 4. Functional Requirements

FR-1 to FR-7 are the acceptance criteria of US-001 to US-007.

## 5. Non-Goals (Out of Scope)

- The website (`bongo-project/bongo-project`, `gh-pages`) and the help
  documentation, using `bongo-project/oldosulwebsite` as a source: a later
  PRD.
- Renaming the binaries or the Go module.
- Removing Claude session links from commit messages.

## 6. Technical Considerations

- US-004 rewrites every commit SHA. Open pull requests and worktrees must
  be finished or recreated afterwards, and release tags move.
- The demo data replacement (US-002) must land before the rewrite, so the
  rewrite only deletes the old files.
- `bongo-project/oldosulwebsite` is a MediaWiki tree that includes
  `LocalSettings.php` and `AdminSettings.php`; check them for credentials
  before reusing anything from it (website PRD).

## 7. Success Metrics

- The repository is public with a licence, synthetic demo data and a clean
  grep over all history.
- CI runs on GitHub-hosted runners for pull requests.

## 8. Open Questions

### 8.1 Decisions (operator, 2026-10-24)

1. Licence: AGPL-3.0.
2. Publish by rewriting in place and force-pushing, accepting that PR refs
   keep the old commits reachable by SHA.
3. Replace the demo data with synthetic data.
4. The website and help documentation come after the repository is public.

### 8.2 Candidates for later

Findings from reviews during this work go here.

From the 26.x work (2026-10-24):

- Order changed: v0.12.0 must be deployed to production before publishing,
  because the repository documents fixes that are live on v0.11.0 (26.5).
- vCard 2.1 `EMAIL;INTERNET:addr` loses the address on import: go-vcard keeps
  it as a parameter key with an empty value, and `parseVCardBlock` reads only
  the value. The existing unit test builds the card by hand and misses it.
  (26.2)
- The handlers test package takes about 240 s alone and hit Go's 10-minute
  default timeout under load; GitHub's standard runners are slower than the
  development machine. Handled in 25.2. (26.1)
- The ComponentLib tokens in `internal/web/static/css/componentlib/` are the
  operator's own design and out of date; refresh them with the website work.

