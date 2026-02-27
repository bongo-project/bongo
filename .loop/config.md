# Loop Config

## Session Limits
max_tasks_per_session: 15
warn_at_tasks: 12

## Project
name: Message Processing Pipeline
repo: /Users/user/dev/ai/oss/bongo-go/bongo-go
created: 2026-02-27

## Commands
build: make build
typecheck: go vet ./...
lint: golangci-lint run
test: go test -race ./...
generate: templ generate
sec: gosec ./...

## Source
prd_path: tasks/prd-message-processing-pipeline.md

## Subagent Defaults
default_subagent: general-purpose

## Model Strategy
default_model: sonnet
complex_model: opus
quick_model: haiku
auto_escalate: true
progress_compress_after: 10
