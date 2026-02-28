# Progress Log

## Patterns Discovered
(Add reusable patterns here as you find them)

## Found Issues
(Unrelated issues discovered by subagents during implementation. Review periodically.)

---

## Execution Log

### 2026-02-28 - Project Setup
- Imported 19 tasks across 7 phases from tasks/prd-message-tags.md
- Key decisions: junction table for SQLite (not JSON column), palette index for colours, $prefix for IMAP keywords
---

### 2026-02-28 - Task 1: Add tags to protobuf schemas [sonnet]
- Approach: Edit existing .proto files, create new tag.proto, regenerate with protoc
- What was done: Added `repeated string tags = 19` to MailMeta, `repeated string tags = 5` to MailboxMessage, created TagMeta message (name, colour_index, created_at, sort_order)
- Files changed: mail.proto, mail.pb.go, mailbox.proto, mailbox.pb.go, tag.proto (new), tag.pb.go (new)
- Tests: PASS (29 packages, 0 failures)
---

### 2026-02-28 - Tasks 2,3,4: SQLite tags, Bleve tags, Tag validation [sonnet, parallel]
- Approach: Three parallel workers for independent Phase 1 tasks
- Task 2: Added message_tags junction table (mail_guid, tag) to SQLite with IndexMailTags (delete+insert txn) and QueryByTag methods on UserIndex/MetadataManager
- Task 3: Added Tags []string keyword field to Bleve MailDocument, updated IndexMail signature with tags param, updated all callers (FSM, compose, IMAP)
- Task 4: Created internal/store/tags.go with ValidateTagName (regex), GetTags, SetTags, AddTags, RemoveTags (in-memory proto helpers), CountUserDistinctTags (Pebble prefix scan)
- Files: metadata.go, metadata_test.go, search.go, search_test.go, tags.go (new), tags_test.go (new), fsm.go, fsm_rebuild.go, fsm_rebuild_test.go, compose.go, backend.go
- Tests: PASS (all 29 packages)
- Patterns: SQLite schema uses CREATE TABLE IF NOT EXISTS (idempotent). Tag replace = DELETE+INSERT in txn. Bleve keyword field for exact-match. Regex ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ handles single-char names. Per-user limits enforced at handler layer via CountUserDistinctTags.
---

### 2026-02-28 - Task 5: Tag management store [sonnet]
- Approach: Created TagStore following MailboxStore/PreferenceStore pattern
- What was done: CreateTag (colour cycling, 100/user limit), ListTags (prefix scan + counts), RenameTag, DeleteTag, UpdateTagColour. All writes via Raft ApplyCommand.
- Files: tag_store.go (new), tag_store_test.go (new)
- Tests: PASS (22 new tests)
- Patterns: TagStore follows identical struct/constructor/applier pattern. directApplier reused across store tests. Colour auto-assign = existingCount % 16.
---

### 2026-02-28 - Tasks 6,7: Tag HTTP handlers [sonnet, parallel]
- Task 6: Created TagsHandler with ListTags, CreateTag, UpdateTag, DeleteTag. DELETE removes tag from all messages via Raft. Routes: GET/POST/PUT/DELETE /api/tags.
- Task 7: Added UpdateMessageTags to MailHandler. POST /mail/{mailbox}/{uid}/tags with add/remove. Updates Pebble (MailMeta+MailboxMessage), SQLite, Bleve. Auto-creates tag defs.
- Files: tags.go, tags_test.go, mail.go, mail_tags_test.go, agent.go
- Tests: PASS (26 new tests across both handlers)
---

### 2026-02-28 - Tasks 8,10,11,12: Tag views and UI [sonnet, parallel]
- Task 8: Tag virtual view at GET /mail/tag/{name}. Added ListMessagesByGUIDs to MailboxStore, ListTagMessages handler. Route before {mailbox} wildcard.
- Task 10: Message detail tag chips with Alpine.js messageTags component. Coloured chips, add dropdown with search/filter, remove X. Uses fetch() to POST tags endpoint.
- Task 11: Message list tag dots (max 3, +N overflow). 8x8px circles with native tooltip. Colour from TagStore palette lookup.
- Task 12: POST /mail/bulk/tags endpoint. Toolbar Tag dropdown button, context menu Tag submenu. 8 new tests.
- Files: mailbox.go, mail.go, mail_tags_test.go, mailbox.templ, message.templ, components.templ, bongo.js, bongo.css, agent.go, i18n (en/fr/de/es)
- Tests: PASS (all packages)
---
