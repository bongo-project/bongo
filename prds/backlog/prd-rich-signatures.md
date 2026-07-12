# PRD: Rich Signatures & HTML Composer

## 1. Introduction/Overview

Bongo currently supports plain-text signatures only (`UserPreferences.Signature`, a
`<textarea>` in Settings, appended by `compose.go signatureBody()` as `"\n\n-- \n" + sig`)
and a plain-text compose body (`<textarea name="body">` sent as `text/plain`, or
`multipart/mixed` when file attachments are present).

This feature adds an HTML signature (logo, colours, formatting) with a plain-text fallback,
and a WYSIWYG compose body editor, so users can send formatted mail. Rich mail is sent as
`multipart/alternative` (text/plain + text/html) so it degrades gracefully in plain-text
clients. Inline images (including signature logos) are embedded as `cid:` inline attachments
so they survive real email clients (Gmail, Outlook).

## 2. Goals

- Users can compose and send formatted (HTML) email with a graceful plain-text fallback.
- Users can define an HTML signature with formatting and an inline logo.
- Plain-text-only users are completely unaffected (mail still goes out as `text/plain`).
- No external CDN, no JS build step (per project constraints).

## 3. Design Decisions (from planning)

- **Scope:** Full rich composer AND HTML signatures.
- **Editor:** Lightweight `contenteditable` WYSIWYG Alpine component with a formatting
  toolbar and an HTML-source toggle. No external library.
- **Inline images:** Stored/edited as `data:` URIs inside the HTML (so the Settings preview
  and editor work directly). At **send** time, each `data:` image is extracted into a `cid:`
  inline part; the HTML `<img src>` is rewritten to `cid:`.
- **Compose toggle:** A plain/rich toggle in the composer. Plain mode sends `text/plain`
  exactly as today; rich mode produces `multipart/alternative`.
- **MIME tree (rich, with inline images and file attachments):**
  `multipart/mixed[ multipart/related[ multipart/alternative[ text/plain, text/html ], inline-images… ], file-attachments… ]`.
  Simpler trees are used when related/mixed layers are unnecessary.

## 4. User Stories

### US-001: HTML signature in Settings
**Description:** As a user, I want to create a formatted signature so my sent mail looks
professional.

**Acceptance Criteria:**
- [ ] The Settings > Composition section shows a WYSIWYG editor for the signature with a
      toolbar containing at least Bold, Italic, Link, Text colour, and Insert image buttons.
- [ ] A "source" toggle switches the editor between WYSIWYG and raw-HTML editing; edits in
      one view are reflected in the other after toggling.
- [ ] Saving a signature of `<b>Jane</b>` persists `signature_html` containing `<b>Jane</b>`
      and a derived plain-text `signature` equal to `Jane`.
- [ ] Saving a signature containing `<script>alert(1)</script>` persists HTML with the
      `<script>` tag removed (sanitized).

### US-002: Plain-text fallback preserved
**Description:** As a user with no HTML signature, my mail must be unchanged.

**Acceptance Criteria:**
- [ ] A user whose `signature_html` is empty and `signature` is `Regards, Jane` sends mail
      whose body is a single `text/plain` part ending with `\n\n-- \nRegards, Jane` (no
      `text/html` part, no `multipart/alternative`).

### US-003: Rich compose with plain/rich toggle
**Description:** As a user, I want to write formatted mail, but also force plain text when I
choose.

**Acceptance Criteria:**
- [ ] The composer has a plain/rich toggle. In **plain** mode, a message with body
      `hello` is sent as a single `text/plain` part with body `hello` (+ plain signature).
- [ ] In **rich** mode, a message whose HTML body is `<p>hello</p>` is sent as
      `multipart/alternative` containing a `text/plain` part with `hello` and a `text/html`
      part containing `<p>hello</p>`.
- [ ] In rich mode, the composer prefills with the user's HTML signature appended to the body.

### US-004: Inline logo via cid
**Description:** As a user, I want a logo image in my signature that recipients can see.

**Acceptance Criteria:**
- [ ] A signature HTML containing `<img src="data:image/png;base64,iVBOR...">` sent in rich
      mode produces a `multipart/related` tree where the `text/html` part references the
      image via `cid:` and a sibling inline part carries `Content-ID: <...>`,
      `Content-Disposition: inline`, and the decoded image bytes.
- [ ] The `data:` URI does not appear in the sent `text/html` part (it is rewritten to `cid:`).

### US-005: Received mail renders
**Description:** As a recipient, formatted mail with an inline logo displays correctly.

**Acceptance Criteria:**
- [ ] Integration test: set an HTML signature with a logo, compose a rich message, send it,
      and assert the received message parses to `multipart/alternative` with both a
      `text/plain` and a `text/html` part, and an inline image resolvable by its `cid:`.

## 5. Functional Requirements

- FR-1: Add `signature_html` (string) to `UserPreferences` proto; keep `signature` (string)
  as the plain-text fallback. Regenerate `.pb.go`.
- FR-2: PreferenceStore reads/writes `signature_html`.
- FR-3: A server-side sanitizer restricts signature/compose HTML to an allow-list of
  formatting tags, links (`http`/`https`/`mailto`), and `img` with `data:` or `cid:` sources
  only; inline `style` limited to colour-related properties. Reuse the existing display
  sanitizer where possible.
- FR-4: On signature save, derive an editable plain-text version from the HTML.
- FR-5: A MIME builder constructs the correct tree: `multipart/alternative` for text+html,
  wrapped in `multipart/related` when inline `cid:` images exist, wrapped in
  `multipart/mixed` when file attachments exist. Plain-only → single `text/plain`.
- FR-6: A helper extracts `data:` image URIs from HTML into `cid:` inline parts and rewrites
  the HTML `<img src>` accordingly, generating stable Content-IDs.
- FR-7: A reusable `contenteditable` WYSIWYG Alpine component (in `bongo.js`) with toolbar
  (bold, italic, link, colour, insert-image), HTML-source toggle, and live preview; posts
  HTML via a hidden field. Insert-image reads a local file into a `data:` URI with a size cap.
- FR-8: The composer has a plain/rich toggle; plain mode reproduces current `text/plain`
  behaviour exactly.
- FR-9: Compose prefill appends the HTML signature (rich) / plain signature (plain).
- FR-10: New UI strings are i18n-wrapped; toolbar buttons have `aria-label`s and are
  keyboard-operable.

## 6. Non-Goals (Out of Scope)

- External rich-text libraries (TipTap/ProseMirror/Quill) or any JS build step.
- Signature templates/gallery or multiple signatures per user.
- Rich-text editing of received/quoted message bodies beyond appending signature.
- Remote (`http:`) image loading in signatures (blocked by sanitizer; `data:`/`cid:` only).

## 7. Technical Considerations

- CSP already allows `img-src` `cid:` and `data:`.
- Existing HTML display sanitization lives in the mail render path (`message.templ` / mail
  handler) — reuse its policy as the basis for FR-3.
- Current message construction is in `compose.go` (`signatureBody`, the `multipart/mixed`
  writer around lines 640–710) and must be refactored, not duplicated.
- Enforce a per-image and total-message size cap to avoid oversized `data:`/`cid:` payloads.

## 8. Success Metrics

- A rich message with an inline logo is received and rendered correctly by a mainstream
  client (verified via integration test asserting MIME structure).
- Zero change to sent output for users without an HTML signature using plain mode.

## 9. Open Questions

- Total inline-image size cap value (proposed: reuse existing `max_message_size`, plus a
  per-image cap e.g. 1 MB). To be confirmed during implementation.
