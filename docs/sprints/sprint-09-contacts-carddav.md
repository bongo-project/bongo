# Sprint 09: Contacts & Address Book (CardDAV)

**Phase:** 1 (Foundation)
**Goal:** Add personal contacts with web UI CRUD, Bleve search, compose autocomplete
integration, vCard import/export, and CardDAV sync for native clients (macOS Contacts,
iOS, DAVx5, Thunderbird).

---

## Context

Sprints 01–08b built the full mail server with a polished Dragonfly web UI. Users can
read, compose, send, thread, bulk-manage, and get real-time notifications — but there's
no address book. This sprint adds the contacts foundation.

**Existing infrastructure to build on:**
- PebbleStore + RaftApplier pattern (PreferenceStore, MailboxStore)
- SearchManager with per-user Bleve indexes (LRU cache, 128 max)
- GAL (Global Address List) with SearchGAL() and GET /api/gal?q=... autocomplete
- BlobStore for content-addressable storage (available for contact photos later)
- Templ + HTMX + Alpine.js + Pico CSS + Open Props for web UI
- Agent struct with store fields, NewAgent() init, Run() route registration
- `internal/caldav/` directory exists but is empty

**Deferred to a future sprint:**
- Contact groups / distribution lists
- Merge duplicate contacts
- Contact photo upload UI (BlobRef field in proto, upload flow deferred)
- Multiple emails/phones per contact (single field each for now)

---

## Tasks

### 1. Contact proto, store, and agent wiring

Foundation layer — data model, storage, lifecycle.

**Create `proto/bongo/v1/contact.proto`:**
- `message Contact`:
  - `string guid = 1`
  - `string display_name = 2`
  - `string email = 3` — primary email
  - `string phone = 4`
  - `string company = 5`
  - `string notes = 6`
  - `bytes vcard_raw = 7` — preserves non-standard vCard properties for CardDAV round-trip
  - `int64 created_at = 8`
  - `int64 updated_at = 9`
- Run `proto/generate.go` to produce `contact.pb.go`

**Create `internal/store/contacts.go`:**
- `ContactStore` struct wrapping `*PebbleStore` + `RaftApplier` (follow PreferenceStore pattern)
- Key format: `/user/{userID}/contact/{guid}` → Contact proto bytes
- `Create(userID string, contact *Contact) (string, error)` — generates GUID, sets created_at,
  marshals proto, applies RaftCommand OP_PUT
- `Get(userID, guid string) (*Contact, error)` — single Get, unmarshal
- `Update(userID, guid string, contact *Contact) error` — sets updated_at, applies OP_PUT
- `Delete(userID, guid string) error` — applies OP_DELETE
- `List(userID string, offset, limit int) ([]*Contact, int, error)` — PrefixScan on
  `/user/{userID}/contact/`, skip offset, collect limit, count total
- `FindByEmail(userID, email string) (*Contact, error)` — scan + filter (for quick-add dedup)

**Create `internal/store/contacts_test.go`:**
- Test CRUD round-trip (create → get → verify fields)
- Test list pagination (create 5, list offset=2 limit=2 → 2 results, total=5)
- Test delete removes key (delete → get returns not found)
- Test FindByEmail returns match

**Wire into `internal/agent/agent.go`:**
- Add `Contacts *store.ContactStore` field to Agent struct
- In `NewAgent()`: initialize `a.Contacts = store.NewContactStore(pebbleStore, a)` after
  Pebble/Raft init (same location as Preferences, Threads)

**Done when:** ContactStore CRUD works, unit tests pass, agent creates ContactStore on startup.

### 2. Contact CRUD web UI

List, create, edit, and delete contacts in the browser.

**Create `internal/web/handlers/contacts.go`:**
- `ContactsHandler` struct with `*store.ContactStore`, `*store.SearchManager`
- Constructor: `NewContactsHandler(contacts, search)`
- `ListContacts(w, r)` — GET `/contacts`: load List() with pagination, render template
- `CreateContact(w, r)` — POST `/contacts`: parse form, Create(), index in Bleve,
  redirect with flash "Contact created"
- `ShowContact(w, r)` — GET `/contacts/{guid}`: load Get(), render detail/edit template
- `UpdateContact(w, r)` — PUT `/contacts/{guid}`: parse form, Update(), re-index Bleve,
  return HTMX partial (updated contact row)
- `DeleteContact(w, r)` — DELETE `/contacts/{guid}`: Delete(), remove from Bleve index,
  HTMX removes row from list

**Create `internal/web/templates/contacts.templ`:**
- `ContactList(contacts []*Contact, page, totalPages int)` — table with columns:
  Name, Email, Phone, Company. Pagination controls. Empty state message when no contacts.
- `ContactCreateForm()` — inline form above table, toggled via Alpine.js `x-show`
- `ContactDetail(contact *Contact)` — detail/edit view with form fields:
  display_name, email, phone, company, notes. Save, Cancel, Delete buttons.
- `ContactRow(contact *Contact)` — single table row (for HTMX partial swaps)

**Modify `internal/web/templates/components.templ`:**
- Add "Contacts" link in sidebar navigation, below mail folders, above Settings
- Icon: address book or people icon

**Register routes in Agent.Run()** (protected group):
```
r.Get("/contacts", contactsH.ListContacts)
r.Post("/contacts", contactsH.CreateContact)
r.Get("/contacts/{guid}", contactsH.ShowContact)
r.Put("/contacts/{guid}", contactsH.UpdateContact)
r.Delete("/contacts/{guid}", contactsH.DeleteContact)
```

**Done when:** User can list, create, edit, delete contacts via browser. Sidebar shows
Contacts link. Empty state shows when no contacts exist.

### 3. Contact search via Bleve and autocomplete integration

Index contacts for search and merge with GAL in compose autocomplete.

**Modify `internal/store/search.go`:**
- Add `ContactDocument` struct: DisplayName, Email, Phone, Company (all string)
- Add `contactIndexMapping()` — all four fields as text fields with standard analyzer
- Add `IndexContact(userID, guid, displayName, email, phone, company string)`:
  - Open per-user index (reuse existing openUserIndex infrastructure)
  - Index ContactDocument with guid as ID
- Add `DeleteContactIndex(userID, guid string)`:
  - Open per-user index, delete by guid
- Add `SearchContacts(userID, query string, limit int) ([]ContactSearchResult, error)`:
  - QueryString query against per-user index
  - Return slice of ContactSearchResult{GUID, DisplayName, Email, Score}

**Modify `internal/web/handlers/compose.go`:**
- In `GALSearch()`: after calling `SearchGAL(query, limit)`:
  1. Also call `SearchContacts(userID, query, limit)`
  2. Merge results: personal contacts first (marked `"source": "contacts"`),
     then GAL entries (marked `"source": "gal"`)
  3. Deduplicate by email address (personal contact wins if same email)
  4. Return merged JSON array

**Modify `internal/store/contacts.go`:**
- In `Create()` and `Update()`: call `search.IndexContact()` after successful Raft apply
- In `Delete()`: call `search.DeleteContactIndex()` after successful Raft apply
- ContactStore needs a `search *SearchManager` field (add to constructor)

**Done when:** Create a contact → it appears in compose autocomplete within the same
session. Personal contacts appear before GAL entries in dropdown. Search deduplicates
by email.

### 4. vCard import/export

Support uploading .vcf files and downloading contacts as vCard format.

**Add `emersion/go-vcard` (or `github.com/emersion/go-vcard`) to go.mod.**

**Add to `internal/web/handlers/contacts.go`:**
- `ExportContacts(w, r)` — GET `/contacts/export`:
  - Load all contacts via List() (no pagination limit)
  - For each contact: if vcard_raw is non-empty, use it directly; else generate
    vCard 3.0 from proto fields (FN, EMAIL, TEL, ORG, NOTE)
  - Write concatenated vCards with Content-Type: text/vcard,
    Content-Disposition: attachment; filename="contacts.vcf"
- `ImportContacts(w, r)` — POST `/contacts/import`:
  - Parse multipart form file (.vcf)
  - Decode vCard entries using go-vcard decoder
  - For each entry: extract FN→display_name, EMAIL→email, TEL→phone, ORG→company,
    NOTE→notes. Store raw vCard bytes in vcard_raw.
  - Create contact via ContactStore for each entry
  - Redirect with flash "Imported N contacts" (or "Imported N, M errors")

**Add to `contacts.templ`:**
- Export button: `<a href="/contacts/export">Export vCard</a>` in toolbar
- Import form: file input + submit button, `enctype="multipart/form-data"`

**Register routes:**
```
r.Get("/contacts/export", contactsH.ExportContacts)
r.Post("/contacts/import", contactsH.ImportContacts)
```

**Done when:** Export downloads valid .vcf file parseable by macOS Contacts. Import a
.vcf file creates contacts in list. Round-trip: export → delete all → import → same contacts.

### 5. CardDAV server (RFC 6352)

Implement CardDAV protocol for sync with native clients.

**Add `emersion/go-webdav` to go.mod.**

**Create `internal/carddav/server.go`:**
- Implement the `carddav.Backend` interface from emersion/go-webdav/carddav:
  - `CurrentUserPrincipal(ctx)` — return `/carddav/{userID}/`
  - `AddressBookHomeSetPath(ctx)` — return `/carddav/{userID}/contacts/`
  - `ListAddressBooks(ctx)` — return single default address book
  - `GetAddressBook(ctx, path)` — return address book metadata
  - `GetAddressObject(ctx, path, req)` — load contact, return vCard data
  - `ListAddressObjects(ctx, path, req)` — list all contacts as address objects
  - `PutAddressObject(ctx, path, card, opts)` — create or update contact from vCard
  - `DeleteAddressObject(ctx, path)` — delete contact
- Backend wraps `*store.ContactStore` + `*store.SearchManager`
- Auth: extract userID from basic auth via existing LocalAuth

**Create `internal/carddav/server_test.go`:**
- Test: ListAddressObjects returns correct count
- Test: PutAddressObject creates contact, GetAddressObject retrieves it
- Test: DeleteAddressObject removes contact

**Wire into `internal/agent/agent.go`:**
- Create CardDAV handler in Agent.Run()
- Mount at `/carddav/` via `r.Handle("/carddav/*", carddavHandler)`
- Add `/.well-known/carddav` redirect: `r.Get("/.well-known/carddav", redirect)`

**Done when:** CardDAV client (curl with PROPFIND/GET/PUT/DELETE, or test harness) can
discover address book, list contacts, fetch individual vCards, create new contacts, and
delete contacts. Contacts created via web UI are visible via CardDAV and vice versa.

### 6. Link contacts to mail senders

Simple "Add to Contacts" button on message detail view.

**Modify `internal/web/templates/message.templ`:**
- Add "Add to Contacts" button/link next to the From address display
- `hx-post="/contacts/quick-add"` with hidden fields `from_email` and `from_name`
- Button styled as small secondary action (Pico CSS secondary button)
- Conditionally hide if sender is already a contact (check via FindByEmail in handler)

**Add to `internal/web/handlers/contacts.go`:**
- `QuickAddContact(w, r)` — POST `/contacts/quick-add`:
  - Parse from_email and from_name from form
  - Call `ContactStore.FindByEmail(userID, email)` — if found, flash "Already in contacts"
  - If not found: Create contact with display_name=from_name, email=from_email
  - Index in Bleve
  - Redirect back to message with flash "Contact added"

**Register route:** `r.Post("/contacts/quick-add", contactsH.QuickAddContact)`

**Done when:** View a message from unknown sender → "Add to Contacts" visible. Click →
sender saved as contact → appears in contacts list and compose autocomplete. Click on
message from known contact → button hidden or shows "Already in contacts".

### 7. Integration tests

**Create `test/integration/contacts_test.go` (build tag `integration`):**

**TestContactCRUD:**
1. Start dev agent, create user, login
2. POST `/contacts` with display_name=John, email=john@example.com, phone=555-1234
3. GET `/contacts` → verify John appears in list
4. PUT `/contacts/{guid}` → update phone to 555-5678
5. GET `/contacts/{guid}` → verify updated phone
6. DELETE `/contacts/{guid}` → verify removed from list

**TestContactAutocomplete:**
1. Create contact (Jane, jane@example.com)
2. Wait for Bleve indexing
3. GET `/api/gal?q=jane` → verify Jane in results with source=contacts
4. Verify Jane appears before any GAL entries with same prefix

**TestVCardImportExport:**
1. Create 2 contacts via POST
2. GET `/contacts/export` → download .vcf, verify Content-Type, verify 2 vCard entries
3. Delete both contacts via DELETE
4. POST `/contacts/import` with the downloaded .vcf → verify 2 contacts restored
5. GET `/contacts` → verify both contacts in list

**TestCardDAVSync:**
1. Create contact via web UI POST
2. PROPFIND `/carddav/{userID}/contacts/` → verify collection contains 1 resource
3. GET `/carddav/{userID}/contacts/{guid}.vcf` → verify valid vCard with correct fields
4. PUT new contact via CardDAV (send vCard to new path)
5. GET `/contacts` → verify CardDAV-created contact appears in web UI list
6. DELETE contact via CardDAV → verify removed from web UI

**Done when:** `go test -race -tags=integration ./test/integration/contacts_test.go` passes.

---

## Exit Criteria

- [ ] Contact proto created with all fields; Go code generated
- [ ] ContactStore CRUD (create/get/update/delete/list/findByEmail) works via Raft
- [ ] `go test -race ./internal/store/...` passes (contact unit tests)
- [ ] GET /contacts renders contact list with pagination
- [ ] POST/PUT/DELETE contacts work via web UI with HTMX
- [ ] Contacts link in sidebar navigation
- [ ] Bleve indexes contacts on create/update, removes on delete
- [ ] Compose autocomplete returns personal contacts + GAL (merged, deduplicated)
- [ ] GET /contacts/export downloads valid .vcf file
- [ ] POST /contacts/import parses .vcf and creates contacts
- [ ] `emersion/go-webdav` and `go-vcard` added to go.mod
- [ ] CardDAV PROPFIND/GET/PUT/DELETE work at /carddav/{userID}/contacts/
- [ ] `/.well-known/carddav` redirects correctly
- [ ] Web UI ↔ CardDAV two-way sync (contacts visible in both directions)
- [ ] "Add to Contacts" on message view creates contact from sender
- [ ] Quick-add skips if contact with same email already exists
- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/contacts_test.go` passes

## What NOT to do in this sprint

- No contact groups / distribution lists (future sprint)
- No merge duplicate contacts (future sprint)
- No contact photo upload UI (BlobRef field in proto, upload deferred)
- No CalDAV / calendar (Sprint 10)
- No multiple emails/phones per contact (single field each for now)
- No LDAP address book integration
- No rich text notes
- No contact birthday/anniversary fields
