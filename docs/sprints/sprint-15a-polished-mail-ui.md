# Sprint 15a: Polished Mail UI — Static Prototype & Port

**Phase:** 1 (Foundation)
**Goal:** Create a beautiful, modern mail frontend that captures the warmth and
personality of old Bongo Dragonfly while looking like a 2025 app. Prototype in
static HTML first, iterate until it looks great, then port to Go templates.

---

## Why this sprint exists

Sprint 14 added Bongo colour variables and CSS overrides, but the result looks like
a wireframe with colours applied — flat, cold, no visual rhythm or depth. The problem
was the approach: setting CSS variables blindly without being able to see the result.

**This sprint takes a different approach:**
1. Build standalone HTML prototypes you can open in a browser
2. Use realistic dummy content (fake emails, contacts, events)
3. Iterate on the design until it actually looks good
4. Only then port back to the Go codebase

---

## Design philosophy

The old Dragonfly was groundbreaking for its era. We want to capture what made it
special — the warmth, the personality, the visual clarity — but in a modern idiom.

**What made old Dragonfly great (keep these):**
- Message bubbles with avatars and speech pointers — colour-coded by state
- Blue sidebar with clear hierarchy and mini calendar
- Warm colour palette (orange accents, blue navigation, green for new, gold for sent)
- Information density without clutter
- Clear visual states (you could tell at a glance what was unread/read/sent)

**What to modernise (don't copy these):**
- Table-based layout → flexbox/grid
- PNG gradients → CSS gradients or subtle shadows
- Fixed-width → responsive
- No dark mode → proper dark theme
- 2000s rounded corners → modern border radius
- Image sprites → SVG icons or well-styled Unicode

**Modern design principles to apply:**
- Subtle shadows for depth (not flat, not skeumorphic — somewhere in between)
- Generous but purposeful whitespace
- Consistent spacing scale (use Open Props size tokens)
- Typography hierarchy (size + weight + colour, not just bold/not bold)
- Micro-interactions (hover transitions, subtle feedback)
- Cards and surfaces with just enough elevation to feel layered

**Inspiration:** Think Apple Mail, Fastmail, Hey.com, Proton Mail — modern mail apps
that feel warm and polished, not corporate and sterile.

---

## Reference material

**CRITICAL — Read these old Dragonfly CSS files before starting:**
- `tmp/dragonfly-python/css/layout.css` — sidebar, header, page frame
- `tmp/dragonfly-python/css/conversation.css` — **message bubbles** (the signature look)
- `tmp/dragonfly-python/css/calendar.css` — calendar grid and events
- `tmp/dragonfly-python/css/contacts.css` — contact cards
- `tmp/dragonfly-python/css/composer.css` — compose form
- `tmp/dragonfly-python/css/login.css` — login page
- `tmp/dragonfly-python/css/summary.css` — dashboard

**Screenshots in `tmp/`** — see sprint-14 doc for full list with descriptions.

**Live old Dragonfly UI** — `tmp/dragonfly-python/bongo-standalone.py` runs the
original UI. Login and click through for the real experience.

**Current Go templates** — `internal/web/templates/*.templ` (14 files) for the
actual HTML structure that prototypes need to match.

---

## Tasks

### 1. Set up prototype directory and shared CSS

Create the prototype workspace with shared styles.

**Create `prototype/` directory in project root** with:
- `shared.css` — the design system: colour palette, typography scale, spacing,
  shadows, border radii, component classes. This becomes the new `bongo.css`.
- `pico.min.css` — copy from `internal/web/static/css/` (same base framework)
- `open-props.min.css` — copy from `internal/web/static/css/`
- `alpine.min.js` — copy for interactive components
- `htmx.min.js` — copy (some prototypes may use it for swap demos)

**In `shared.css`, define the design system:**

```css
/* ============================================
   BONGO DESIGN SYSTEM
   ============================================ */

/* --- Colour Palette --- */
:root {
  /* Brand */
  --bongo-orange: #ffac35;
  --bongo-orange-hover: #f57900;
  --bongo-blue: #517faf;
  --bongo-blue-dark: #5f85bf;
  --bongo-blue-light: #bbceea;
  --bongo-blue-lightest: #f2f5fa;

  /* Message states — the signature Dragonfly look */
  --msg-unread-border: #8fd78b;
  --msg-unread-bg: #d2f2cf;
  --msg-unread-header: #43923d;
  --msg-read-border: #ddd;
  --msg-read-bg: #f8f8f8;
  --msg-read-header: #777;
  --msg-sent-border: #ffdb4e;
  --msg-sent-bg: #ffeda6;
  --msg-sent-header: #a78600;

  /* Surfaces & depth */
  --surface-0: #ffffff;
  --surface-1: #f8f9fa;
  --surface-2: #f0f1f3;
  --surface-raised: 0 1px 3px rgba(0,0,0,0.08), 0 1px 2px rgba(0,0,0,0.06);
  --surface-overlay: 0 4px 12px rgba(0,0,0,0.12), 0 2px 4px rgba(0,0,0,0.08);

  /* Typography */
  --font-heading: 'Trebuchet MS', system-ui, sans-serif;
  --font-body: system-ui, -apple-system, sans-serif;
  --font-mono: 'SF Mono', 'Fira Code', monospace;

  /* Spacing (use Open Props --size-* tokens) */
  /* Transitions */
  --transition-fast: 150ms ease;
  --transition-medium: 250ms ease;
}
```

**Done when:** `prototype/index.html` opens in a browser with shared CSS loaded
and looks clean.

### 2. Prototype: Login page

Create `prototype/login.html` — the first thing users see, sets the tone.

**Study:** `tmp/dragonfly-python/css/login.css` and `tmp/Hawkeye_login.png`

**Design target:**
- Full-bleed gradient background (the old blue gradient was beautiful — modernise it)
- Centred card with subtle shadow, rounded corners, warm background
- Bongo logo/wordmark above the form
- Username, Password, Language selector, "Remember me" checkbox
- "Log In" button: prominent, brand colour, satisfying hover state
- The card should feel inviting, not institutional
- Consider a subtle pattern or illustration in the background

**Dummy content:** Pre-filled "admin" / password dots, English selected.

**Done when:** Login page looks like a premium product. You'd be proud to show it.

### 3. Prototype: Inbox / message list

Create `prototype/inbox.html` — the core mail experience.

**Study:** `tmp/dragonfly-python/js/MailWidgets.js` and `css/layout.css`

**Design target — SIDEBAR:**
- Warm blue-tinted sidebar with subtle depth (not flat white, not heavy dark)
- Mailbox list with clear active state (full-width highlight, left accent border)
- Unread counts as subtle badges (not screaming orange pills)
- Mini calendar widget at bottom of sidebar (compact, today highlighted)
- Section separators that feel like breathing room, not harsh lines
- "Compose" button: prominent, inviting, maybe full-width with icon

**Design target — MESSAGE LIST:**
- Table with columns: checkbox, star, from, subject + snippet, date
- Alternating row tint (very subtle, barely there)
- Unread: bold from + subject, subtle left border accent (mint green)
- Hover: gentle background lift with transition
- Selected: blue-tinted background
- Star: warm gold when active, ghost outline when not
- Date: relative ("3m ago", "Yesterday", "Feb 15") in muted colour
- Thread count: small badge after sender name
- Bulk action toolbar: appears when items selected, doesn't jump layout
- Empty state: friendly illustration or icon, not sad and grey

**Design target — READING PANE (two-pane mode):**
- Clean header with From, To, Date, Subject
- Subtle divider between list and reading pane
- Message body with comfortable line-height and readable width
- Quoted text with blue left border (not grey)

**Dummy content:** 15-20 realistic emails — mix of unread/read/starred/sent, various
senders, realistic subjects, some with thread counts (3), (7), etc.

**Done when:** Inbox looks like a modern mail app. You'd want to use it daily.

### 4. Prototype: Message thread (conversation view)

Create `prototype/thread.html` — the **signature Dragonfly experience**.

**Study:** `tmp/dragonfly-python/css/conversation.css` — READ THE ENTIRE FILE.

**Design target — MESSAGE BUBBLES:**
This is what made Dragonfly special. Each message is a "bubble" with:
- **Avatar** on the left (rounded, with initials fallback for unknown contacts)
- **Speech pointer** from bubble to avatar (CSS triangle, modernised)
- **Colour-coded by state:**
  - Unread: mint green tint (`#d2f2cf` → modernise slightly)
  - Read: clean neutral (not grey and depressing — warm neutral)
  - Sent: warm gold tint (`#ffeda6` → modernise slightly)
- **Header area:** sender name (bold), recipients, date, action buttons
- **Body area:** white/clean background, comfortable reading
- **Collapsed state:** click to expand, show snippet preview
- **Action buttons:** Reply, Reply All, Forward — subtle icons, appear on hover
- **Attachments:** below body, clean card style with file type icons

**Modern touches:**
- Smooth expand/collapse animation
- Rounded corners on bubbles (not square boxes)
- Subtle shadow on bubbles for depth
- Avatar with initials + background colour derived from name
- Hover on avatar shows contact popover

**Dummy content:** A 4-message thread with mix of sent/received/unread, one with
attachments, one collapsed.

**Done when:** Thread view is recognisably "Bongo" but feels like 2025. The colour
coding immediately tells you what's new, what you sent, what you've read.

### 5. Prototype: Compose

Create `prototype/compose.html`.

**Study:** `tmp/dragonfly-python/css/composer.css`

**Design target:**
- Clean form layout: To (with autocomplete chips), Cc/Bcc (toggleable), Subject, Body
- Body: generous textarea with toolbar (bold, italic, link, attach)
- "Send" button: prominent, brand colour, left side
- "Save Draft" / "Discard": secondary buttons, right side
- Attachment area: drag-drop zone with file previews
- Overall: feels lightweight and fast, not form-heavy

**Done when:** Compose feels quick and pleasant to use.

### 6. Prototype: Calendar

Create `prototype/calendar.html` — month view with event chips.

**Study:** `tmp/dragonfly-python/css/calendar.css` and calendar screenshots.

**Design target:**
- Month grid with generous day cells
- Today: warm highlight (orange or blue tint, not jarring)
- Event chips: colour-coded, rounded, readable at small size
- Navigation: clean prev/next with month/year display
- Day numbers: clear hierarchy (current month bold, other months muted)
- Week view: time grid with timed events as positioned blocks

**Dummy content:** A month with 8-10 events scattered across days.

**Done when:** Calendar feels like Google Calendar quality (clean, colourful, usable).

### 7. Prototype: Contacts

Create `prototype/contacts.html`.

**Study:** `tmp/dragonfly-python/css/contacts.css` and `Dragonfly-newcontact.png`

**Design target:**
- Contact list: avatar + name + email + phone in a clean row
- Contact detail: card layout with avatar, all fields, edit/delete actions
- Search/filter bar at top
- "New Contact" button consistent with Compose button style

**Done when:** Contacts feel organized and approachable.

### 8. Prototype: Settings + Security

Create `prototype/settings.html`.

**Design target:**
- Clean settings form with grouped sections (Appearance, Mail, Security)
- Toggle switches for on/off settings (not checkboxes)
- Dropdown selects with consistent styling
- 2FA setup section with QR code area and recovery codes
- Password change section
- Preview: show live theme/density changes

**Done when:** Settings feel like iOS Settings — clean, grouped, intuitive.

### 9. Port prototypes to Go codebase

Once ALL prototypes look good, port the design back.

**Process:**
1. Copy `prototype/shared.css` → `internal/web/static/css/bongo.css` (replace entirely)
2. For each `.templ` file, compare against the prototype HTML:
   - Match the HTML structure (add wrapper divs, classes as needed)
   - Ensure all CSS classes from prototypes exist in templates
   - Keep all existing functionality (HTMX attributes, Alpine.js, i18n `t()` calls)
   - Keep all ARIA attributes and accessibility features
3. Verify favicon, logo, any new static assets are in `internal/web/static/`

**Critical: Don't break functionality during the port.**
- Every HTMX `hx-get`, `hx-post`, `hx-target`, `hx-swap` must be preserved
- Every Alpine.js `x-data`, `x-on:`, `x-show` must be preserved
- Every `t(ctx, "...")` i18n call must be preserved
- Every `aria-label`, `role`, `aria-live` must be preserved
- Test by running the actual Go server after each template port

**Done when:** Running bongo-go looks identical to the prototypes. All existing
functionality works. Tests pass.

### 10. Visual review and iteration

Final polish pass.

**Verify every page in the running app:**
1. Login → looks like prototype
2. Inbox (empty) → friendly empty state
3. Inbox (with messages) → unread/read/starred states clear
4. Message thread → bubbles, colours, avatars, collapse/expand
5. Compose → form, autocomplete, attachments
6. Calendar month → grid, events, navigation
7. Calendar week → time grid, positioned events
8. Contacts list → clean rows
9. Contact detail → card layout
10. Settings → grouped forms, toggles
11. Security → 2FA setup, password change
12. Search results → consistent with inbox styling
13. Import → clean upload form

**Test responsive at 320px, 768px, 1024px, 1440px:**
- Sidebar collapses on mobile
- Message list adapts
- Reading pane stacks vertically on small screens
- Calendar simplifies on mobile

**Dark mode:**
- Verify all pages look good in dark theme
- Colours adapt (muted versions of unread green, sent gold, etc.)
- No white flashes, no contrast issues

**Done when:** Every page looks polished in both themes at all breakpoints.

---

## Exit Criteria

- [ ] `prototype/` directory with shared.css design system
- [ ] Login prototype: gradient background, centred card, warm and inviting
- [ ] Inbox prototype: sidebar + message list + reading pane, all states visible
- [ ] Thread prototype: message bubbles with avatars, speech pointers, colour coding
- [ ] Compose prototype: clean form with chips, drag-drop attachments
- [ ] Calendar prototype: month grid with event chips, today highlight
- [ ] Contacts prototype: list + detail card views
- [ ] Settings prototype: grouped forms with toggles
- [ ] All prototypes look like a premium modern mail app (not a wireframe)
- [ ] CSS ported to `internal/web/static/css/bongo.css`
- [ ] Template structure updated in `.templ` files to match prototypes
- [ ] All HTMX/Alpine.js/i18n/ARIA attributes preserved during port
- [ ] Login page matches prototype in running app
- [ ] Inbox matches prototype in running app (with real data)
- [ ] Thread view matches prototype in running app
- [ ] Compose matches prototype in running app
- [ ] Calendar matches prototype in running app
- [ ] Contacts matches prototype in running app
- [ ] Settings + Security match prototype in running app
- [ ] Responsive works at 320px, 768px, 1024px, 1440px
- [ ] Dark mode looks good on all pages
- [ ] `go test -race ./...` passes
- [ ] `go test -race -tags=e2e ./test/e2e/` passes

## What NOT to do

- Don't add new features — visual changes only
- Don't remove existing functionality during port
- Don't pixel-match the old Bongo — capture the spirit in modern form
- Don't skip the prototype step — that's the whole point of this sprint
- Don't use heavyweight frameworks (Tailwind, Bootstrap) — keep Pico + custom CSS
- Don't create separate CSS files per page — one unified `bongo.css` design system
