# Sprint 13b: Email Rendering, Attachments & Import/Export

**Phase:** 1 (Foundation)
**Goal:** Improve HTML email rendering, add inline attachment previews, fix compose
attachment upload, add single-message EML export and web-based mbox import, and add
print-friendly message view.

---

## Context

Sprints 01–13a deliver a secure, feature-complete mail server. However, the message
viewing experience has gaps: HTML emails only get basic sanitization (no inline CSS
rewriting), attachments are download-only (no preview), compose silently ignores file
uploads, and there's no way to export/import messages from the web UI.

**Existing infrastructure:**
- HTML sanitization: bluemonday UGC policy with inline style attributes on common
  elements. Remote image blocking already done (Sprint 13a).
- Message parsing: `enmime.ReadEnvelope()` extracts HTML, text, and attachments
- Attachment storage: BlobRef proto (hash, size, content_type) in MailMeta.Attachments
- Blob store: content-addressable `BlobStore.Get(hash)` returns io.ReadCloser
- Download endpoint: `GET /mail/blob/{hash}` with `Content-Disposition: attachment`
- Compose: `r.ParseMultipartForm(32<<20)` called but file inputs never read
- `buildRFC5322()` creates text/plain only — no MIME multipart/mixed
- CLI import tool exists: `cmd/bongo-import/` with mbox, maildir, EML, IMAP sources
  and resume tracking — but no web UI for import
- Contact export pattern: `GET /contacts/export` streams vCard file
- No print CSS, no PDF preview library, no syntax highlighting

---

## Tasks

### 1. Improved HTML email rendering

Better CSS handling and quoted-text formatting for HTML emails.

**Modify `internal/web/handlers/mail.go`:**
- After bluemonday sanitization, run CSS inlining pass:
  - Extract any `<style>` blocks from the HTML body
  - Apply rules as inline `style=""` attributes on matching elements
  - Use a lightweight CSS inliner (or simple regex-based approach for common selectors:
    element, class, ID)
  - Remove `<style>` blocks after inlining (CSP blocks them anyway)
- Scope all email CSS to prevent leaking into app UI:
  - Wrap sanitized HTML in `<div class="email-body">` container
  - Prefix any remaining inline styles to be contained

**Modify `internal/web/templates/message.templ`:**
- Wrap HTML body in `<div class="email-body">` with overflow handling
- Add base CSS for `.email-body`: `max-width: 100%; overflow-x: auto; word-wrap: break-word`
- Style quoted text (lines starting with `>` in text-only view):
  - Add `<span class="quoted">` wrapper with left border + muted colour

**Add to `internal/web/static/css/bongo.css`:**
- `.email-body` container styles: contained width, overflow scroll
- `.email-body img` max-width: 100% (prevent layout breaking)
- `.email-body table` responsive handling
- `.quoted` left border, muted text colour

**Done when:** HTML emails with `<style>` blocks render correctly (styles inlined).
Email CSS cannot affect the app layout. Quoted text in plain-text emails has visual
left-border formatting.

### 2. Compose attachment upload

Fix the compose handler to actually process file attachments.

**Modify `internal/web/handlers/compose.go` → `Send()`:**
- After `r.ParseMultipartForm(32<<20)`, read uploaded files:
  ```go
  files := r.MultipartForm.File["attachments"]
  for _, fh := range files {
      f, _ := fh.Open()
      hash, _ := h.blobs.PutReader(f, fh.Size)
      attachments = append(attachments, &bongov1.BlobRef{
          Hash: hash, Size: fh.Size, ContentType: fh.Header.Get("Content-Type"),
      })
  }
  ```
- Store BlobRef array in MailMeta.Attachments via Raft command
- Modify `buildRFC5322()` to create MIME multipart/mixed when attachments present:
  - `Content-Type: multipart/mixed; boundary=...`
  - First part: `text/plain` (or `text/html`) body
  - Subsequent parts: each attachment with `Content-Disposition: attachment; filename="..."`
    and `Content-Transfer-Encoding: base64`
- File validation: reject files > 25MB individually, total form limit 32MB
- Content-type detection: use `http.DetectContentType()` as fallback if header missing

**Modify `internal/web/templates/compose.templ`:**
- Improve file input UX:
  - Show selected files list with names + sizes before send
  - Alpine.js component: `x-data="{files: []}"` with `@change` handler
  - Remove individual files from list
  - Total size indicator

**Modify `SaveDraft()` similarly:**
- Save attachments to blob store when drafting
- Store BlobRef array in draft MailMeta

**Done when:** User attaches files in compose → files visible in list with sizes →
Send → recipient sees attachments in message → can download them. Draft saves preserve
attachments.

### 3. Inline attachment preview

Preview images and PDFs inline in the message view.

**Modify `internal/web/handlers/mail.go` → `ServeBlob()`:**
- Add a `?inline=1` query parameter option:
  - If `inline=1`: set `Content-Disposition: inline` instead of `attachment`
  - Validate content-type is safe for inline display (images, PDF, text)
  - Set appropriate `Content-Type` header from BlobRef
- Add `Content-Security-Policy` header for inline blob serving:
  - `sandbox` for PDFs to prevent script execution

**Modify `internal/web/templates/message.templ`:**
- For image attachments (`image/jpeg`, `image/png`, `image/gif`, `image/webp`):
  - Show inline `<img>` thumbnail (lazy-loaded, max-height 200px)
  - Click to expand full size in lightbox (Alpine.js modal)
- For PDF attachments (`application/pdf`):
  - Embed `<iframe src="/mail/blob/{hash}?inline=1" sandbox>` with fixed height
  - Fallback "Download PDF" link for browsers without inline PDF support
- For text/code attachments (`text/plain`, `text/csv`, `text/html`,
  `application/json`, `application/xml`):
  - Show first 50 lines in `<pre><code>` block
  - "Show full file" / "Download" links
- For other types: keep download-only link with file icon

**Add to static assets:**
- No external library needed — browser native PDF rendering in iframe
- Image lightbox: simple Alpine.js modal (no library dependency)

**Done when:** Image attachments show inline thumbnails with click-to-expand. PDFs
render in sandboxed iframe. Text files show preview. Other types show download link.

### 4. Single-message EML export

Download individual messages as standard .eml files.

**Add to `internal/web/handlers/mail.go`:**
- `ExportEML(w, r)` — GET `/mail/{mailbox}/{uid}/export`:
  - Load raw message blob via MailMeta.RawMessage.Hash
  - Set headers:
    - `Content-Type: message/rfc822`
    - `Content-Disposition: attachment; filename="{subject}.eml"`
    - Sanitize filename (replace special chars, limit length)
  - Stream blob bytes directly to response (no parsing needed — raw RFC 5322)

**Register route in agent.go:**
- `r.Get("/mail/{mailbox}/{uid}/export", mailH.ExportEML)`

**Add "Download as EML" button to message.templ:**
- Add to message toolbar (next to Reply/Forward/Delete)
- Download icon + "Export" label
- Direct link (no HTMX, just browser download)

**Done when:** Click "Export" on any message → browser downloads `{subject}.eml` file.
File can be opened in Thunderbird, Apple Mail, etc.

### 5. Web-based mbox import

Web UI for importing mailbox archives (mbox format).

**Add to `internal/web/handlers/mail.go`:**
- `ImportPage(w, r)` — GET `/import`:
  - Render import form: file upload + target mailbox dropdown
- `ImportMbox(w, r)` — POST `/import`:
  - Parse multipart form (allow up to 500MB for mbox files)
  - Read uploaded .mbox file
  - Parse mbox format: split on `\nFrom ` lines (same logic as CLI importer)
  - For each message:
    - Store raw bytes in blob store
    - Parse headers for MailMeta (subject, from, to, date, message-id)
    - Deliver via existing delivery pipeline (or direct Raft apply to skip
      spam scoring — imported mail is already classified)
  - Return result: "{count} messages imported to {mailbox}"
  - Handle errors gracefully: continue on individual message parse failure,
    report count of successes + failures

**Create `internal/web/templates/import.templ`:**
- `ImportPage(mailboxes []string)`:
  - File upload input (accept=".mbox,.mbx")
  - Target mailbox dropdown (default: INBOX, option to create new)
  - Import button
  - Progress indicator (for large files, show "Importing... {n} messages processed")
- `ImportResult(imported, failed int, mailbox string)`:
  - Success/failure summary
  - Link to target mailbox

**Add "Import" link in sidebar or settings:**
- Settings → Import/Export section, or dedicated `/import` page
- Link from admin panel as well (admin can import for any user — future)

**Register routes:**
- `r.Get("/import", mailH.ImportPage)`
- `r.Post("/import", mailH.ImportMbox)`

**Done when:** User uploads .mbox file → selects target mailbox → import runs →
sees "42 messages imported to INBOX" → messages appear in mailbox with correct
dates, subjects, and threading.

### 6. Print-friendly message view

Add print CSS and print button for clean message printing.

**Add to `internal/web/static/css/bongo.css`:**
```css
@media print {
  /* Hide navigation */
  nav, .sidebar, .toolbar, .message-actions,
  .flash-message, footer, .admin-link { display: none !important; }

  /* Full-width message */
  .message-content, .email-body { width: 100% !important; max-width: none !important; }

  /* Clean header */
  .message-header { border-bottom: 1px solid #ccc; padding-bottom: 0.5em; }

  /* Show URLs for links */
  .email-body a[href]:after { content: " (" attr(href) ")"; font-size: 0.8em; color: #666; }

  /* Attachment list: simple text */
  .attachment-item { display: block; color: #333; }

  /* Page margins */
  @page { margin: 2cm; }
}
```

**Modify `internal/web/templates/message.templ`:**
- Add print button to toolbar: `<button onclick="window.print()" class="print-btn">Print</button>`
- Add print icon (use Unicode 🖨 or inline SVG)

**Done when:** Click "Print" → browser print dialog opens → preview shows clean
message with headers, body, and attachment list — no navigation or UI chrome.

### 7. Integration tests

**Create `test/integration/rendering_test.go` (build tag `integration`):**

**TestComposeWithAttachments:**
1. Start dev agent, create user, login
2. Build multipart form with text body + 2 file attachments (small PNG + text file)
3. POST `/compose/send`
4. Verify message stored with 2 BlobRef entries in MailMeta.Attachments
5. GET message → verify attachments listed with correct filenames/sizes
6. GET `/mail/blob/{hash}` → verify file content matches uploaded

**TestInlineAttachmentPreview:**
1. Deliver email with image attachment
2. GET message view
3. Verify response contains `<img` tag with inline blob URL for image attachment
4. Verify PDF attachment has `<iframe` tag (if PDF attached)

**TestEMLExport:**
1. Deliver test email via SMTP
2. GET `/mail/INBOX/{uid}/export`
3. Verify response headers: Content-Type message/rfc822, Content-Disposition attachment
4. Verify response body is valid RFC 5322 (contains From:, To:, Subject:, Date:)

**TestMboxImport:**
1. Create a valid mbox file in memory (2-3 messages with From_ separators)
2. POST `/import` with mbox file + target mailbox "Imported"
3. Verify 3 messages appear in "Imported" mailbox
4. Verify subjects and dates parsed correctly

**TestHTMLRendering:**
1. Deliver email with `<style>` block + styled `<div>`
2. GET message view
3. Verify `<style>` block removed from output
4. Verify styled div has inline `style` attribute applied

**Done when:** `go test -race -tags=integration ./test/integration/rendering_test.go` passes.

---

## Exit Criteria

- [x] HTML emails with `<style>` blocks render correctly (styles inlined, blocks removed)
- [x] Email CSS contained in `.email-body` wrapper (no layout leaking)
- [x] Quoted text in plain-text emails has visual left-border formatting
- [x] Compose attachment upload works (files stored in blob store)
- [x] Compose builds MIME multipart/mixed RFC 5322 when attachments present
- [x] Compose UI shows selected files list with sizes before send
- [x] Draft save preserves attachments
- [x] Image attachments show inline thumbnails with click-to-expand
- [x] PDF attachments render in sandboxed iframe
- [x] Text/code attachments show first 50 lines preview
- [x] EML export downloads raw message as .eml file
- [x] "Export" button in message toolbar
- [x] Mbox import via web UI (upload .mbox → select mailbox → import)
- [x] Import reports success/failure count
- [x] Print CSS hides navigation, shows clean message
- [x] Print button in message toolbar
- [x] `go test -race ./...` passes
- [x] Integration tests pass (compose attachments, preview, EML export, mbox import, HTML rendering)

## What NOT to do in this sprint

- No maildir/EML import via web UI (CLI tool already handles these)
- No IMAP migration via web UI (CLI tool handles this)
- No admin import-for-user feature (future)
- No attachment virus scanning (ClamAV deferred)
- No syntax highlighting library (plain `<pre><code>` is sufficient for v1)
- No image gallery/carousel (simple lightbox modal is sufficient)
- No "Download all attachments as ZIP" (future enhancement)
- No drag-and-drop file upload in compose (future enhancement)
- No inline editing of text attachments
