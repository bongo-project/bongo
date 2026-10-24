# Norman Config

## Session Limits
max_tasks_per_session: 15
warn_at_tasks: 12

## Project
name: bongo-go
repo: .
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
# Both tools must run under the go.mod toolchain: under the local Go 1.27,
# golangci-lint fails its typecheck and gosec silently loads nothing and exits
# 0 (found 2026-10-22, when CI's gosec caught G118 that "clean" local runs had
# missed).
lint: GOTOOLCHAIN=go1.27.3 make lint && GOTOOLCHAIN=go1.27.3 gosec -quiet -exclude-generated -severity=medium ./...
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
