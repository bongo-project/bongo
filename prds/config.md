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
default_model: sonnet
complex_model: opus
quick_model: haiku
auto_escalate: true
progress_compress_after: 10
