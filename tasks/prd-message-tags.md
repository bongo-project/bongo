# PRD: Message Tags (Hybrid Folder + Tag Model)

## Introduction

Add a lightweight tagging system to Bongo-Go that supplements the existing folder model. Messages continue to live in exactly one mailbox (preserving IMAP compatibility), but can carry multiple user-defined tags. Tags power new virtual views in the sidebar, replace hardcoded virtual mailboxes with a general mechanism, and enable smarter import deduplication and organisation — without breaking any IMAP client.

## Goals

- Allow users to assign one or more tags to any message (e.g. "important", "project-x", "receipts")
- Expose tags as virtual views in the web UI sidebar
- Map IMAP custom keywords to tags so standards-compliant clients can read/write them
- Provide auto-tagging via filter rules (existing rule engine)
- Enable retroactive bulk tagging from the web UI
- Keep the folder model as primary — tags are additive, not a replacement
- No regression for IMAP, POP3, or CalDAV/CardDAV clients

## Non-Goals

- Full Gmail-style label-as-folder model (messages still belong to one mailbox)
- Tag-based mailbox routing in SMTP delivery (use filter rules instead)
- Nested/hierarchical tags (flat namespace only, keep it simple)
- Shared tags across users (tags are per-user)
- Tag-based permissions or ACLs

## Architecture Decision: Why Hybrid

A pure label model (Gmail-style) where "folders" are just labels has significant downsides:

1. **IMAP incompatibility** — RFC 9051 expects folders. Multi-label messages appear duplicated to every IMAP client (Thunderbird, Apple Mail, Outlook). Gmail's X-GM-LABELS extension is proprietary and unsupported by most clients.
2. **Key space redesign** — Current Pebble layout (`/user/{id}/mailbox/{name}/msg/{uid}`) assumes one mailbox per message. Changing this is a large migration.
3. **Counter complexity** — Message counts become non-trivial when a message belongs to multiple "folders".

The hybrid approach gives 80% of the benefit:
- Messages live in one folder (IMAP works perfectly)
- Tags are metadata on the message (like IMAP keywords)
- The web UI can show tag-based views that feel like Gmail labels
- Import can tag messages instead of duplicating them across folders

## User Stories

### US-001: View and manage tags on a message
**Description:** As a user, I want to see which tags a message has and add/remove tags, so I can organise my mail beyond folder structure.

**Acceptance Criteria:**
- [ ] Message detail view shows current tags as coloured chips
- [ ] Clicking a tag chip opens that tag's virtual view
- [ ] "Add tag" button opens a dropdown with existing tags + free-text input
- [ ] Tags can be removed by clicking the X on a chip
- [ ] Changes persist immediately (no save button)
- [ ] Tags visible in message list as small coloured dots (max 3, tooltip shows tag name on hover)

### US-002: Tag-based virtual views in sidebar
**Description:** As a user, I want my tags to appear as clickable views in the sidebar, so I can quickly see all messages with a given tag.

**Acceptance Criteria:**
- [ ] Tags section appears in sidebar between Mailboxes and Views
- [ ] Each tag shows as a nav item with its colour and unread count
- [ ] Clicking a tag shows all messages across all mailboxes that have that tag
- [ ] Tag views support the same pagination, sorting, and layout modes as mailbox views
- [ ] Section is collapsible (consistent with Views/Quick Links)

### US-003: Bulk tagging from message list
**Description:** As a user, I want to select multiple messages and apply or remove a tag in bulk, so I can organise large amounts of mail efficiently.

**Acceptance Criteria:**
- [ ] Bulk toolbar includes "Tag" button when messages are selected
- [ ] Tag button opens a dropdown with checkboxes for existing tags + create new
- [ ] Applying a tag updates all selected messages
- [ ] Removing a tag updates all selected messages
- [ ] Context menu includes "Tag" submenu

### US-004: Auto-tagging via filter rules
**Description:** As a user, I want to create filter rules that automatically tag incoming messages, so new mail gets organised without manual effort.

**Acceptance Criteria:**
- [ ] Filter rule action list includes "Add tag" alongside existing "Move to folder"
- [ ] Multiple tags can be added by a single rule
- [ ] Rules apply to incoming SMTP mail and IMAP APPEND
- [ ] Existing rule UI updated with tag action option

### US-005: IMAP keyword mapping
**Description:** As a user connecting via Thunderbird or another IMAP client, I want my tags to appear as IMAP keywords, so I can see and modify them from any client.

**Acceptance Criteria:**
- [ ] Tags stored as IMAP keywords (e.g. tag "important" = keyword `$important`)
- [ ] PERMANENTFLAGS response includes `\*` to allow custom keywords
- [ ] IMAP STORE with custom keywords creates/updates tags
- [ ] IMAP FETCH returns custom keywords in FLAGS response
- [ ] Keywords set by IMAP clients appear as tags in web UI
- [ ] Standard `$Forwarded`, `$MDNSent` keywords handled correctly

### US-006: Tag management settings page
**Description:** As a user, I want to manage my tags (rename, change colour, delete), so I can keep my tag list clean and visually distinct.

**Acceptance Criteria:**
- [ ] Settings page section for "Tags"
- [ ] List of all tags with name, colour swatch, message count
- [ ] Rename tag (updates all message references)
- [ ] Change tag colour
- [ ] Delete tag (removes from all messages, with confirmation)
- [ ] Create new tag with name and colour

### US-007: Import with tag preservation
**Description:** As a user importing mail, I want Gmail labels and IMAP keywords to be preserved as tags, so I don't lose my organisation.

**Acceptance Criteria:**
- [ ] Gmail import maps labels to tags (e.g. Gmail "Work" label = "work" tag)
- [ ] Gmail special labels mapped: "Important" -> "important" tag, "Starred" -> flagged flag
- [ ] IMAP import preserves custom keywords as tags
- [ ] Thunderbird import preserves tags from X-Mozilla-Keys header
- [ ] Import deduplication: "All Mail" messages that already exist get tags merged, not duplicated

## Functional Requirements

### Storage

- FR-1: Add `tags` repeated string field to `MailMeta` protobuf (tag names, lowercase, no spaces)
- FR-2: Add `tags` repeated string field to `MailboxMessage` protobuf (denormalised for query speed)
- FR-3: Add per-user tag metadata to Pebble: `/user/{user_id}/tag/{name}` -> TagMeta protobuf (colour, created_at, sort_order)
- FR-4: Add `tags` column (JSON array or comma-separated) to SQLite `maildocument` table for indexed queries
- FR-5: Add `tags` field to Bleve `MailDocument` for full-text search filtering by tag
- FR-6: Tag names: lowercase, alphanumeric + hyphens, max 50 chars, max 100 tags per user, max 20 tags per message

### API / Handlers

- FR-7: `POST /mail/{mailbox}/{uid}/tags` — add/remove tags on a single message `{action: "add"|"remove", tags: ["tag1"]}`
- FR-8: `POST /mail/bulk/tags` — bulk add/remove tags on selected messages
- FR-9: `GET /mail/tag/{name}` — virtual view listing all messages with a given tag (paginated)
- FR-10: `GET /api/tags` — list user's tags with metadata and counts
- FR-11: `POST /api/tags` — create tag `{name, colour}`
- FR-12: `PUT /api/tags/{name}` — update tag (rename, recolour)
- FR-13: `DELETE /api/tags/{name}` — delete tag (remove from all messages)

### IMAP

- FR-14: Advertise `PERMANENTFLAGS (\Seen \Answered \Flagged \Deleted \Draft \*)` in SELECT response
- FR-15: Map tag names to IMAP keywords with `$` prefix (tag "work" = keyword `$work`)
- FR-16: Handle STORE +FLAGS ($work $urgent) by adding corresponding tags
- FR-17: Include custom keywords in FETCH FLAGS response
- FR-18: Handle keyword removal via STORE -FLAGS

### Filter Rules

- FR-19: Add `TAG` action type to FilterRule protobuf
- FR-20: Rule engine applies tag action by updating MailMeta tags and indexing

### Import

- FR-21: Gmail importer extracts X-Gmail-Labels header and maps to tags
- FR-22: IMAP importer preserves FLAGS keywords as tags
- FR-23: Thunderbird importer reads X-Mozilla-Keys header for tag mapping
- FR-24: Dedup logic merges tags when "All Mail" message already imported from a specific folder

## Design Considerations

### Tag colours
Use a predefined palette of 12-16 colours (similar to Gmail). Store as a colour index, not hex value, so themes can adjust. Default assignment: cycle through palette as tags are created.

### Sidebar UX
Tags section sits between Mailboxes and Quick Links. Collapsible. Shows tags sorted by most-used or alphabetical (user preference). Maximum 20 tags shown; "Show all" link for users with more.

### Performance
- Tag queries use the SQLite index (WHERE tags LIKE '%tagname%' or JSON array contains). For better performance, consider a separate `message_tags` junction table.
- Virtual tag views use the same pagination as mailbox views.
- Tag counts cached in TagMeta, updated on tag add/remove operations.

### Migration
- Existing messages have empty tags — no migration needed, tags are additive.
- Existing virtual mailboxes (unread, starred, all-mail) remain as built-in views, separate from user tags.
- No breaking changes to Pebble key space — new keys are additive.

## Technical Considerations

### Protobuf changes
Adding `repeated string tags` to MailMeta and MailboxMessage is backward-compatible (protobuf ignores unknown fields on old readers). New tag fields use the next available field numbers.

### Raft compatibility
Tag updates go through the same `ApplyCommand` path as flag updates. No new Raft command types needed — just PUT operations on existing keys with updated protobuf values.

### IMAP keyword limits
RFC 9051 does not limit keyword count, but some clients may not display more than ~20. The 100-tag-per-user limit and per-message practical limit of ~20 tags keeps things reasonable.

### SQLite indexing strategy
Option A: JSON array column with `json_each()` for queries. Simple, but slower for large mailboxes.
Option B: Junction table `message_tags(guid TEXT, tag TEXT)` with composite index. Faster queries, slightly more complex writes.
Recommend Option B for production use with large mailboxes (1500+ messages as seen in user's imported data).

## Success Metrics

- Users can tag a message in under 2 clicks from the message list
- Tag virtual views load in under 200ms for mailboxes with 5000+ messages
- Gmail import preserves 95%+ of user labels as tags
- IMAP clients can read and write tags via keywords without errors
- No regression in IMAP FETCH/STORE performance

## Resolved Decisions

1. **Built-in views stay separate.** Unread, Starred, and All Mail remain hardcoded views based on IMAP system flags (`\Seen`, `\Flagged`). They are not migrated to the tag system. This avoids an awkward mapping layer where every IMAP STORE of `\Flagged` would also need to update the tag index, and keeps "All Mail" as what it is — the absence of a filter, not a tag.

2. **Small coloured dots in message list, full chips in detail view.** The message list row shows 2-3 coloured dots next to the subject (tooltip on hover shows tag name). This gives quick visibility without horizontal clutter, especially when a message has multiple tags. The message detail view shows full chips with text labels and remove buttons for management.

3. **No system auto-tags in v1.** Auto-detection of "attachment", "newsletter", "mailing-list" etc. is deferred. Users can achieve the same result via filter rules (e.g. "if has List-Unsubscribe header, add tag newsletter"). Keeping tags as a purely user-controlled concept reduces complexity for the initial implementation.

4. **Max 20 tags per message, 100 tags per user.** 20 per message is generous enough for any practical use case. 100 per user matches reasonable expectations (Gmail allows 500 labels but most users have under 30).
