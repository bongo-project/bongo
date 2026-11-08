# PRD: Unified application shell and UI quality

## Context

Bongo's individual web screens are visually polished, but Mail, Calendar,
Contacts, Tasks, Settings and Admin each replace the sidebar with a different
navigation model. The mail sidebar also gives primary folders, virtual views,
custom folders, settings and application switching equal visual weight. On a
populated account the application links fall below the viewport.

The current frontend has several related quality gaps: mutations can fail
silently, the notification permission prompt obscures content, some HTMX swaps
move focus unexpectedly, mobile tabs and tables can clip, and route-specific
CSS has accumulated as a final override layer over legacy rules.

## Goals

- One stable application shell and application switcher on every authenticated
  screen.
- Contextual navigation that changes by application without changing the user's
  overall mental map.
- A scannable mail sidebar that separates primary mailboxes, secondary
  mailboxes, virtual views, folders and tags.
- Visible success and failure feedback for every client-side mutation.
- Keyboard, screen-reader, reduced-motion, mobile and dark-mode behavior treated
  as component requirements.
- Visual verification that includes populated, long, error and mobile states.

## Phases

### Phase A: Shell and high-impact defects

- [x] Add a shared application switcher to every authenticated sidebar.
- [x] Reorganize the mail sidebar into primary Mail, collapsible More,
      collapsible Views, Folders and Tags.
- [x] Keep the app switcher fixed while contextual navigation scrolls.
- [x] Bind mobile navigation's expanded state and current-page semantics.
- [x] Replace the blocking notification prompt with a dismissible prompt that
      is legible in both themes and does not cover mobile controls.
- [x] Fix the desktop-notification icon URL.
- [x] Add shared client-side status feedback; no user mutation may silently
      discard an HTTP or network error.
- [x] Add reduced-motion behavior.

### Phase B: Shared navigation migration

- [x] Migrate Calendar, Contacts, Tasks, Settings, Security, Rules, Tags and
      Admin to the shared sidebar frame.
- [x] Remove route-specific "Back to Mail" navigation as the only app escape.
- [x] Preserve contextual sidebar scroll and collapsed state.
- [x] Keep sidebar dimensions, active states, icons and mobile drawer behavior
      consistent across applications.

### Phase C: Screen and responsive fixes

- [x] Rebuild the calendar event editor with the shared page header, centered
      form card, sensible maximum width and visible mobile actions.
- [x] Make Settings tabs complete ARIA tabs with keyboard navigation and a
      visible mobile overflow affordance.
- [x] Make Admin tables usable on mobile without silently clipping columns.
- [x] Align Tasks mobile metadata and actions.
- [x] Improve Rules and Contacts empty states.
- [x] Auto-scroll Week and Day calendar views to a useful time and soften the
      full-day current-date treatment.
- [x] Confirm destructive publish operations.

### Phase D: Reactivity and accessibility

- [ ] Complete the existing mail reactivity PRD: viewed-folder subscription,
      row insertion/removal/update, count refresh and reconnect resync.
- [x] Centralize keyboard shortcut definitions and help rendering.
- [x] Scope HTMX focus movement to page-level navigation and explicit dialogs.
- [x] Restore focus after dialogs and menus close.
- [ ] Add automated accessibility checks, 200% zoom, forced-colour and
      reduced-motion coverage.

### Phase E: Frontend consolidation and verification

- [ ] Split the monolithic JS and CSS by subsystem without adding a build step.
- [ ] Remove superseded selectors and the legacy polish override layer.
- [ ] Add and document a generator for generated_geometry.css.
- [ ] Capture full pages, dark mobile, tablet, open mobile navigation and
      populated edge states in the screenshot harness.
- [ ] Add interaction coverage for drawers, collapsible groups, tabs, dialogs,
      failure feedback and keyboard-only flows.

## Acceptance criteria

- Every authenticated route exposes the same application destinations in the
  same order and keeps the current application selected.
- At 1440x900 the app destinations remain visible with a long folder tree.
- At 390x844 the drawer opens, traps navigation appropriately, reports its
  expanded state and exposes the same hierarchy as desktop.
- No tested route has a console, CSP or same-origin asset error in light, dark
  or mobile capture.
- Every client-side mutation has a visible failure state and a test that fails
  when error handling is removed.
- Keyboard and screen-reader users can identify the current page, operate tabs,
  dismiss prompts and return focus to the initiating control.
- `prefers-reduced-motion: reduce` disables non-essential animation.

## Testing strategy

- Template tests pin structure, ARIA relationships and navigation ordering.
- JavaScript tests pin error handling, focus restoration and shortcut behavior.
- Handler tests cover any new fragments or status endpoints.
- `make generate`, targeted Go tests, `make test`, the Deslop diff scan and
  `make ui-shots` gate each implementation batch.

## Risks

- Navigation changes touch most templates. Migrate through a shared component
  while preserving route-specific tools until each screen is verified.
- HTMX partial navigation can leave stale client state. Derive current state
  from rendered data attributes and re-run idempotent initializers after swaps.
- Large CSS cleanup can cause subtle regressions. Remove legacy rules only after
  the replacement component is captured in all supported variants.
