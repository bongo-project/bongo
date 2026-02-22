# Sprint 13d: i18n Template Wiring

**Phase:** 1 (Foundation)
**Goal:** Replace all hardcoded English strings in `.templ` files with `i18n.T()` calls
so the locale selector and message catalogues actually take effect in the UI.

---

## Context

Sprint 13c built the full i18n infrastructure: `go-i18n` v2 library, embedded message
catalogues (`en.json` with 175 strings, `fr.json`, `de.json`, `es.json`), `locale` field
in UserPreferences proto, locale selector in Settings, `<html lang="">` dynamic from
preference, and locale middleware passing locale through request context.

**The gap:** All 14 `.templ` files still use hardcoded English strings like `"Compose"`,
`"Reply"`, `"Settings"`, etc. The `i18n.T()` function exists and works, but templates
never call it. Switching locale in Settings changes the `lang` attribute but the visible
UI text stays English.

**Existing infrastructure to use:**
- `internal/i18n/i18n.go` — `I18n` struct with `T(locale, messageID) string` method
- `internal/i18n/messages/en.json` — 175 message IDs (e.g., `nav.inbox`, `mail.compose`,
  `mail.reply`, `common.save`, `common.cancel`, `admin.dashboard`, etc.)
- `internal/i18n/messages/{fr,de,es}.json` — translation files
- Locale available in request context via `middleware.LocaleFromContext(ctx)`
- Template helper: `localeFromCtx(ctx)` already used for `<html lang="">` attribute

---

## Validation Gaps — MUST FIX

The coding agent completed tasks 1–7 but validation found **41 hardcoded English strings**
still remaining across 5 files. These MUST be converted to `t()` calls with corresponding
entries added to all four locale JSON files (en/fr/de/es).

### Gap A: `settings.templ` — 17 hardcoded strings ✅ FIXED

Replace these hardcoded option labels and UI text with `t()` calls:
- Theme options: `"Auto (follow OS)"`, `"Light"`, `"Dark"` → `t(ctx, "settings.themeAuto")`, etc.
- Layout options: `"List"`, `"Two Pane"`, `"Three Pane"` → `t(ctx, "settings.layoutList")`, etc.
- Density options: `"Compact"`, `"Comfortable"`, `"Spacious"` → `t(ctx, "settings.densityCompact")`, etc.
- Default reply options: `"Reply to sender"`, `"Reply all"` → `t(ctx, "settings.replySender")`, etc.
- Language label: `"Language"` → `t(ctx, "settings.language")`
- Language option display names: `"English"`, `"Français"`, `"Deutsch"`, `"Español"` → `t(ctx, "settings.langEn")`, etc.
- Checkbox label: `"Infinite scroll (load more messages automatically when scrolling)"` → `t(ctx, "settings.infiniteScroll")`

### Gap B: `rules.templ` — 22 hardcoded strings ⚠️ STILL OPEN

The original 11 strings (table headers, Yes/No, section headers) were fixed, but **11 more
form-level strings** remain hardcoded. Fix ALL of these:

**Already fixed (keep as-is):** table headers (colName etc.), Yes/No, conditionsHeader, actionHeader ✓

**Still hardcoded — MUST FIX:**
- Field option labels in condition `<select>`:
  `"From"` → `t(ctx, "rules.fieldFrom")`
  `"To"` → `t(ctx, "rules.fieldTo")`
  `"CC"` → `t(ctx, "rules.fieldCc")`
  `"Subject"` → `t(ctx, "rules.fieldSubject")`
  `"Body"` → `t(ctx, "rules.fieldBody")`
  `"Has Attachment"` → `t(ctx, "rules.fieldHasAttachment")`
  `"Size (bytes)"` → `t(ctx, "rules.fieldSize")`
- Default operator label: `"equals"` → `t(ctx, "rules.opEquals")`
- Button text: `"+ Add Condition"` → `t(ctx, "rules.addCondition")`
- Form label: `"When matched"` → `t(ctx, "rules.whenMatched")`
- Form label: `"Target folder"` → `t(ctx, "rules.targetFolder")`

Add all 11 new message IDs to en.json, fr.json, de.json, es.json.

### Gap C: `calendar.templ` — 7 hardcoded strings ✅ FIXED

- Day column headers: `"Mon"`, `"Tue"`, `"Wed"`, `"Thu"`, `"Fri"`, `"Sat"`, `"Sun"` → `t(ctx, "calendar.mon")`, etc.

### Gap D: `security.templ` — 3 hardcoded strings ✅ FIXED

- `"Status:"` (2 occurrences) → `t(ctx, "security.status")`
- `"Minimum 8 characters"` → `t(ctx, "security.minPassword")`

### Gap E: `import.templ` — 3 hardcoded strings ✅ FIXED

- `"-- New mailbox (type name below) --"` → `t(ctx, "import.newMailboxOption")`
- `"New mailbox name (optional)"` placeholder → `t(ctx, "import.newMailboxPlaceholder")`
- `"Go to { mailbox }"` button text → `t(ctx, "import.goToMailbox")`

### After fixing gaps A–E:

- Add all new message IDs to `en.json`, `fr.json`, `de.json`, `es.json`
- Re-run `TestI18nCoverage` to confirm no missing/orphan keys
- Re-run `TestI18nAllTemplates` to confirm no bare English on any page with locale="fr"

---

## Tasks

### 1. Add i18n helper to template rendering context

Make translation function accessible from all templates without passing it individually.

**Create or modify `internal/web/templates/i18n_helpers.templ`:**
- Add a `t(ctx context.Context, messageID string) string` helper that:
  - Gets locale from `localeFromCtx(ctx)` (or defaults to `"en"`)
  - Calls the global `i18n.T(locale, messageID)`
  - Returns the translated string
- Alternatively, if templ doesn't support calling package functions directly, add `T func(string) string` to template data structs and populate from handler

**Ensure every handler passes i18n context:**
- Verify locale middleware runs before all web handlers
- Verify `localeFromCtx(ctx)` returns correct locale for authenticated users

**Done when:** `t(ctx, "mail.compose")` returns "Compose" (en) or "Composer" (fr)
from any `.templ` file.

### 2. Wire navigation and layout strings

Replace hardcoded strings in layout and sidebar components.

**Modify `internal/web/templates/layout.templ`:**
- `"Bongo"` brand text → keep as-is (brand name, not translatable)
- `"Settings"` → `{ t(ctx, "nav.settings") }`
- `"Logout"` → `{ t(ctx, "nav.logout") }`
- `"Skip to main content"` → `{ t(ctx, "nav.skip_to_content") }`
- Search placeholder → `{ t(ctx, "nav.search_placeholder") }`
- Theme toggle tooltip → `{ t(ctx, "nav.toggle_theme") }`

**Modify `internal/web/templates/components.templ`:**
- `"Inbox"`, `"Sent"`, `"Drafts"`, `"Trash"`, `"Junk"` → `{ t(ctx, "nav.inbox") }`, etc.
- `"Unread"`, `"Starred"`, `"All Mail"` → translated
- `"Contacts"`, `"Calendar"`, `"Admin"` → translated
- `"New Folder"` → translated
- Pagination: `"Previous"`, `"Next"` → translated

**Done when:** Sidebar, top bar, and pagination render in the user's selected locale.

### 3. Wire mailbox and message view strings

**Modify `internal/web/templates/mailbox.templ`:**
- `"No messages"` / `"This mailbox is empty"` → translated
- `"Select all"` checkbox label → translated
- Bulk toolbar: `"Mark Read"`, `"Mark Unread"`, `"Flag"`, `"Delete"`, `"Move to..."` → translated
- `"Refresh"` → translated
- Column headers if any (From, Subject, Date) → translated

**Modify `internal/web/templates/message.templ`:**
- `"From:"`, `"To:"`, `"Cc:"`, `"Date:"`, `"Subject:"` → translated
- `"Reply"`, `"Reply All"`, `"Forward"`, `"Delete"` → translated
- `"Export EML"`, `"Print"` → translated
- `"Load external images"` → translated
- `"Show quoted text"` / `"Hide quoted text"` → translated
- Thread labels → translated

**Modify `internal/web/templates/search.templ`:**
- `"Search results for"` → translated
- `"No results found"` → translated

**Done when:** Mail list, message view, and search pages render translated strings.

### 4. Wire compose, contacts, and calendar strings

**Modify `internal/web/templates/compose.templ`:**
- `"Compose"`, `"To"`, `"Cc"`, `"Bcc"`, `"Subject"` → translated
- `"Send"`, `"Save Draft"`, `"Discard"` → translated
- `"Attach files"` → translated
- Placeholder text → translated

**Modify `internal/web/templates/contacts.templ`:**
- `"Contacts"`, `"New Contact"`, `"Edit"`, `"Delete"` → translated
- `"Name"`, `"Email"`, `"Phone"`, `"Company"` → translated
- `"Import"`, `"Export"`, `"Merge Duplicates"` → translated
- `"No contacts"` → translated

**Modify `internal/web/templates/calendar.templ`:**
- `"Month"`, `"Week"`, `"Day"` → translated
- `"New Event"`, `"Edit Event"`, `"Delete Event"` → translated
- `"Today"` → translated
- Month names and day abbreviations → use Go's `time` locale or i18n catalogue
- `"All Day"`, `"Location"`, `"Description"` → translated
- `"Reminders"` → translated

**Done when:** Compose, contacts, and calendar pages render translated strings.

### 5. Wire settings, security, and admin strings

**Modify `internal/web/templates/settings.templ`:**
- `"Preferences"`, `"Theme"`, `"Layout"`, `"Density"` → translated
- `"Language"`, `"Timezone"`, `"Signature"` → translated
- `"Save"`, `"Cancel"` → translated
- Theme options: `"Light"`, `"Dark"`, `"System"` → translated

**Modify `internal/web/templates/security.templ`:**
- `"Two-Factor Authentication"`, `"Enable"`, `"Disable"` → translated
- `"Recovery Codes"`, `"Change Password"` → translated
- `"Current Password"`, `"New Password"`, `"Confirm Password"` → translated

**Modify `internal/web/templates/admin.templ`:**
- Dashboard: `"System Health"`, `"Storage"`, `"Connections"`, `"Throughput"` → translated
- User management: `"Users"`, `"Create User"`, `"Disable"`, `"Delete"` → translated
- Domain management: `"Domains"`, `"Aliases"` → translated
- Queue: `"Queue"`, `"Retry"`, `"Drop"`, `"Flush"` → translated
- Cluster: `"Nodes"`, `"Logs"` → translated

**Modify `internal/web/templates/login.templ`:**
- `"Sign In"`, `"Email"`, `"Password"` → translated
- `"Invalid credentials"` → translated
- `"Enter your 2FA code"` → translated

**Modify `internal/web/templates/rules.templ`:**
- `"Mail Filters"`, `"New Rule"`, `"Edit"`, `"Delete"` → translated
- Condition labels, operator labels, action labels → translated
- `"Test Rule"`, `"Save Rule"` → translated

**Modify `internal/web/templates/import.templ`:**
- `"Import Mailbox"`, `"Choose file"`, `"Import"` → translated

**Done when:** All remaining templates (settings, security, admin, login, rules, import)
render translated strings.

### 6. Verify message catalogue completeness

Ensure all `i18n.T()` calls in templates have corresponding entries in `en.json`
and the stub translation files.

**Scan all `.templ` files for `t(ctx, "...")` calls:**
- Extract all message IDs used
- Cross-reference with `en.json` keys
- Add any missing keys to `en.json` with English text
- Add same keys to `fr.json`, `de.json`, `es.json` (translate where possible,
  English fallback for remainder)

**Add integration test `TestI18nCoverage`:**
- Load all message files
- Parse all `.templ` files for `t(ctx, "...")` patterns
- Assert every used message ID exists in `en.json`
- Assert every `en.json` key is used in at least one template (no orphan strings)

**Done when:** No missing message IDs at runtime. Coverage test passes. All four
locale files have entries for every message ID.

### 7. Integration tests

**Add `test/integration/i18n_test.go`:**
- `TestI18nTemplateRendering`: Login, set locale to "fr", GET `/mail/INBOX`
  → response body contains French strings (e.g., "Boîte de réception" not "Inbox")
- `TestI18nFallback`: Set locale to unknown ("xx"), verify English fallback renders
- `TestI18nAllTemplates`: Hit each major page (mail, compose, contacts, calendar,
  admin, settings) with locale="fr" → verify no bare English strings from the
  catalogue appear (i.e., "Compose" should not appear, "Composer" should)
- `TestI18nContextPropagation`: Verify locale flows from preference → middleware
  → handler → template correctly

**Done when:** `go test -race -tags=integration ./test/integration/ -run I18n` passes.

---

## Exit Criteria

- [x] Template helper `t(ctx, messageID)` available in all `.templ` files
- [x] `layout.templ` and `components.templ` use `t()` for all UI strings
- [x] `mailbox.templ` and `message.templ` use `t()` for all UI strings
- [x] `compose.templ`, `contacts.templ`, `calendar.templ` use `t()` for all UI strings
- [x] `settings.templ`, `security.templ`, `admin.templ` use `t()` for all UI strings
- [x] `login.templ`, `rules.templ`, `search.templ`, `import.templ` use `t()` for all UI strings
- [x] `en.json` message catalogue covers all template message IDs
- [x] `fr.json`, `de.json`, `es.json` have entries for all message IDs
- [x] `TestI18nCoverage` passes (no missing/orphan message IDs)
- [x] `TestI18nTemplateRendering` passes (French UI renders correctly)
- [x] `TestI18nFallback` passes (unknown locale falls back to English)
- [x] Switching locale in Settings changes visible UI text (not just `lang` attribute)
- [x] `go test -race ./...` passes
- [x] `go test -race -tags=integration ./test/integration/ -run I18n` passes

## What NOT to do in this sprint

- No new i18n infrastructure (already built in 13c — just wire it up)
- No new locale additions beyond en/fr/de/es
- No RTL layout support
- No pluralisation rules (keep simple key→string mapping for now)
- No JavaScript string translation (Alpine.js labels can stay English for now — Phase 2)
- No date/number formatting localisation (use browser Intl API later)
