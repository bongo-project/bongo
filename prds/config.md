# Norman Config

## Session Limits
max_tasks_per_session: 15
warn_at_tasks: 12

## Project
name: bongo-go security follow-ups
repo: /home/user/dev/bongo-project/bongo-go/bongo-go
created: 2026-07-03

## Commands
build: go build ./...
test: go test ./...
lint: gosec ./...
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
