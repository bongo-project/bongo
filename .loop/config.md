# Loop Config

## Session Limits
max_tasks_per_session: 15
warn_at_tasks: 12

## Project
name: Message Tags
repo: /Users/user/dev/ai/oss/bongo-go/bongo-go
created: 2026-02-28

## Commands
build: make build
test: go test ./...
lint: go vet ./...
generate: templ generate

## Source
prd_path: tasks/prd-message-tags.md

## Subagent Defaults
default_subagent: general-purpose

## Model Strategy
default_model: sonnet
complex_model: opus
quick_model: haiku
auto_escalate: true
progress_compress_after: 10

## Security
security_scan: gosec -quiet ./... 2>&1 | grep -E "HIGH|MEDIUM" | head -10
block_on: HIGH
warn_on: MEDIUM
