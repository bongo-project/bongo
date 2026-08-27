# Sprint 14: Visual UI Matching — Old Bongo Dragonfly/Hawkeye

**Phase:** 1 (Foundation) — FINAL SPRINT
**Goal:** Match the bongo-go web UI to the visual style and feel of the original Bongo
Dragonfly (webmail) and Hawkeye (admin) interfaces, while keeping the modern stack
(HTMX, Alpine.js, Pico CSS, Open Props).

---

## Context

The bongo-go UI is fully functional but uses generic Pico CSS defaults. The original
Bongo had a distinctive look: a blue/orange/lime colour palette, gradient backgrounds,
specific sidebar styling, message thread bubbles with avatars, and a recognisable visual
identity. This sprint is **visual/design-driven** rather than feature-driven — no new
features, just CSS, layout, spacing, colours, iconography, and interaction polish.

**Reference material — READ THESE BEFORE CODING:**

### Screenshots (visual targets) — `tmp/`
- `Dragonfly-summary.png` — Dashboard: sidebar with mini calendar + contact list,
  starred mail, mail from contacts, upcoming events. Shows sidebar blue active state.
- `Dragonfly-calendar-month.png` — Month grid with colour-coded event chips (green
  holidays, orange all-day, grey timed). View toggle bar (Day|Upcoming|Week|Month).
- `Dragonfly-calendar-week.png` — Week view with time grid, all-day bar at top,
  timed event blocks. Grey event block with resize handle dots.
- `Calendar-sharing-view.jpg` — Week view with multiple calendars (colour-coded),
  event detail cards with avatar photos, "Join this event" button, calendar sidebar
  with checkbox toggles per calendar.
- `Calendar-sharing-joining-event.jpg` — Event join dialog with attendees, message
  field, "Add others" selector. Blue rounded card style.
- `Dragonfly-calendar-sidebar.png` — Day click popup: event list for a date with
  coloured dots per calendar, "view in calendar" link.
- `Dragonfly-calendar-newevent.png` — Natural language event creation ("release
  Dragonfly tomorrow at 11:13"). Yellow tooltip popover.
- `Dragonfly-event-edit.png` — Event editor: tabbed (Event|Sharing|Repeat), date/time
  inputs, note field, Delete/Cancel/Save buttons.
- `Dragonfly-event-view.png` — Event detail popover: title, location, date/time,
  Delete/Edit links. Yellow tooltip style.
- `Dragonfly-newcontact.png` — Contact form: avatar placeholder, Personal/Home/Work
  tabs, Email/Mobile/IM/Website fields. Popup over calendar.
- `Dragonfly-invitation-accept.png` — Calendar invitation card with subscribe/junk
  actions. Yellow card style.
- `Dragonfly-summary-frommycontacts.png` — "Mail from my contacts" section with star
  icons and message counts.
- `Dragonfly-summary-lists.png` — "Mail to mailing lists" section.
- `Hawkeye-login.png` / `Hawkeye_login.png` — Two variants of admin login: blue
  gradient background, centred card, Bongo logo, username/password/language fields.
- `Hawkeye-desktop.png` / `Hawkeye_desktop.png` — Admin dashboard: dark sidebar
  (Desktop/Users/Agents/System/Help with colourful icons), "Welcome to Bongo!" with
  server illustration, blue status table (Agent Status, Bongo Updates, Memory, Load).
- `Hawkeye_desktop_toggled.png` — Dashboard with sidebar collapsed.
- `Hawkeye_agents.png` — Agent grid: 3x3 grid of large illustrated icons (wrench for
  Enable/Disable, envelope truck for SMTP, glasses for IMAP, ladybug for Antivirus,
  mail truck for POP3, globe for Global, bridge for Mail proxy, monitor for Queue,
  cloud for Antispam).
- `Hawkeye_server.png` — Enable/Disable agents: table with agent names + checkboxes.
- `Hawkeye_agents_smtp.png` — SMTP config: label-value form (SSL Port, Socket timeout,
  Port, Maximum recipients, Message size limit, etc.).
- `Hawkeye_agents_queue.png` — Queue config form.
- `Hawkeye_agents_antispam.png` — Antispam config: hosts textarea + timeout.
- `Hawkeye_agents_antivirus.png` — Antivirus config.
- `Hawkeye_agents_global.png` — Global config: postmaster, host address, hostname.
- `Hawkeye_agents_pop3.png` — POP3 config: Port, SSL Port.
- `Hawkeye_agents_imap.png` — IMAP config: Port, SSL Port, Maximum threads.
- `Hawkeye_agents_mailproxy.png` — Mail proxy config.

### Full Dragonfly source — `tmp/dragonfly-python/`
This is the complete old Dragonfly web UI source. Key files to study:

**CSS (read these for exact values):**
- `css/layout.css` — Overall page frame, sidebar, header, search bar, login page.
  Key: sidebar width 14.5em, tab gradient styling, colour variables.
- `css/conversation.css` (**MOST IMPORTANT**) — Message bubble threading style:
  - Lines 42-51: `.msg` class — positioned left of 48px avatar with speech pointer
  - Lines 53-93: Avatar (`.face`), CSS triangle pointer (`.pointer-border`, `.pointer`)
  - Lines 131-168: `.msg-content` with 2px border, gradient header, white body
  - Lines 302-325: **Colour states** — this is the signature Dragonfly look:
    - Unread: border `#8fd78b` (mint green), background `#d2f2cf`, header `#43923d`
    - Read: border `#ddd`, background `#eee`, header `#777`
    - Sent: border `#ffdb4e` (gold), background `#ffeda6`, header `#a78600`
  - Lines 225-267: Collapsed messages (`.msg.closed`) — snippet at 75% opacity
  - Lines 170-223: Action buttons (Reply/Forward) float right with 16px icon backgrounds
- `css/calendar.css` — Calendar grid, event chips, time labels, mini calendar.
- `css/contacts.css` — Contact cards and address book.
- `css/composer.css` — Compose form styling.
- `css/summary.css` — Dashboard/summary page sections.
- `css/login.css` — Login page: centred card with background gradient.
- `css/widgets.css` — Shared UI widgets.
- `css/admin.css` — Hawkeye admin panel (referenced via `hawkeye/` directory).

**JavaScript (read these for UI structure):**
- `js/MailWidgets.js` — `ConversationList` builds inbox table: checkbox, star, from
  (with thread count), subject (truncated 55 chars), date. Alternating `odd`/`even` rows.
- `js/MailConversationView.js` — Message thread view: subject heading, message bubbles
  with expand/collapse, avatar placeholders, Reply/Reply All/Forward links per message.
- `js/MailListView.js` — List view with pager, "No messages here" empty state.
- `js/Summary.js` — Dashboard sections: starred mail, mail from contacts, mailing lists.
- `js/Composer.js` — Compose form structure.

**Images to reuse:**
- `img/bongo-favicon.ico` / `img/bongo-favicon.png` — Favicon (copy to static assets)
- `img/contact-unknown.png` — Default avatar placeholder (48x48)
- Consider recreating the icon style in SVG rather than using old PNGs

### LIVE OLD DRAGONFLY UI — `tmp/dragonfly-python/`
The old Dragonfly UI is **runnable**. A Python backend in `tmp/dragonfly-python/` serves
the full original UI with login, mail, calendar, contacts, and compose. Use this as
the primary visual reference — run it and click through every page to see the actual
rendering, colours, spacing, and interaction patterns live.

**To run:** `cd tmp/dragonfly-python && python bongo-standalone.py` (check the script
for port/config). Login and navigate: Summary → Mail → Calendar → Contacts → Compose.
Take screenshots of each view for side-by-side comparison with bongo-go.

This is far more valuable than static screenshots — you can see hover states, active
states, transitions, and the full conversation thread bubble view with real messages.

### Old Bongo compiled source — `src/www/` in a bongo-project/bongo-project checkout
Same CSS files as above but this is the packaged version. 160+ image assets in `img/`.
Use `tmp/dragonfly-python/` as the primary reference (easier to navigate).

**Current CSS stack:**
- `pico.min.css` (83KB) — classless framework, provides base styles
- `open-props.min.css` (29.5KB) — CSS design tokens/variables
- `bongo.css` (1,262 lines) — all custom styling
- Theme: auto/light/dark via `data-theme` attribute + Pico CSS variables

**Old Bongo colour palette (from layout.css):**
- Header/accent: `#ffac35` (warm orange)
- Sidebar active: `#bbceea` (light blue), hover: `#f2f5fa`
- Sidebar border: `#5f85bf` (slate blue)
- Links: `#517faf` (teal blue), hover: `#f57900` (orange)
- Page background: `#e7e4ef` (light purple)
- Selection: `#7ed12d` (lime green)
- Unread: `#8fd78b` (mint green)
- Sent: `#ffdb4e` (yellow gold)
- Borders: `#ddd`, `#999`, `#5f85bf`

**Current iconography:** Unicode symbols only (☰, ☀, ☾, ✉, ★). No SVG or icon font.

---

## Tasks

### 1. Study reference material and extract design tokens

Before making changes, study the old Dragonfly/Hawkeye source and screenshots.

**Read the key CSS files listed in the reference section above.** Extract:
- Exact colour hex values for all states (unread/read/sent/active/hover)
- Font families, sizes, weights
- Spacing values (padding, margins, sidebar width)
- Border styles and radii
- Shadow and gradient patterns

**Study the screenshots in `tmp/`** to understand:
- Visual hierarchy and information density
- How the sidebar, message list, and reading pane work together
- Calendar grid proportions and event chip sizing
- Admin panel layout and status table styling
- Login page composition and branding

**Key design tokens to extract:**

| Token | Old Value | Usage |
|-------|-----------|-------|
| Sidebar active bg | `#bbceea` | Active mailbox/section |
| Sidebar hover bg | `#f2f5fa` | Hover on sidebar items |
| Sidebar border | `#5f85bf` | Sidebar edge accent |
| Header accent | `#ffac35` | Top bar orange stripe |
| Link colour | `#517faf` | All links |
| Link hover | `#f57900` | Link hover state |
| Unread border | `#8fd78b` | Unread message bubble border |
| Unread bg | `#d2f2cf` | Unread message bubble fill |
| Unread header | `#43923d` | Unread message sender text |
| Read border | `#ddd` | Read message bubble border |
| Read bg | `#eee` | Read message bubble fill |
| Sent border | `#ffdb4e` | Sent message bubble border |
| Sent bg | `#ffeda6` | Sent message bubble fill |
| Sent header | `#a78600` | Sent message sender text |
| Star colour | `#f0ad4e` | Flagged/starred indicator |
| Page bg | `#e7e4ef` | Light purple page background |
| Msg content border | 2px solid | Message bubble outer border |
| Avatar size | 48x48px | Contact avatar placeholder |
| Sidebar width | 14.5em (~232px) | Sidebar panel |
| Subject truncate | 55 chars | Inbox subject column |
| Font family | Trebuchet MS, Bitstream Vera Sans | Headings |

**Done when:** Design tokens documented. Agent has clear picture of target aesthetic.

### 2. Colour palette and CSS variables

Map old Bongo colours to Pico CSS custom properties for both light and dark themes.

**Modify `internal/web/static/css/bongo.css`:**

Add Bongo-branded CSS variables (light theme):
```css
:root {
  --bongo-orange: #ffac35;
  --bongo-orange-hover: #f57900;
  --bongo-blue: #517faf;
  --bongo-blue-dark: #5f85bf;
  --bongo-blue-light: #bbceea;
  --bongo-blue-lightest: #f2f5fa;
  --bongo-green: #7ed12d;
  --bongo-green-light: #8fd78b;
  --bongo-gold: #ffdb4e;
  --bongo-purple-light: #e7e4ef;
  --bongo-bg: #ffffff;
  --bongo-text: #333333;
  --bongo-border: #dddddd;
  --bongo-border-strong: #999999;
}
```

**Override Pico CSS variables to use Bongo palette:**
```css
:root[data-theme="light"], :root:not([data-theme="dark"]) {
  --pico-primary: var(--bongo-blue);
  --pico-primary-hover: var(--bongo-orange-hover);
  --pico-primary-background: var(--bongo-blue);
  --pico-primary-focus: var(--bongo-blue-light);
  --pico-secondary: var(--bongo-orange);
}
```

**Dark theme variant:**
- Darken the Bongo palette appropriately (deeper blues, muted orange)
- Keep brand colours recognisable but comfortable in dark mode

**Done when:** Light theme matches old Bongo colour feel. Dark theme is a
complementary dark variant of the same palette. All interactive elements
(links, buttons, focus rings) use Bongo brand colours.

### 3. Header and sidebar styling

Match the top bar and sidebar to the original Dragonfly layout.

**Modify top bar styling in `bongo.css`:**
- Add orange accent border on top or bottom of header:
  `border-top: 3px solid var(--bongo-orange);`
- Background: subtle gradient or solid matching old header
- Brand text "Bongo" styling: match original logo font weight/size
- Search input: rounded, matching original search box appearance

**Modify sidebar styling:**
- Active mailbox: `background: var(--bongo-blue-light)` with left border accent
- Hover state: `background: var(--bongo-blue-lightest)`
- Unread badge: pill shape with `background: var(--bongo-orange)`
- Mailbox icons: consider adding small SVG or emoji icons before mailbox names
  (📥 Inbox, 📤 Sent, 📝 Drafts, 🗑 Trash, ⚠ Junk) — keep it subtle
- Section dividers: match original separator styling
- Sidebar width: keep 240px (close to old 14.5em ≈ 232px)

**Add Bongo logo/favicon:**
- Copy `src/www/img/bongo-favicon.png` from a bongo-project/bongo-project checkout to `internal/web/static/img/`
- Copy or recreate logo for brand area
- Add `<link rel="icon">` in layout.templ
- Reference logo in PWA manifest if applicable

**Done when:** Sidebar has the blue accent colour scheme. Header has orange
accent. Favicon displays in browser tab. Overall frame feels like old Bongo.

### 4. Message list and conversation thread view

Match the mail list density, message states, and the **signature Dragonfly conversation
bubble threading** — this is the most distinctive visual element of old Bongo.

**Message list styling (inbox table):**
Reference: `tmp/dragonfly-python/js/MailWidgets.js` → `buildRow()` and
`tmp/dragonfly-python/css/layout.css` → `.conversation-list` styles.
- Table columns: checkbox (narrow), star, from (with thread count in parens), subject
  (truncated), date
- Alternating row colours (`odd`/`even` classes)
- Unread rows: bold font weight
- Starred: gold star icon (`#f0ad4e`)
- Selected: `background: var(--bongo-blue-lightest)` (matching old selection)
- Row hover: subtle blue tint
- Date column: show time for today, date for older messages (match original logic)

**Conversation thread view — MESSAGE BUBBLES (key differentiator):**
Reference: `tmp/dragonfly-python/css/conversation.css` — read the entire file.
This is the signature Dragonfly look. Each message in a thread is a "bubble" with:

- **Avatar** on the left (48x48, `contact-unknown.png` placeholder), absolutely
  positioned at `left: -60px` from the message content
- **CSS speech pointer** — a triangle pointing from the bubble to the avatar, made
  with CSS borders (`.pointer-border` outer, `.pointer` inner white)
- **Colour-coded by state:**
  - Unread: `#8fd78b` border, `#d2f2cf` body bg, `#43923d` header text — mint green
  - Read: `#ddd` border, `#eee` body bg, `#777` header text — neutral grey
  - Sent: `#ffdb4e` border, `#ffeda6` body bg, `#a78600` header text — warm gold
- **Message content** has 2px coloured border, gradient header area, white body area
- **Collapsed state**: body hidden, snippet shown at 75% opacity, pointer hidden
- **Disclosure triangle**: CSS triangle that rotates between expanded (down) and
  collapsed (right) states
- **Action buttons** (Reply, Reply All, Forward): float right in header with icons
- **Attachments section**: below body, lighter background variant of bubble colour

Modern implementation approach:
- Use CSS flexbox instead of absolute positioning for avatar + bubble layout
- CSS custom properties for the three colour states
- CSS `clip-path` or border-trick for speech pointer
- `aria-expanded` for accessible collapse/expand
- Smooth CSS transition on collapse/expand

**Print stylesheet:**
- Verify `@media print` styles hide nav, show full message content

**Done when:** Message list matches old Dragonfly inbox feel. Thread view shows
colour-coded message bubbles with avatars and speech pointers. Collapsed messages
show snippet. The overall effect is recognisably "Bongo Dragonfly" but modern.

### 5. Compose flow and calendar views

Polish compose form and calendar to match original appearance.

**Compose form:**
- Match original composer layout (old `composer.css`):
  - To/Cc/Bcc fields with contact chips or clear styling
  - Subject field prominent
  - Body textarea: comfortable height, subtle border
  - Toolbar: send button prominent (orange/blue accent), secondary actions muted
- File attachment area: drag-drop zone styling matching modern patterns
  but with Bongo colour accents

**Calendar views:**
- Month view: match old `calendar.css` grid appearance
  - Day cells: adequate height, border styling matching original
  - Today: highlighted with Bongo blue/orange accent
  - Event chips: colour-coded per calendar, rounded, matching original density
  - Weekend columns: subtle background differentiation if original had it
- Week/day views: time grid with hour labels, matching original time display
- Mini calendar in sidebar: compact, matching original sidebar calendar widget
  (table layout, small font, today highlighted)

**Done when:** Compose form and calendar views feel visually consistent with the
rest of the Bongo-styled UI and echo the original layout patterns.

### 6. Contacts, admin panel, and login page

Polish remaining views.

**Contacts:**
- Contact cards: match original address book card appearance
- List view: clean table/list matching overall message list style
- Contact detail: photo area, field layout matching original contact view
- Match original `contacts.css` layout patterns where appropriate

**Admin panel (Hawkeye):**
- Dashboard: stat cards with Bongo colour accents
- Match original Hawkeye admin layout from screenshots:
  - Sidebar navigation consistent with main app
  - Tables with zebra striping or subtle borders
  - Status badges with Bongo palette colours
  - Action buttons consistent with rest of UI
- User management, domain management, queue views: match admin density

**Login page:**
- Match original login screen aesthetic (old `login.css`):
  - Centred card with Bongo branding
  - Logo above login form
  - Orange/blue accent colours on form elements
  - Clean, professional appearance

**Done when:** All views share consistent Bongo visual identity. Admin panel
feels like the Hawkeye counterpart. Login page is branded.

### 7. Iconography and interaction polish

Replace Unicode symbols with proper icons and polish micro-interactions.

**Icon strategy — choose ONE approach:**
- Option A: Small inline SVGs (hand-crafted or from a free icon set like Lucide)
  — best for performance, no external dependency
- Option B: Unicode emoji with consistent styling — simplest, already partially done
- Option C: Icon font (Feather, Lucide) — easy to use, adds a dependency

**Replace key Unicode symbols:**
- ☰ hamburger → proper SVG hamburger icon
- ☀/☾ theme toggle → SVG sun/moon icons
- ✉ empty state → SVG envelope illustration
- ★/☆ flag toggle → SVG star icons with smooth fill animation
- ⬅ back arrow, navigation arrows → SVG chevron icons
- Layout view toggle icons → clearer SVG representations

**Micro-interaction polish:**
- Smooth transitions on sidebar hover/active states (150ms ease)
- Message row hover: gentle background fade (not instant)
- Star toggle: scale animation on click
- HTMX content swap: subtle fade-in (using `htmx-settling` class)
- Mobile sidebar: smooth slide-in/out with backdrop fade
- Button hover: colour transition matching old Bongo button gradients
- Loading states: Bongo-branded spinner (replace default HTMX indicator)

**Focus styles:**
- Visible focus ring using `var(--bongo-orange)` outline
- Match Bongo colour for keyboard navigation visibility

**Done when:** All icons are consistent in style. Transitions feel smooth and
polished. UI responds to interactions with appropriate visual feedback. Focus
styles are visible and branded.

### 8. Side-by-side comparison and iteration

Compare new UI against old Bongo screenshots and iterate.

**Process:**
- Run bongo-go dev server in one browser tab
- Run old Dragonfly UI (`tmp/dragonfly-python/bongo-standalone.py`) in another tab
- Walk through each view:
  1. Login page
  2. Inbox / message list
  3. Message reading
  4. Compose
  5. Calendar (month, week, day)
  6. Contacts (list, detail)
  7. Settings / preferences
  8. Admin dashboard
  9. Admin user management
  10. Admin queue/logs
- Note discrepancies in: spacing, colour usage, font sizes, border styles,
  overall density, visual hierarchy
- Fix top discrepancies iteratively

**Responsive check:**
- Verify mobile view still works well with new styling
- Test at 320px, 768px, 1024px, 1440px widths
- Ensure sidebar behaviour, reading pane modes all adapt correctly

**Done when:** UI team (user) is satisfied with visual alignment. Major layout
patterns, colour usage, and overall feel match the original Bongo aesthetic
while maintaining modern stack advantages.

### 9. Integration tests for visual regression baseline

Ensure existing tests still pass and capture baseline for visual consistency.

**Run full test suite:**
- `go test -race ./...` — unit + package tests
- `go test -race -tags=integration ./test/integration/` — integration tests
- `go test -race -tags=e2e ./test/e2e/` — E2E smoke tests

**Add basic visual checks to E2E:**
- Verify CSS file serves with 200 status and correct Content-Type
- Verify static assets (favicon, logo if added) serve correctly
- Verify key CSS classes exist in rendered HTML (`.bongo-branded` or similar)
- Verify theme switching still works (data-theme attribute toggles)

**Check no accessibility regressions:**
- Run existing `TestAccessibilityBasics` — skip-link, landmarks, ARIA labels
- Verify colour contrast is sufficient (manual check for Bongo palette)
- Ensure focus styles are visible with new colour scheme

**Done when:** All existing tests pass. No regressions in accessibility or
functionality. Visual changes are CSS/asset-only, no logic changes.

---

## Exit Criteria

- [ ] Reference screenshots captured or documented from old Bongo UI
- [ ] Bongo colour palette CSS variables defined (light + dark themes)
- [ ] Pico CSS variables overridden with Bongo brand colours
- [ ] Header has orange accent, matches old Bongo header feel
- [ ] Sidebar uses blue colour scheme (active/hover states match original)
- [ ] Favicon added from old Bongo assets
- [ ] Message list: unread/read/flagged states use Bongo colours
- [ ] Message list row density matches old Dragonfly appearance
- [ ] Reading pane styled with Bongo palette (quoted text, headers)
- [ ] Compose form matches original composer layout feel
- [ ] Calendar views styled with Bongo colour accents
- [ ] Contacts and admin panel visually consistent with Bongo theme
- [ ] Login page branded with Bongo logo and colour scheme
- [ ] Unicode icons replaced with consistent SVG or styled icons
- [ ] Smooth transitions on hover, active, and HTMX swap states
- [ ] Focus styles use Bongo brand colour (visible keyboard navigation)
- [ ] Mobile responsive still works correctly with new styling
- [ ] Side-by-side comparison reviewed — UI matches old Bongo feel
- [ ] `go test -race ./...` passes (no regressions)
- [ ] `go test -race -tags=e2e ./test/e2e/` passes
- [ ] No accessibility regressions (skip-link, ARIA, landmarks intact)

## What NOT to do in this sprint

- No new features — visual changes only (CSS, assets, minor template tweaks)
- No pixel-perfect reproduction (match feel and colour scheme, not exact pixels)
- No removal of modern features (dark mode, responsive, accessibility)
- No reintroduction of table-based layout or iframes from original
- No PNG gradient images — use CSS gradients instead
- No IE6 compatibility hacks from original CSS
- No JavaScript UI behaviour changes (only CSS transitions/animations)
- No changes to Go handlers, protobuf, or business logic
