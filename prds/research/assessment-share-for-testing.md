# Assessment: readiness to share Bongo with external testers

**Date:** 2026-08-19. Assessment only — nothing here has been changed.

## Summary

The project is in better shape than a first-time sharer usually is: no secrets in the repo, a
working `.gitignore`, a README with four quickstarts, a binary install guide, green CI, and zero
`TODO`/`FIXME` markers in non-test code. The gaps are mostly *legal and hygiene*, not quality.

Three things would actually block or embarrass a share, and they are all small.

## Blockers

### 1. No LICENSE — this is the real blocker

There is no `LICENSE`, `CONTRIBUTING.md`, `SECURITY.md` or `CODE_OF_CONDUCT.md`.

Without a licence file the code is **all rights reserved by default**. A tester who clones it has
no legal permission to run, modify or redistribute it, and anyone whose employer has a policy on
this will not touch it. Whatever you intend — permissive, copyleft, source-available,
"evaluation only" — it has to be stated. This is a one-file fix and it gates everything else.

A `SECURITY.md` matters more than usual here, because it is a mail server: testers *will* find
issues, and they need to know where to send them rather than opening a public issue.

### 2. Deployment-specific data is still in the code

The B1 de-branding pass missed these, or they were re-added afterwards:

| Location | Content |
|---|---|
| `internal/web/handlers/dns_guide_test.go:22-26` | a real domain, **the production IP**, a DKIM public-key fragment |
| `test/integration/alias_delivery_test.go:96,115` | `lance@example.net` — a real personal address |
| `internal/web/handlers/dns_guide.go:20` | `example.net` in a doc comment |
| `internal/web/handlers/admin_test.go:54,281` | comments referencing the external mail provider by name |

None is a secret — the IP and DKIM record are public DNS. But shipping your production IP and a
personal address in test fixtures tells every tester exactly which host to point at, and the
provider-specific comments are meaningless to anyone outside your head. Replace with
`example.com` / `192.0.2.1` (RFC 5737 documentation range), which is what the rest of the repo
already uses.

### 3. The module path does not match the repository

`go.mod` declares `module github.com/bongo-go/bongo-go`, but the repo is `github.com/lhaig/bongo-go`.

A tester running `go install github.com/bongo-go/bongo-go/cmd/bongo-go@latest` gets a failure,
and `go get` of the module path will not resolve. Either move the repo to a `bongo-go` org, set up
a vanity import path, or change the module to match. Worth deciding before people start cloning,
because changing it later breaks anyone who already imported it.

## Not blockers, but tell testers up front

- **Unimplemented surfaces.** The `agent` role implements SMTP only — `imap`, `pop3` and `caldav`
  log "not yet implemented" and silently do nothing. Each is a one-line note in the README.
- **The repo is private.** Sharing means either making it public (see the licence question) or
  adding collaborators. Note that GitHub Actions minutes are billable on private repos, and CI
  currently runs on every push.

## Already good — do not redo these

- No secrets in the repo. The only `password` hit is a commented placeholder in
  `deploy/bongo.example.hcl`.
- `.gitignore` covers `/data/`, `/deploy/.env`, `/deploy/secrets/`.
- README has quickstarts for development, standalone binary, single-server production and
  multi-domain, plus a documentation index and an architecture section.
- `docs/install.md` covers a clone-to-running path from the binary release.
- `make dev` gives a newcomer a zero-config server.
- Zero `TODO`/`FIXME`/`XXX` markers in non-test code.
- CI is green across lint, test, security, integration and build — and `make test-all` now runs
  what CI runs, so a contributor can check before pushing.

## Suggested order

1. **LICENSE** — decide and add. Nothing else matters until this exists.
2. **Scrub the deployment-specific values** — small, mechanical, and stops your production IP
   travelling with the code.
3. **Settle the module path** — cheap now, disruptive later.
4. **SECURITY.md** — where to report, and what is in scope.
5. **README note on unimplemented surfaces** — sets tester expectations honestly.

Items 2–5 are perhaps half a day together. Item 1 is a decision, not work.
