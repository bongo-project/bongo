# PRD: Per-component log levels v0.15.14

Status: imported as Phase 38. Ships in v0.15.14 with the INFO login lines (PR #207).

## 1. Overview

Bongo has one log level for the whole process (`telemetry.log.level`, default
`info`, hot on reload). An operator chasing a problem in one feature has to raise
the level for every feature, which floods the log. This PRD adds a level per
component, so one feature can be made more verbose while the rest stay quiet.

Nearly every log message already starts with its component name and a colon
(`imap: login succeeded`, `queue: retry scheduled`). The component is taken from
that prefix, or from a `component` attribute, so no call site changes.

## 2. Goals

- Set a log level per component in the config file.
- Change it on a config reload, without a restart.
- Leave existing installs unchanged: with no components listed, behaviour is
  exactly the global level as today.

## 3. User Stories

### US-001: Per-component level
**Description:** As an operator, I want to set `imap = "debug"` while the rest of
the server logs at `warn`, so I can troubleshoot one feature without noise.

**Acceptance Criteria:**
- [ ] `telemetry { log { components = { imap = "debug" } } }` sets the level for
      messages whose prefix is `imap:` or whose `component` attribute is `imap`.
- [ ] Messages from components not listed, and messages with no prefix, use
      `telemetry.log.level`.
- [ ] A component can be set more verbose or less verbose than the global level.
- [ ] Levels are `debug`, `info`, `warn`, `error`. There is no `off`: errors are
      always logged (operator decision 2).
- [ ] An unknown component name or level fails config validation with a message
      naming the key, at startup and on reload (a rejected reload changes nothing).
- [ ] The admin log viewer shows the same lines as the log output.

### US-002: Hot reload
**Description:** As an operator, I want to change component levels with a
reload, so I can turn detail on and off without dropping connections.

**Acceptance Criteria:**
- [ ] `telemetry.log.components` is a hot key: a reload applies it and reports it
      in "config reload complete".
- [ ] Removing a component from the map returns it to the global level.

### US-003: Documentation
**Acceptance Criteria:**
- [ ] `deploy/LOGGING.md` lists the component names and the troubleshooting
      pattern (run at `warn` or `info`, raise one component to `info` or `debug`,
      reload, lower it again).
- [ ] `deploy/bongo.example.hcl` shows a commented example.
- [ ] `docs/design/config-reload.md` lists the new hot key.

## 4. Functional Requirements

- FR-1: `LogConfig.Components map[string]string` (`hcl:"components,optional"`).
- FR-2: The known component names are one list in the config package. A test
  scans the source for message prefixes and fails if a prefix is missing from
  the list or the list names a prefix no longer used.
- FR-3: A filtering handler sits outermost, above the admin log sink, and holds
  the global level and the component map behind atomics so a reload never
  races a log call. `Enabled` passes the lowest configured level; `Handle`
  applies the record's component level.
- FR-4: Config diff and clone handle map fields.

## 5. Non-Goals

- Turning off error logging for a component.
- Per-user or per-connection log levels.
- Renaming existing message prefixes or grouping them into larger features.

## 6. Decisions

1. (operator, 2026-11-14) The default stays `info`; existing installs are unchanged.
2. (operator, 2026-11-14) The quietest component level is `error`; there is no `off`.
3. (operator, 2026-11-14) Ships in the next release, v0.15.14.
