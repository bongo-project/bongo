# Loop Config

## Session Limits
max_tasks_per_session: 15
warn_at_tasks: 12

## Project
name: Sprint 13a - Security & Login Hardening
repo: /Users/user/dev/ai/oss/bongo-go/bongo-go
created: 2026-02-22

## Commands
build: go build -o bongo-go ./cmd/bongo-go
typecheck: go vet ./...
lint: golangci-lint run
test: go test -race ./...
generate: templ generate

## Source
prd_path: docs/sprints/sprint-13a-security-hardening.md

## Subagent Defaults
default_subagent: general-purpose
# Security tasks benefit from opus
# phase_2_subagent: security-auditor

## Model Strategy
default_model: sonnet
complex_model: opus
quick_model: haiku
auto_escalate: true
progress_compress_after: 10
