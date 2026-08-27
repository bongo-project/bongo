# Norman Subagent Reference

This file is read by the Haiku classifier during Step 3 of Mode 4 (Continue).
The orchestrator does NOT need this in its main context.

**Source of truth:** Available agents live in `.claude/agents/` in this repository. This file is a curated guide over that directory — if you add or remove an agent there, update the tables below. Haiku is instructed to verify the chosen `subagent_type` against the directory before returning a classification.

This set is deliberately trimmed to the stack this project actually uses: Go, htmx/Alpine with templ, the mail protocols, and a Docker deployment. Upstream norman ships many more (Python, Rust, Flutter, Kubernetes, ML and so on); they were left out rather than carried as noise. If you need one, copy it from <https://github.com/lhaig/norman> into `.claude/agents/` and add a row below.

To regenerate an authoritative list of agent names:
```
for f in .claude/agents/*.md; do grep -m1 "^name:" "$f" | sed 's/name: *//'; done | sort -u
```

**Built-in agents** (always available, not files): `general-purpose`, `Explore`, `Plan`, `claude`, `statusline-setup`, `claude-code-guide`.

---

## Advisor Pattern

Norman uses a three-tier advisor model:

| Role | Model | Purpose |
|------|-------|---------|
| **Advisor** | `advisor_model` (opus default) | Reviews plans before execution, reviews completed code, diagnoses failures |
| **Worker** | `default_model` (sonnet) | Implements all tasks (never the advisor model) |
| **Support** | `quick_model` (haiku) | Classifies tasks, gathers context, compresses progress |

Workers stay on the worker model. The advisor handles quality control through plan review (Step 3.1) and code review (Step 6.2). This separation means the strongest model spends tokens on reasoning about approach and quality rather than writing boilerplate. Note: all agent files in `.claude/agents/` use `model: inherit` in their frontmatter, so norman's explicit model parameter on the Agent tool (or the session model, outside norman) determines what they run on.

---

## Available Agent Types

### Language / Framework

| Area | subagent_type |
|------|---------------|
| Go | `golang-pro` |
| htmx / Alpine.js frontends | `htmx-alpine-pro` |

### Infrastructure / DevOps

| Domain | subagent_type |
|--------|---------------|
| CI/CD / Docker / deployment | `deployment-engineer` |
| DevOps troubleshooting | `devops-troubleshooter` |
| Network engineering (SMTP/IMAP/TLS/DNS) | `network-engineer` |

### Code Domain

| Domain | subagent_type |
|--------|---------------|
| API design | `backend-architect` |
| Architecture review | `architect-reviewer` |
| Code review | `code-reviewer` |
| Database query optimization (SQLite indexes, Bleve) | `database-optimizer` |
| Debugging non-obvious issues | `debugger` |
| Developer experience tooling | `dx-optimizer` |
| Error / log analysis | `error-detective` |
| Performance / profiling | `performance-engineer` |
| Security / auth | `security-auditor` |
| Testing | `test-automator` |

### Documentation

| Domain | subagent_type |
|--------|---------------|
| Long-form technical docs | `docs-architect` |

### General Purpose / Built-in

| Use case | subagent_type |
|----------|---------------|
| No specialist match | `general-purpose` |
| Context gathering / classification (Haiku) | `general-purpose` |
| Read-only codebase search | `Explore` |
| Implementation planning | `Plan` |

---

## Classification Guide

### Complexity Classification

The classifier returns a `COMPLEXITY` level used to decide whether the Opus advisor reviews the task (in `auto` mode):

Return **high** if ANY of these apply:
- Task involves security (auth, encryption, tokens, permissions, input validation)
- Task requires designing new architecture or significant refactoring across 5+ files
- Task involves complex algorithms, concurrency, or race conditions
- Task requires debugging a non-obvious issue
- Task modifies core infrastructure (database schemas, middleware, CI/CD)
- Task involves payment processing or financial logic
- Task is a performance investigation requiring profiling and tradeoff analysis
- Task is an active incident or production outage

Return **medium** if ANY of these apply:
- Task touches 3-4 files
- Task involves non-trivial business logic
- Task has integration points with external services
- Task modifies shared utilities or common code

Otherwise return **low**.

In `always` mode, the advisor reviews every task regardless of complexity. In `never` mode, the advisor is skipped entirely.

### Agent Selection

Pick the **most specific** agent that matches:
1. If the task is mostly Go, or mostly htmx/Alpine templates → language / framework specialist
2. If the task is infra/DevOps → infrastructure specialist
3. If the task domain dominates the language (writing tests, security audit, perf work) → domain specialist
4. If the task is documentation → documentation specialist
5. If nothing fits well → `general-purpose`

All workers run on Sonnet. The agent type determines the specialist prompt, not the model. The Opus advisor (Step 3.1, Step 6.2) handles complexity-based quality review separately — see Complexity Classification above.

### Verification Step (required)

Before returning a classification, verify the chosen `subagent_type` exists:
- It must be either a built-in (listed above), OR
- An agent with that `name:` in `.claude/agents/` in this repository

If the agent does not exist, fall back to `general-purpose` and note the missing agent in your `CONTEXT` field so the orchestrator can flag it.
