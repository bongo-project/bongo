# AGENTS.md

Guidance for AI coding assistants working in this repository. Claude Code reads
`CLAUDE.md`, which is more detailed; this file carries the same essentials in the
tool-neutral format other assistants look for. If the two ever disagree,
`CLAUDE.md` is authoritative.

## What this is

Bongo: a mail and calendar server — IMAP, SMTP, POP3, CalDAV/CardDAV — with an
HTMX web UI, embedded storage and Raft-based clustering. One Go binary.

Module path: `github.com/bongo-project/bongo`.

## Build and test

```bash
make generate     # templ generate - REQUIRED before build after touching .templ
make build        # generate + build bin/bongo and bin/bongo-import
make test         # go test ./...          (fast; SKIPS integration)
make test-race    # go test -race ./...
make test-integration   # behind //go:build integration, ~6 min, starts real servers
make test-all     # fmt-check + test-race + test-integration - run before pushing
make lint         # golangci-lint run
make sec          # gosec ./...
make dev          # build, run a dev server, seed test data
```

`make test` alone does **not** run the integration suite — it is behind a build
tag. The suite once stayed red for a month because a plain `go test ./...` looked
green. Use `make test-all` before you push.

Integration tests start agents through `sharedStartAgentErr`, which fails with
Run's own error when startup fails. An `agent not ready in time` message used to
hide such errors; if a start fails now, read the error rather than re-running.

## Architecture in one screen

Three storage tiers:

- **Pebble** — metadata, the source of truth. Small protobufs holding blob
  references. Replicated through Raft.
- **Blob store** — content-addressed files on disk, SHA-256 as filename. One flat
  shared directory, not per-user, so identical messages delivered to several
  people are stored once. Synced between nodes over HTTP, not the Raft log.
- **SQLite + Bleve** — per-user derived query indexes. Rebuildable from Pebble
  plus blobs, so they are never authoritative.

Every write goes through Raft. Handlers never write to Pebble directly — they
call `ApplyCommand()`, which puts a serialised `RaftCommand` through the log; the
FSM applies committed entries. If you find yourself writing to Pebble from a
handler, that is the bug.

Blob deletion happens **only** in the GC sweep. Blobs are shared across users, so
deleting one from any single owner's code path destroys everyone's copy.

## Conventions

- `internal/` for everything private; public API only under `cmd/`
- Wrap errors with `fmt.Errorf("context: %w", err)`; sentinel errors only where
  `errors.Is()` needs them
- Define interfaces at the consumer, not the implementer
- Table-driven tests, `t.TempDir()` for storage, `httptest` for handlers, `-race`
  always
- Imports grouped stdlib, third-party, internal, separated by blank lines
- Static assets are local minified files in `internal/web/static/` — never a CDN
- No emojis in code or commit messages; conventional commit prefixes

## Things that will bite you

- **Generated protobuf files cannot be text-edited.** `.pb.go` embeds a
  length-prefixed binary descriptor; changing a string inside it corrupts the
  length prefix. This compiles fine and then panics at `init()` with a
  slice-bounds error. Regenerate with `protoc` instead.
- **`templ generate` before building** after touching any `.templ`.
- **Chi route ordering:** static segments before wildcards, or `/mail/tag/{name}`
  gets swallowed by `/mail/{mailbox}`.
- **Multi-step Raft mutations are not atomic** unless implemented as a composite
  command. Do not describe them as atomic if they are several `ApplyCommand()`
  calls; either make it composite or document the partial-failure behaviour.
- **Do not trust `X-Forwarded-For`** except from a configured trusted proxy.
- Use HTMX for HTML fragment swaps and Alpine `fetch()` for JSON endpoints — not
  the other way round.

## Security expectations

This is a mail server that runs on the public internet, so:

- Never ignore `ApplyCommand()` errors on a path the user is told succeeded.
  Best-effort writes need a comment saying so, sane behaviour without them, and a
  logged failure.
- Never log secrets, tokens, generated passwords or session material.
- Set cookie `HttpOnly`, `Secure` and `SameSite` explicitly; never rely on
  browser defaults.
- Cookie-authenticated mutating routes need CSRF protection or strict origin
  checks.
- New `http.Server` instances need explicit timeouts.
- Validate control characters out of anything reaching a protocol boundary —
  `TrimSpace` only strips the ends, and an embedded CRLF becomes header
  injection.

Run `make sec` and address HIGH and MEDIUM findings before proposing a merge.

## Workflow

The repository ships its own workflow skills in `.claude/`, used by Claude Code.
Other assistants can ignore that directory; the task and progress files it reads
live in `prds/` and are plain markdown you can follow along with.

Please do not add yourself as a commit co-author.
