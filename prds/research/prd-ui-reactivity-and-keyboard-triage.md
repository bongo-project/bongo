# PRD: UI reactivity and keyboard-driven triage

Backlog: **B11**. Raised by the operator 2026-08-20: the interface is "not very
reactive" — new mail does not appear without a manual refresh, and unread counts
do not decrease as you read through messages.

Covers **both strands** of B11 by operator decision (2026-09-06): server-push
reactivity, and a keyboard triage loop that never needs the pointer. They are one
PRD because keyboard triage is precisely what exposes stale counts — you notice a
list is stale when you are moving through it three keys a second, not when you
click once a minute.

## 1. Introduction / Overview

The web UI has SSE already, and it does almost nothing. `/events` emits three
things: a `newmail` event that raises a desktop notification and touches no DOM,
an `oob` event carrying a single `#unread-badge` span, and calendar `reminder`s.

Concretely, today:

- **The stream is pinned to INBOX.** `bongo.js:601` opens
  `new EventSource('/events?mailbox=INBOX')` regardless of the folder on screen,
  so viewing Sent or a tag subscribes you to updates about INBOX.
- **The client cannot insert, only replace.** The `oob` handler builds a detached
  element and does `target.outerHTML = oobEl.outerHTML` by `id`, so it can swap a
  badge but cannot add a row to a list.
- **Message rows have no stable identity.** `MessageRows(messages, mailbox)`
  renders into `.msg-list` with nothing addressable per message.
- **Sidebar counts are rendered once.** `MailboxInfo.Unread` is computed
  server-side per request, with `aggregateUnread` rolling child folders into
  parents; nothing updates it afterwards.
- **Reading a message updates nothing** except on the next full page load.

The IMAP side of this problem was fixed separately (v0.6.28: `session.Poll` was a
stub, so no client polling with NOOP was ever told anything). The same underlying
notifier already exists and already carries the events this PRD needs —
`NotifNewMail`, `NotifExpunge`, `NotifFlagChange` — so this is largely a matter of
subscribing to the right mailbox and rendering what arrives.

## 2. Goals

- New mail appears in the open message list without a refresh.
- Per-folder unread counts in the sidebar stay correct as mail arrives and as
  messages are read, including changes made from another client or tab.
- A message read, moved or deleted elsewhere is reflected in an open list.
- A full triage loop — next, previous, open, archive, delete, mark read — is
  reachable from the keyboard with no pointer.
- Scroll position and selection survive every update.

## 3. User Stories

### US-001: New mail appears in the open list

**Description:** As a user reading mail, I want a message delivered to the folder
I am viewing to appear at the top of the list, so I do not have to refresh to
find out whether anything arrived.

**Acceptance criteria:**
- [ ] Given a user viewing `INBOX` with an open SSE stream, when a message is
      delivered to `INBOX`, then the stream emits an `oob` event whose data
      contains an element with `id="msg-{uid}"` and an
      `hx-swap-oob="afterbegin:#msg-list"` attribute.
- [ ] The emitted fragment contains the message's subject and sender.
- [ ] Given the same user viewing `Sent`, when a message is delivered to `INBOX`,
      then no row fragment for that message is emitted on their stream.
- [ ] After the swap, the number of elements matching `[id^="msg-"]` in
      `#msg-list` has increased by exactly one.

### US-002: The stream follows the folder being viewed

**Description:** As a user, I want updates for the folder I am actually looking
at, because today the stream is hardcoded to INBOX and every other folder is
silent.

**Acceptance criteria:**
- [ ] `GET /events?mailbox=Sent` subscribes to `Sent`, and a delivery to `Sent`
      produces an event on that stream.
- [ ] The client opens the stream with the mailbox currently rendered, not the
      literal string `INBOX`.
- [ ] Navigating from one folder to another closes the old stream and opens one
      for the new folder; a test asserts the server sees at most one subscription
      per session after the change.
- [ ] A mailbox name containing a `/` (a nested folder) subscribes correctly.

### US-002a: Desktop notifications keep working for INBOX

**Description:** As a user browsing any folder, I want to keep being notified
about new mail arriving in INBOX, because that is what the notification is for.

**Rationale.** Desktop popups already work: `bongo.js:603` raises a
`Notification` on the `newmail` event. They work *by accident of* the stream
being pinned to INBOX. US-002 removes that accident, and a naive implementation
would silently stop notifying while the user browses Sent or a tag — a
regression introduced by a change that otherwise looks like a pure improvement.
This story exists to make that failure impossible to ship unnoticed.

**Acceptance criteria:**
- [ ] Given a user viewing `Sent`, when a message is delivered to `INBOX`, then a
      `newmail` event is emitted on their stream carrying the sender and subject.
- [ ] That same delivery emits NO `msg-{uid}` row fragment, because INBOX is not
      the list on screen (this is US-001's third criterion, restated from the
      other side so the two cannot be satisfied by conflating them).
- [ ] Given a user viewing `INBOX`, a delivery to `INBOX` emits both the
      `newmail` event and the row fragment — exactly one notification, not two.
- [ ] A delivery to a folder that is neither INBOX nor the folder on screen emits
      no `newmail` event, so a busy filtered folder does not generate popups.
- [ ] A test asserts the Sent-viewing case, since it is the one that regresses.

### US-003: Sidebar counts update live

**Description:** As a user, I want each folder's unread count to change as mail
arrives and as I read it, so the sidebar is not quietly wrong.

**Acceptance criteria:**
- [ ] Given a sidebar showing `INBOX` with unread 3, when a message is delivered
      to `INBOX`, then an `oob` fragment with `id="count-INBOX"` and text `4` is
      emitted.
- [ ] When a message in `INBOX` is marked read, a fragment with `id="count-INBOX"`
      and text one lower than before is emitted.
- [ ] Counts for a nested folder update its parent's aggregate too, matching
      `aggregateUnread`'s existing roll-up: given `Work/Bugs` at 2 and `Work` at 0
      of its own, `count-Work` reads `2`.
- [ ] A count reaching zero emits an empty or hidden badge rather than the text
      `0`, matching how `#unread-badge` behaves today.

### US-004: Changes from another client are reflected

**Description:** As a user with Thunderbird open alongside the web UI, I want an
action in one to show up in the other, because the two disagreeing is worse than
either being slow.

**Acceptance criteria:**
- [ ] Given a message visible in an open web list, when it is marked read over
      IMAP, then the web stream emits a fragment for `id="msg-{uid}"` whose
      markup no longer carries the unread class.
- [ ] When a message is expunged or moved over IMAP, the stream emits an
      instruction that removes `#msg-{uid}` from the list.
- [ ] The same holds for actions taken in a second browser tab.

### US-005: The list survives updates

**Description:** As a user scrolled halfway down a list with a message selected, I
want an update not to throw away where I am.

**Acceptance criteria:**
- [ ] Given a list scrolled so that the first visible row is not the first row,
      when a new row is inserted at the top, then the previously visible rows
      remain in the DOM with their identities unchanged (asserted by `id`).
- [ ] A row that is currently selected keeps its selected state across an
      unrelated row being inserted or removed.
- [ ] No update replaces the whole `#msg-list` element.

### US-006: Keyboard navigation through a list

**Description:** As a user triaging mail, I want to move through the list from
the keyboard.

**Acceptance criteria:**
- [ ] `j` moves the selection to the next message; `k` to the previous.
- [ ] Selection is visible without a pointer — the selected row carries a class
      that is asserted in a test, and the row is scrolled into view.
- [ ] `j` at the last row and `k` at the first are no-ops rather than errors or
      wrap-arounds.
- [ ] `Enter` opens the selected message in the reading pane.
- [ ] Keys do nothing while focus is in a text input, a textarea or the composer.

### US-007: Keyboard triage actions

**Description:** As a user, I want to act on the selected message without the
mouse, so a zero-inbox pass is a sequence of keystrokes.

**Acceptance criteria:**
- [ ] `e` archives the selected message; `#` or `Delete` moves it to Trash;
      `u` toggles read/unread.
- [ ] After an action that removes the message from the current list, the
      selection moves to the next message, not to nothing.
- [ ] Each action updates the sidebar count via the same mechanism as US-003, with
      no full page load.
- [ ] `?` opens the existing shortcut-help modal, and it lists every shortcut this
      PRD adds.
- [ ] Every shortcut has an equivalent pointer action; nothing is keyboard-only.

### US-008: The mechanism is testable in CI

**Description:** As a maintainer, I want the reactivity proven by an automated
test, because a feature whose only check is "open two windows and look" regresses
silently.

**Acceptance criteria:**
- [ ] An integration test opens `/events` for a user, delivers a message through
      the real pipeline, reads the stream, and asserts on the emitted frames.
- [ ] The test asserts the frame's structure (event name, element `id`, swap
      attribute), not merely that some bytes arrived.
- [ ] Deleting the emit call from the handler makes the test fail. This is stated
      as a requirement because a test that passes against the pre-change code
      proves nothing, and this repository has shipped two such suites.
- [ ] The test does not require a browser or a JavaScript runtime.

## 4. Functional Requirements

- **FR-1:** `/events` must accept the mailbox to subscribe to and honour it,
  rather than the client hardcoding `INBOX`.
- **FR-1a:** Notification-worthy events for INBOX must reach the client
  regardless of which folder is subscribed for list updates. Whether that is a
  second subscription, a server-side special case, or a `notify` scope parameter
  is an implementation choice; what must not happen is notifications silently
  following the viewed folder.
- **FR-2:** The SSE handler must emit an out-of-band fragment for a new message in
  the subscribed mailbox, addressable as `msg-{uid}` and inserted with
  `hx-swap-oob="afterbegin:#msg-list"`.
- **FR-3:** Message rows must carry a stable `id="msg-{uid}"` in the templates
  that render them.
- **FR-4:** The SSE handler must emit per-folder unread count fragments
  addressable as `count-{mailbox}`, including parent aggregates.
- **FR-5:** The client `oob` handler must support insertion and removal, not only
  same-`id` replacement. Prefer delegating to HTMX's own OOB processing over
  extending the hand-rolled swap.
- **FR-6:** The handler must subscribe to `NotifFlagChange` and `NotifExpunge` in
  addition to `NotifNewMail`, and emit the corresponding fragments.
- **FR-7:** Updates must not replace or re-render `#msg-list` as a whole.
- **FR-8:** Keyboard handlers must be inert while focus is in an editable field.
- **FR-9:** The shortcut-help modal must be generated from the same source as the
  bindings, so it cannot drift out of date.
- **FR-10:** Integration tests must cover FR-2, FR-4 and FR-6 by reading the
  stream, and each must fail if its emit is removed.

## 5. Non-Goals

- Replacing HTMX or introducing a frontend framework.
- WebSockets. SSE is already present, is one-directional, and suffices.
- Reactivity outside mail: calendar, contacts and admin are unchanged beyond the
  existing reminder events.
- Offline support or optimistic local mutation.
- Threading or conversation-view changes.
- Push notifications beyond the desktop `Notification` already wired to `newmail`.
- Changing IMAP behaviour. That half is done (v0.6.28, v0.6.30).

## 6. Technical Considerations

- **The notifier already carries what is needed.** `internal/imap.Notifier`
  publishes `NotifNewMail`, `NotifExpunge` and `NotifFlagChange`, and the SSE
  handler already subscribes to it. Most of this work is rendering, not plumbing.
- **`NotifExpunge` has no producer today.** Noted during the IMAP work: nothing
  currently emits it, so `Idle`'s expunge branch is unreachable. US-004 needs a
  producer, which is a change on the mutation paths rather than in the handler.
- **Fragment rendering should reuse the templates.** The row markup exists in
  `MessageRows`; emitting a hand-built `<tr>` string in Go would drift from it
  within a release. Render the same templ component to a buffer.
- **Sidebar aggregation already exists** in `aggregateUnread`
  (`internal/web/handlers/sidebar.go:181`) and must not be reimplemented, or
  parent counts will diverge between first paint and live update.
- **`disableStreamTimeouts` exists for a reason.** SSE responses clear the
  server's write deadline; a new streaming endpoint must do the same or the
  browser reconnects in a loop after 60s.
- **One stream per session.** Switching folders must close the previous
  `EventSource`; leaking one per navigation will exhaust the browser's per-origin
  connection limit within a few clicks.
- **Alpine, not vanilla.** The project's convention is to fix Alpine patterns
  rather than convert them to plain JS, and keyboard state (selection index) is
  Alpine-shaped.
- **i18n.** Every user-visible string added, including shortcut descriptions, goes
  through the existing translation helper; the project requires a second grep pass
  for hardcoded strings after template edits.

## 7. Success Metrics

- Delivering a message to the folder on screen makes a row appear with no user
  action, within one second.
- Reading a message decrements the sidebar count for its folder and every
  ancestor, with no page load.
- An action taken in Thunderbird is visible in an open web list without a refresh.
- A full triage pass — open, act, next — is completable with no pointer input.
- Each of FR-2, FR-4 and FR-6 has a test that fails when its emit is removed.

## 8. Open Questions

- **What produces `NotifExpunge`?** US-004's removal case needs a producer on the
  expunge and move paths. Whether that belongs in this work or as a prerequisite
  is a scoping decision for import.
- **Insert position for a new row.** `afterbegin` is correct for a newest-first
  list, which is the default, but a list sorted oldest-first or by sender would
  need the position computed. Is non-default sorting in scope?
- **Rate limiting.** A bulk delivery or an IMAP move of 500 messages would emit
  500 fragments. Should the handler coalesce, and if so on what interval?
- **What happens to a row that no longer matches the current filter?** Marking a
  message read while viewing an unread-only view should presumably remove it, but
  that is a product decision.
- **Two concerns, one stream.** Notifications and list updates want different
  scopes: notifications care about INBOX always, list updates care about the
  folder on screen. Options are a second `EventSource`, a `notify=INBOX`
  parameter alongside `mailbox=`, or having the server always include INBOX
  arrivals as `newmail` while scoping row fragments to the subscription. The last
  keeps one connection, which matters given the per-origin limit noted above.
- **Reconnection.** `EventSource` reconnects automatically, but events during the
  gap are lost and the list is then silently stale. Should reconnect trigger a
  one-shot resync, and does that reintroduce the full-list re-fetch this design
  otherwise avoids?
