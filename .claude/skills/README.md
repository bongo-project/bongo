# Vendored Claude Code skills

These skills give contributors the same development workflow the project is
built with. Claude Code discovers them automatically when working in this
repository — no installation step.

| Skill | What it does |
|---|---|
| `norman` | Plan, execute and verify work with crash recovery and session persistence. Drives the other two. |
| `prd` | Generate a requirements document. Invoked as `norman prd`. |
| `sweep` | Post-implementation cleanup: prove tests guard the invariants they claim, and strip stale task scaffolding from comments. |

The workflow, in short:

```
norman prd        # write requirements into prds/research/
norman import     # move the PRD to backlog, extract tasks into TASKS.md
norman            # execute the next task
norman verify     # validate the implementation against the PRD
norman sweep      # clean up afterwards
```

The task and progress files these commands read and write live in `prds/`.

## Provenance

Vendored from <https://github.com/lhaig/norman>, MIT licensed — see `LICENSE`
in this directory. They are a copy, not a submodule, so contributors get the
workflow without a second checkout. That means they can drift from upstream:
if you change something here that is generally useful, please send it upstream
too.

Only the skill definitions are vendored. Upstream also ships agent definitions
and a statusline that are not needed to work on this repository.
