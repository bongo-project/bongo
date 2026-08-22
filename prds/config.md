# Norman Config

## Session Limits
max_tasks_per_session: 15
warn_at_tasks: 12

## Project
name: bongo-go
repo: /home/user/dev/bongo-project/bongo-go/bongo-go
created: 2026-07-03

## Commands
build: go build ./...
# `go test ./...` SKIPS the integration suite entirely and still reports success,
# because those tests sit behind a `-tags=integration` build tag. Workers told to
# run it get false confidence — that is how a change which broke 10 integration
# tests passed unit tests cleanly in Phase 12. test-all is what CI runs
# (fmt-check + race + integration, ~6 min), so a worker that passes locally
# passes CI.
test: make test-all
lint: gosec ./...
# Not a canonical norman key — kept deliberately. templ compiles .templ files to
# Go, so a build after touching any template fails without it.
generate: templ generate

## Subagent Defaults
default_subagent: golang-pro

## Model Strategy
advisor_mode: always        # always | auto | never — controls advisory review
advisor_model: opus         # plan review, code review, diagnosis, verification
default_model: sonnet
quick_model: haiku
progress_compress_after: 10
verify_rigor: adversarial   # single | adversarial — Mode 5 verification depth
