# Project: Message Tags

> Hybrid folder + tag model: messages stay in one mailbox, tags are additive metadata with IMAP keyword mapping.

Started: 2026-02-28
Status: in-progress

---

## Tasks

### Phase 1: Storage Foundation
- [x] 1. Add tags to protobuf schemas (MailMeta, MailboxMessage, new TagMeta), compile .pb.go files
- [ ] 2. Add message_tags junction table to SQLite metadata with IndexMailTags and QueryByTag methods (needs: 1)
- [ ] 3. Add tags keyword field to Bleve MailDocument, update IndexMail to include tags (needs: 1)
- [ ] 4. Add tag methods to MailboxStore: GetTags, SetTags, AddTags, RemoveTags with validation (max 20/msg, 100/user, name rules) + unit tests (needs: 1)

### Phase 2: Tag CRUD API
- [ ] 5. Tag management store: CreateTag, ListTags, RenameTag, DeleteTag, UpdateTagColour in Pebble with cached counts (needs: 1)
- [ ] 6. Tag management HTTP handlers: GET/POST/PUT/DELETE /api/tags with JSON responses (needs: 5)
- [ ] 7. Single message tag handler: POST /mail/{mailbox}/{uid}/tags with add/remove actions, updates Pebble + SQLite + Bleve (needs: 4, 5)

### Phase 3: Tag Views & UI
- [ ] 8. Tag virtual view handler: GET /mail/tag/{name} with pagination, reusing ListVirtualMessages pattern (needs: 4, 5)
- [ ] 9. Sidebar tags section: collapsible, colour dots, unread counts, between Mailboxes and Views (needs: 8)
- [ ] 10. Message detail tag chips: coloured chips with text, add dropdown, remove X button, HTMX updates (needs: 7)
- [ ] 11. Message list tag dots: small coloured dots next to subject (max 3), tooltip on hover (needs: 4)
- [ ] 12. Bulk tagging: toolbar Tag button with dropdown, context menu Tag submenu, POST /mail/bulk/tags (needs: 7)

### Phase 4: IMAP Keywords
- [ ] 13. IMAP PERMANENTFLAGS + FETCH: advertise \* in SELECT, return tags as $keyword in FLAGS response (needs: 4)
- [ ] 14. IMAP STORE keywords: handle +FLAGS/-FLAGS for custom $keywords, sync to tag store (needs: 13)

### Phase 5: Filter Rules
- [ ] 15. Add TAG action type to FilterRule protobuf and rule engine apply logic (needs: 4)
- [ ] 16. Filter rule UI: add tag action option to rule editor template (needs: 15)

### Phase 6: Import
- [ ] 17. Gmail/IMAP/Thunderbird import: extract X-Gmail-Labels, IMAP keywords, X-Mozilla-Keys as tags (needs: 4)
- [ ] 18. Import dedup tag merging: when All Mail message already exists, merge tags instead of duplicating (needs: 17)

### Phase 7: Settings & Polish
- [ ] 19. Tag management settings page: list tags with colour/count, rename, recolour, delete with confirmation (needs: 5, 6)

---

## Notes

- Tags are per-user, flat namespace, lowercase alphanumeric + hyphens, max 50 chars
- Max 20 tags per message, 100 tags per user
- Built-in views (Unread, Starred, All Mail) stay separate from tags
- Message list shows coloured dots (not chips) for tags; detail view shows full chips
- No system auto-tags in v1
- SQLite uses junction table (message_tags) not JSON column for performance
- IMAP keywords use $ prefix: tag "work" = keyword $work
- Tag colours stored as palette index (12-16 predefined colours), not hex
