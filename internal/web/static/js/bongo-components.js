// Bongo Alpine.js component registrations.

document.addEventListener('alpine:init', () => {
  function swipeOffsetClass(deltaX) {
    var clamped = Math.max(-800, Math.min(800, deltaX));
    var bucket = Math.round(clamped / 4);
    return 'swipe-x-' + bucket;
  }

  function contextPositionClass(prefix, value) {
    var clamped = Math.max(0, Math.min(2040, value));
    var bucket = Math.round(clamped / 8);
    return prefix + bucket;
  }

  function setSwipeDragState(el, deltaX) {
    if (!el) return;
    el.classList.remove('msg-row-swipe-animating', 'msg-row-swipe-dismiss-left', 'msg-row-swipe-dismiss-right');
    replacePrefixedClass(el, 'swipe-x-', swipeOffsetClass(deltaX));
  }

  function finalizeSwipeState(el, direction) {
    if (!el) return;
    el.classList.remove('msg-row-swipe-dismiss-left', 'msg-row-swipe-dismiss-right');
    el.classList.add('msg-row-swipe-animating');
    el.classList.add(direction === 'left' ? 'msg-row-swipe-dismiss-left' : 'msg-row-swipe-dismiss-right');
    replacePrefixedClass(el, 'swipe-x-', null);
  }

  function resetSwipeState(el) {
    if (!el) return;
    el.classList.remove('msg-row-swipe-dismiss-left', 'msg-row-swipe-dismiss-right');
    el.classList.add('msg-row-swipe-animating');
    replacePrefixedClass(el, 'swipe-x-', null);
  }

  // App-level state: theme, layout, density
  Alpine.data('appState', () => ({
    theme: localStorage.getItem('bongo-theme') || 'auto',
    layoutMode: localStorage.getItem('bongo-layout') || 'list',
    density: localStorage.getItem('bongo-density') || 'comfortable',
    sidebarOpen: false,
    sidebarReturnFocus: null,

    init() {
      this.applyTheme();
      this.applyDensity();
      this.watchOS();
      // Hydrate from server preferences.
      fetch('/api/user-prefs')
        .then(r => r.ok ? r.json() : null)
        .then(prefs => {
          if (!prefs) return;
          if (!localStorage.getItem('bongo-theme')) {
            this.theme = prefs.theme || 'auto';
            this.applyTheme();
          }
          if (!localStorage.getItem('bongo-layout')) {
            this.layoutMode = prefs.layout_mode || 'list';
          }
          if (!localStorage.getItem('bongo-density')) {
            this.density = prefs.density || 'comfortable';
            this.applyDensity();
          }
        })
        .catch(() => {});
    },

    toggleTheme() {
      const cycle = { auto: 'light', light: 'dark', dark: 'auto' };
      this.theme = cycle[this.theme] || 'auto';
      localStorage.setItem('bongo-theme', this.theme);
      this.applyTheme();
      // Persist to server (fire-and-forget).
      const form = new FormData();
      form.set('theme', this.theme);
      fetch('/settings', { method: 'POST', body: new URLSearchParams(form) }).catch(() => {});
    },

    // setTheme applies an explicit choice (e.g. the Settings dropdown). It writes
    // localStorage so the choice actually takes effect: init() prefers
    // localStorage over the server preference, so a settings-only save would be
    // ignored on reload whenever localStorage already held a different value.
    setTheme(theme) {
      this.theme = theme;
      localStorage.setItem('bongo-theme', theme);
      this.applyTheme();
      const form = new FormData();
      form.set('theme', theme);
      fetch('/settings', { method: 'POST', body: new URLSearchParams(form) }).catch(() => {});
    },

    applyTheme() {
      document.documentElement.setAttribute('data-theme', this.resolvedTheme());
    },

    resolvedTheme() {
      if (this.theme === 'auto') {
        return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
      }
      return this.theme;
    },

    watchOS() {
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
        if (this.theme === 'auto') this.applyTheme();
      });
    },

    get themeIcon() {
      const t = this.resolvedTheme();
      return t === 'dark' ? '\u263E' : '\u2600'; // moon / sun
    },

    setLayout(mode) {
      this.layoutMode = mode;
      localStorage.setItem('bongo-layout', mode);
      const form = new URLSearchParams();
      form.set('layout_mode', mode);
      fetch('/settings', { method: 'POST', body: form }).catch(() => {});
    },

    applyDensity() {
      document.body.classList.remove('density-compact', 'density-comfortable', 'density-spacious');
      document.body.classList.add('density-' + this.density);
      document.documentElement.setAttribute('data-density', this.density);
    },

    setDensity(d) {
      this.density = d;
      localStorage.setItem('bongo-density', d);
      this.applyDensity();
      const form = new URLSearchParams();
      form.set('density', d);
      fetch('/settings', { method: 'POST', body: form }).catch(() => {});
    },

    toggleSidebar(event) {
      const opening = !this.sidebarOpen;
      this.sidebarOpen = opening;
      if (!opening) return;
      this.sidebarReturnFocus = event && event.currentTarget;
      this.$nextTick(() => {
        const first = document.querySelector('#sidebar a[href], #sidebar button:not([disabled]), #sidebar input:not([disabled])');
        if (first) first.focus();
      });
    },

    dismissSidebar() {
      this.sidebarOpen = false;
      this.sidebarReturnFocus = null;
    },

    closeSidebar() {
      if (!this.sidebarOpen) return;
      this.sidebarOpen = false;
      this.$nextTick(() => {
        if (this.sidebarReturnFocus && this.sidebarReturnFocus.isConnected) this.sidebarReturnFocus.focus();
        this.sidebarReturnFocus = null;
      });
    },

    trapSidebarFocus(event) {
      if (!this.sidebarOpen || event.key !== 'Tab' || !window.matchMedia('(max-width: 768px)').matches) return;
      const controls = Array.from(document.querySelectorAll('#sidebar a[href], #sidebar button:not([disabled]), #sidebar input:not([disabled])'))
        .filter((el) => el.offsetParent !== null);
      if (controls.length === 0) return;
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    },

    // Label for the search shortcut hint: the command key on Apple platforms,
    // Ctrl elsewhere.
    get searchHint() {
      const p = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || navigator.userAgent || '';
      return /mac|iphone|ipad|ipod/i.test(p) ? '\u2318K' : 'Ctrl K';
    }
  }));

  // Account menu (top bar). dismiss() closes without moving focus, for
  // outside clicks; close() returns focus to the button, for Escape.
  Alpine.data('accountMenu', () => ({
    open: false,
    toggle() {
      this.open = !this.open;
    },
    dismiss() {
      this.open = false;
    },
    close() {
      if (!this.open) return;
      this.open = false;
      this.$refs.button.focus();
    }
  }));

  Alpine.data('settingsTabs', (count) => ({
    activeTab: 0,
    select(index, moveFocus) {
      this.activeTab = (index + count) % count;
      if (!moveFocus) return;
      this.$nextTick(() => {
        const tabs = this.$root.querySelectorAll('[role="tab"]');
        if (tabs[this.activeTab]) {
          tabs[this.activeTab].focus();
          tabs[this.activeTab].scrollIntoView({ block: 'nearest', inline: 'nearest' });
        }
      });
    },
    onKeydown(event) {
      if (!event.target.matches('[role="tab"]')) return;
      if (event.key === 'ArrowRight') this.select(this.activeTab + 1, true);
      else if (event.key === 'ArrowLeft') this.select(this.activeTab - 1, true);
      else if (event.key === 'Home') this.select(0, true);
      else if (event.key === 'End') this.select(count - 1, true);
      else return;
      event.preventDefault();
    }
  }));

  // Add a device, Apple choice. The form posts and the response is a file
  // download, which leaves the page as it was, so on submit the form is
  // swapped for a panel saying what to do next. Hiding the form also stops a
  // second click creating a second credential. A refusal navigates away, so
  // the panel only outlives a success or a failed connection.
  Alpine.data('appleDevice', () => ({
    idle: true,
    done: false,
    submit(event) {
      // The QR button navigates to a page, not a download: leave the form and
      // any banner alone, or the panel would claim a download that never starts.
      if (event.submitter && event.submitter.hasAttribute('data-device-qr')) {
        return;
      }
      const form = event.target;
      // Hide now; clear the secrets after the browser has serialised the
      // form, which happens once this handler returns.
      this.idle = false;
      this.done = true;
      // A download leaves the page in place, so a banner from an earlier
      // attempt and the setup hint would contradict the panel. A refusal
      // navigates and renders them afresh.
      document.querySelectorAll('.flash, [data-apple-stale]').forEach((el) => el.remove());
      setTimeout(() => {
        form.querySelectorAll('input[type=password], input[name=totp_code]').forEach((el) => {
          el.value = '';
        });
      }, 0);
    }
  }));

  // Collapsible sidebar sections with localStorage persistence
  Alpine.data('sidebarSection', (key, defaultOpen) => ({
    open: localStorage.getItem('bongo-sidebar-' + key) === null
      ? defaultOpen !== false
      : localStorage.getItem('bongo-sidebar-' + key) !== 'closed',
    toggle() {
      this.open = !this.open;
      localStorage.setItem('bongo-sidebar-' + key, this.open ? 'open' : 'closed');
    }
  }));

  // GAL autocomplete for compose form
  Alpine.data('galAutocomplete', () => ({
    query: '',
    results: [],
    show: false,
    loading: false,
    selected: -1,
    // multi is set by data-gal-multi on the wrapper: the field then holds a
    // comma-separated list (the event form's attendees) and a search or a pick
    // concerns only the entry being typed, the last one. Every other field is
    // a single value and keeps the whole-field behaviour.
    multi: false,

    init() {
      this.multi = this.$el.hasAttribute('data-gal-multi');
      // Read the server-rendered value attribute to seed the query.
      // We use getAttribute('value') because it reads the HTML attribute
      // directly, not the DOM property which Alpine may have already cleared.
      // When the component wraps the input (the event form's attendees field,
      // which needs room for a suggestion list) the value is on that input.
      const src = this.$el.hasAttribute('value') ? this.$el : this.$el.querySelector('input[name]');
      const initial = src ? src.getAttribute('value') : null;
      if (initial) {
        this.query = initial;
      }
    },

    // onInput is the input handler. The Alpine CSP build cannot parse
    // "query = $el.value; search()", so the two steps live here.
    onInput(el) {
      this.query = el.value;
      this.search();
    },

    // lastSeparator is the index of the comma that ends the last complete
    // entry, or -1. A comma inside a quoted name ("Doe, Jane" <j@x>) does not
    // separate entries.
    lastSeparator() {
      let quoted = false;
      let last = -1;
      for (let i = 0; i < this.query.length; i++) {
        const c = this.query.charAt(i);
        if (c === '\\') {
          i++;
        } else if (c === '"') {
          quoted = !quoted;
        } else if (c === ',' && !quoted) {
          last = i;
        }
      }
      return last;
    },

    // searchTerm is what the GAL is asked about.
    searchTerm() {
      if (!this.multi) return this.query;
      return this.query.slice(this.lastSeparator() + 1).trim();
    },

    async search() {
      const term = this.searchTerm();
      if (term.length < 2) {
        this.results = [];
        this.show = false;
        return;
      }
      this.loading = true;
      try {
        const resp = await fetch('/api/gal?q=' + encodeURIComponent(term));
        if (resp.ok) {
          this.results = await resp.json();
          this.show = this.results.length > 0;
        }
      } catch (e) {
        console.error('GAL search failed:', e);
      }
      this.loading = false;
    },

    select(entry) {
      if (this.multi) {
        const cut = this.lastSeparator() + 1;
        this.query = (cut > 0 ? this.query.slice(0, cut) + ' ' : '') + entry.email;
      } else {
        this.query = entry.email;
      }
      this.show = false;
      this.results = [];
      // Setting the query fires no change event; tell the event form's
      // availability view once Alpine has written the input.
      this.$nextTick(() => this.$dispatch('availability-refresh'));
    },

    onKeydown(event) {
      if (!this.show) return;
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        this.selected = Math.min(this.selected + 1, this.results.length - 1);
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        this.selected = Math.max(this.selected - 1, 0);
      } else if (event.key === 'Enter' && this.selected >= 0) {
        event.preventDefault();
        this.select(this.results[this.selected]);
      } else if (event.key === 'Escape') {
        this.show = false;
      }
    }
  }));

  // Recipient pills for the compose To/Cc/Bcc rows. The pills are a read-only
  // view of the text input inside the wrapper: this component never writes the
  // input's value, so the posted field is exactly what the user typed (and
  // galAutocomplete's own :value binding is untouched). While the input is
  // blurred, non-empty and every address parses, the pills are shown in its
  // place; focusing the pills, or the input itself, shows the raw text again.
  Alpine.data('recipientPills', () => ({
    pills: [],
    showPills: false,
    input: null,

    init() {
      this.input = this.$el.querySelector('input[name]');
      if (!this.input) return;
      const self = this;
      this.input.addEventListener('focus', function() { self.showPills = false; });
      this.input.addEventListener('blur', function() { self.commit(); });
      this.$nextTick(function() { self.commit(); });
    },

    // Split a recipient list on commas that sit outside double quotes. Returns
    // {name, email} items, skipping empty tokens, or null when any token is
    // not an address. A comma inside <...> needs no tracking of its own: the
    // address pattern below rejects it, so such a list is null either way.
    parse(str) {
      const tokens = [];
      let cur = '';
      let inQuote = false;
      for (let i = 0; i < str.length; i++) {
        const c = str[i];
        if (inQuote && c === '\\' && i + 1 < str.length) {
          cur += c + str[i + 1];
          i++;
          continue;
        }
        if (c === '"') inQuote = !inQuote;
        if (c === ',' && !inQuote) {
          tokens.push(cur);
          cur = '';
          continue;
        }
        cur += c;
      }
      tokens.push(cur);

      const addr = /^[^\s@<>,"]+@[^\s@<>,"]+$/;
      const out = [];
      for (const raw of tokens) {
        const tok = raw.trim();
        if (tok === '') continue;
        const m = tok.match(/^(.*)<([^<>]*)>$/);
        let name = '';
        let email = tok;
        if (m) {
          name = m[1].trim();
          email = m[2].trim();
          if (name.length >= 2 && name[0] === '"' && name[name.length - 1] === '"') {
            name = name.slice(1, -1).replace(/\\(.)/g, '$1');
          }
        }
        if (!addr.test(email)) return null;
        out.push({ name: name, email: email, label: name || email });
      }
      return out;
    },

    // Mirrors avatarInitials in the Go templates, except that lengths and
    // slices count code points here, so a non-ASCII name is never cut inside
    // a character. The Go helper is being made rune-safe separately.
    initials(name, email) {
      if (name) {
        const parts = name.split(/\s+/).filter(Boolean).map(function(p) { return Array.from(p); });
        if (parts.length >= 2) {
          return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
        }
        if (parts.length === 1 && parts[0].length >= 2) {
          return parts[0].slice(0, 2).join('').toUpperCase();
        }
      }
      const e = Array.from(email);
      if (e.length >= 2) return e.slice(0, 2).join('').toUpperCase();
      return '??';
    },

    // Same hash as avatarClass in the Go templates (uint32 wrap, mod 7).
    colourClass(name, email) {
      let hash = 0;
      for (const ch of name + email) {
        hash = (hash * 31 + ch.codePointAt(0)) >>> 0;
      }
      return 'avatar-colour-' + (hash % 7);
    },

    commit() {
      const v = this.input ? this.input.value : '';
      const parsed = v.trim() === '' ? null : this.parse(v);
      if (!parsed || parsed.length === 0) {
        this.pills = [];
        this.showPills = false;
        return;
      }
      const self = this;
      this.pills = parsed.map(function(p) {
        return {
          label: p.label,
          initials: self.initials(p.name, p.email),
          colour: self.colourClass(p.name, p.email)
        };
      });
      this.showPills = true;
    },

    edit() {
      this.showPills = false;
      if (this.input) this.input.focus();
    }
  }));

  function messageRowsWithUIDs() {
    return Array.from(document.querySelectorAll('.msg-list .msg-row[data-uid]')).filter(function(row) {
      return row.dataset.uid && !Number.isNaN(parseInt(row.dataset.uid, 10));
    });
  }

  function setToolbarSelectAllChecked(checked) {
    document.querySelectorAll('.tool-check').forEach(function(input) {
      input.checked = checked;
    });
  }

  // Bulk message selection
  Alpine.data('messageSelect', () => ({
    selected: {},
    count: 0,
    lastClicked: null,

    _updateCount() {
      this.count = Object.keys(this.selected).length;
    },

    toggle(uid, event) {
      if (event && event.shiftKey && this.lastClicked !== null) {
        const rows = messageRowsWithUIDs();
        const uids = rows.map(r => parseInt(r.dataset.uid, 10));
        const start = uids.indexOf(this.lastClicked);
        const end = uids.indexOf(uid);
        if (start !== -1 && end !== -1) {
          const [lo, hi] = start < end ? [start, end] : [end, start];
          for (let i = lo; i <= hi; i++) {
            this.selected[uids[i]] = true;
          }
        }
      } else {
        if (this.selected[uid]) {
          delete this.selected[uid];
        } else {
          this.selected[uid] = true;
        }
      }
      this.lastClicked = uid;
      this._updateCount();
    },

    selectAll() {
      const rows = messageRowsWithUIDs();
      rows.forEach(r => { this.selected[parseInt(r.dataset.uid, 10)] = true; });
      this._updateCount();
      setToolbarSelectAllChecked(rows.length > 0);
    },

    deselectAll() {
      this.selected = {};
      this.count = 0;
      setToolbarSelectAllChecked(false);
    },

    isSelected(uid) {
      return !!this.selected[uid];
    },

    async bulkAction(mailbox, action) {
      if (this.count === 0) return;
      const params = new URLSearchParams();
      params.set('action', action);
      for (const uid of Object.keys(this.selected)) {
        params.append('uids', uid);
      }
      try {
        const resp = await fetch(bulkURL(mailbox), {
          method: 'POST',
          body: params
        });
        if (resp.ok) {
          this.selected = {};
          this.count = 0;
          this.lastClicked = null;
          setToolbarSelectAllChecked(false);
          // Refresh message list via HTMX.
          htmx.ajax('GET', listReloadURL(mailbox), '#main-content');
        }
      } catch (e) {
        console.error('Bulk action failed:', e);
      }
    }
  }));

  // Combined component for message list: row selection and mobile swipe.
  // Used as x-data="messageList" (Alpine.data registered names cannot be
  // called as functions inside inline x-data expressions).
  Alpine.data('messageList', () => ({
    // messageSelect state
    selected: {},
    count: 0,
    lastClicked: null,

    // Bulk "Tag..." dropdown. The CSP build cannot parse statement lists, so
    // the template calls one method per event instead of "a(); b = c".
    bulkTagOpen: false,
    bulkTagInput: '',
    bulkTagTrigger: null,

    _updateCount() {
      this.count = Object.keys(this.selected).length;
    },

    // Translated "N selected" text; the format arrives as data-selected-format.
    selectedLabel() {
      const format = this.$root.dataset.selectedFormat || '%d';
      return format.replace('%d', this.count);
    },

    toggle(uid, event) {
      const row = event && event.target && event.target.closest ? event.target.closest('.msg-row') : null;
      const key = row ? selectionKey(row) : String(uid);
      if (event && event.shiftKey && this.lastClicked !== null) {
        const keys = messageRowsWithUIDs().map(selectionKey);
        const start = keys.indexOf(this.lastClicked);
        const end = keys.indexOf(key);
        if (start !== -1 && end !== -1) {
          const [lo, hi] = start < end ? [start, end] : [end, start];
          for (let i = lo; i <= hi; i++) {
            this.selected[keys[i]] = true;
          }
        }
      } else {
        if (this.selected[key]) {
          delete this.selected[key];
        } else {
          this.selected[key] = true;
        }
      }
      this.lastClicked = key;
      this._updateCount();
    },

    selectAll() {
      const rows = messageRowsWithUIDs();
      rows.forEach(r => { this.selected[selectionKey(r)] = true; });
      this._updateCount();
      setToolbarSelectAllChecked(rows.length > 0);
    },

    deselectAll() {
      this.selected = {};
      this.count = 0;
      setToolbarSelectAllChecked(false);
    },

    isSelected(uid, el) {
      const row = el && el.closest ? el.closest('.msg-row') : null;
      return !!this.selected[row ? selectionKey(row) : String(uid)];
    },

    async bulkAction(mailbox, action) {
      if (this.count === 0) return;
      const params = new URLSearchParams();
      params.set('action', action);
      for (const key of Object.keys(this.selected)) {
        addSelectionToParams(params, key);
      }
      try {
        const resp = await fetch(bulkURL(mailbox), {
          method: 'POST',
          body: params
        });
        if (resp.ok) {
          this.selected = {};
          this.count = 0;
          this.lastClicked = null;
          setToolbarSelectAllChecked(false);
          htmx.ajax('GET', listReloadURL(mailbox), '#main-content');
        }
      } catch (e) {
        console.error('Bulk action failed:', e);
      }
    },

    toggleBulkTag(event) {
      this.bulkTagTrigger = event && event.currentTarget;
      this.bulkTagOpen = !this.bulkTagOpen;
    },

    closeBulkTag() {
      this.bulkTagOpen = false;
      if (this.bulkTagTrigger) this.bulkTagTrigger.focus();
    },

    dismissBulkTag() {
      this.bulkTagOpen = false;
    },

    bulkTagEmpty() {
      return this.bulkTagInput.trim() === '';
    },

    bulkTagSubmit(mailbox, action) {
      const name = this.bulkTagInput;
      this.bulkTagOpen = false;
      this.bulkTagInput = '';
      return this.bulkTagAction(mailbox, action, name);
    },

    async bulkTagAction(mailbox, action, tagName) {
      tagName = (tagName || '').trim();
      if (!tagName || this.count === 0) return;
      const messageIds = Object.keys(this.selected).map(key => selectionMessageId(key, mailbox));
      try {
        const resp = await fetch('/mail/bulk/tags', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: action, tags: [tagName], message_ids: messageIds })
        });
        if (resp.ok) {
          this.selected = {};
          this.count = 0;
          this.lastClicked = null;
          setToolbarSelectAllChecked(false);
          htmx.ajax('GET', listReloadURL(mailbox), '#main-content');
        } else {
          console.error('Bulk tag action failed:', resp.status);
        }
      } catch (e) {
        console.error('Bulk tag action error:', e);
      }
    },

    // Swipe state (touch devices only)
    startX: 0,
    startY: 0,
    currentX: 0,
    swiping: false,
    swipeTarget: null,

    start(event, uid) {
      if (!window.matchMedia('(pointer: coarse)').matches) return;
      var touch = event.touches[0];
      this.startX = touch.clientX;
      this.startY = touch.clientY;
      this.currentX = 0;
      this.swiping = false;
      this.swipeTarget = event.currentTarget;
    },

    move(event) {
      if (!this.swipeTarget) return;
      var touch = event.touches[0];
      var deltaX = touch.clientX - this.startX;
      var deltaY = touch.clientY - this.startY;
      if (Math.abs(deltaX) > 20 && Math.abs(deltaY) < 30) {
        this.swiping = true;
        this.currentX = deltaX;
        setSwipeDragState(this.swipeTarget, deltaX);
      }
    },

    end(event, uid, mailbox) {
      if (!this.swipeTarget || !this.swiping) {
        this._resetSwipe();
        return;
      }
      var deltaX = this.currentX;
      if (deltaX < -60) {
        // Swipe left: archive (move to Archive); in Archive, unarchive (move
        // to INBOX). The list reloads only when the move succeeded.
        // The verb and the URL come from the message's real mailbox: in a
        // virtual view (Starred, tags) the list mailbox says nothing about
        // whether it is archived, and UIDs are only unique per mailbox.
        var row = this.swipeTarget;
        var real = (row && row.dataset.realMailbox) || mailbox;
        finalizeSwipeState(row, 'left');
        var verb = real === 'Archive' ? 'unarchive' : 'archive';
        fetch('/mail/' + encodeURIComponent(real) + '/' + uid + '/' + verb, {
          method: 'POST'
        }).then(function(resp) {
          if (!resp.ok) {
            resetSwipeState(row);
            return;
          }
          htmx.ajax('GET', listReloadURL(mailbox), '#main-content');
        }).catch(function() {
          resetSwipeState(row);
        });
      } else if (deltaX > 60) {
        var row2 = this.swipeTarget;
        finalizeSwipeState(row2, 'right');
        var params2 = new URLSearchParams();
        params2.set('action', 'delete');
        params2.append('uids', uid.toString());
        fetch('/mail/' + encodeURIComponent(actionMailbox(row2, mailbox)) + '/bulk', {
          method: 'POST', body: params2
        }).then(function() {
          htmx.ajax('GET', listReloadURL(mailbox), '#main-content');
        }).catch(function() {});
      } else {
        this._resetSwipe();
      }
    },

    _resetSwipe() {
      if (this.swipeTarget) {
        resetSwipeState(this.swipeTarget);
      }
      this.swipeTarget = null;
      this.swiping = false;
      this.currentX = 0;
    },

    openContextMenu(event, uid, mailbox) {
      event.preventDefault();
      const row = event.target && event.target.closest ? event.target.closest('.msg-row') : null;
      this.$dispatch('ctx-menu', { x: event.clientX, y: event.clientY, uid: uid, mailbox: mailbox, real: actionMailbox(row, mailbox), trigger: row });
    }
  }));

  // Attachment drop-zone for the compose form.
  // The component sits on the compose card so a file can be dropped anywhere
  // on it; the file input itself is visually hidden.
  Alpine.data('dropZone', () => ({
    files: [],
    totalSize: 0,
    dragging: false,

    hasFiles(event) {
      const types = event.dataTransfer ? Array.from(event.dataTransfer.types || []) : [];
      return types.indexOf('Files') !== -1;
    },

    dragOver(event) {
      if (!this.hasFiles(event)) return;
      event.preventDefault();
      this.dragging = true;
    },

    dragLeave(event) {
      if (event.relatedTarget && this.$root.contains(event.relatedTarget)) return;
      this.dragging = false;
    },

    // Adds the dropped files to the ones already chosen and fires the input's
    // change event, so updateFiles() and the form post see them as usual.
    drop(event) {
      this.dragging = false;
      if (!this.hasFiles(event)) return;
      event.preventDefault();
      const input = this.$root.querySelector('input[type="file"][name="attachments"]');
      if (!input) return;
      const dt = new DataTransfer();
      Array.from(input.files).forEach(function(f) { dt.items.add(f); });
      Array.from(event.dataTransfer.files).forEach(function(f) { dt.items.add(f); });
      input.files = dt.files;
      input.dispatchEvent(new Event('change', { bubbles: true }));
    },

    formatBytes(b) {
      if (b < 1024) return b + ' B';
      if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
      return (b / 1048576).toFixed(1) + ' MB';
    },

    // "N file(s), total: SIZE" from the translated template on the root.
    summary() {
      const tpl = this.$root.dataset.filesSummary || '{n} / {size}';
      return tpl.replace('{n}', this.files.length).replace('{size}', this.formatBytes(this.totalSize));
    },

    updateFiles(el) {
      this.files = Array.from(el.files);
      this.totalSize = this.files.reduce(function(s, f) { return s + f.size; }, 0);
    }
  }));

  // Desktop notifications + SSE
  Alpine.data('notifications', () => ({
    permission: typeof Notification !== 'undefined' ? Notification.permission : 'denied',
    promptDismissed: localStorage.getItem('bongo-notification-prompt') === 'dismissed',
    eventSource: null,
    streamMailbox: '',
    navigationHandler: null,
    reconnectTimer: null,
    resyncOnOpen: false,

    get showPrompt() {
      return this.permission === 'default' && !this.promptDismissed;
    },

    init() {
      this.connectSSE();
      this.navigationHandler = () => this.reconnectForMailbox();
      document.addEventListener('htmx:afterSettle', this.navigationHandler);
    },

    requestPermission() {
      if (typeof Notification === 'undefined') return;
      Notification.requestPermission().then(p => {
        this.permission = p;
        this.promptDismissed = true;
        localStorage.setItem('bongo-notification-prompt', 'dismissed');
      });
    },

    dismissPrompt() {
      this.promptDismissed = true;
      localStorage.setItem('bongo-notification-prompt', 'dismissed');
    },

    currentStreamMailbox() {
      return currentMailbox() || 'INBOX';
    },

    reconnectForMailbox() {
      const mailbox = this.currentStreamMailbox();
      if (this.eventSource && mailbox === this.streamMailbox) return;
      if (this.eventSource) this.eventSource.close();
      this.eventSource = null;
      this.streamMailbox = '';
      clearTimeout(this.reconnectTimer);
      this.connectSSE();
    },

    connectSSE() {
      if (this.eventSource) return;
      this.streamMailbox = this.currentStreamMailbox();
      this.eventSource = new EventSource('/events?mailbox=' + encodeURIComponent(this.streamMailbox));

      this.eventSource.onopen = () => {
        if (!this.resyncOnOpen) return;
        this.resyncOnOpen = false;
        this.resyncVisibleMailbox();
      };

      this.eventSource.addEventListener('newmail', (e) => {
        if (window.refreshUnreadCounts) window.refreshUnreadCounts();
        try {
          const data = JSON.parse(e.data);
          if (this.permission === 'granted') {
            const n = new Notification(data.from || i18n('js.newMail'), {
              body: data.subject || '',
              icon: '/static/img/bongo-favicon.png'
            });
            n.onclick = () => { window.focus(); n.close(); };
          }
        } catch (err) { /* ignore parse errors */ }
      });

      this.eventSource.addEventListener('oob', (e) => {
        // HTMX OOB swap for unread badge.
        const tmp = document.createElement('div');
        tmp.innerHTML = e.data;
        htmx.process(tmp);
        const oobEl = tmp.firstElementChild;
        if (oobEl) {
          const target = document.getElementById(oobEl.id);
          if (target) target.outerHTML = oobEl.outerHTML;
        }
      });

      this.eventSource.addEventListener('mailboxchange', (e) => {
        try {
          this.applyMailboxChange(JSON.parse(e.data)).catch(() => showClientStatus('', 'error'));
        } catch (err) { /* ignore malformed stream data */ }
      });

      this.eventSource.addEventListener('mailboxcounts', () => {
        if (window.refreshUnreadCounts) window.refreshUnreadCounts();
      });

      this.eventSource.addEventListener('resync', (e) => {
        try {
          const data = JSON.parse(e.data);
          if (data.mailbox === currentMailbox()) this.resyncVisibleMailbox();
        } catch (err) { /* ignore malformed stream data */ }
      });

      this.eventSource.addEventListener('reminder', (e) => {
        try {
          const data = JSON.parse(e.data);
          if (this.permission === 'granted') {
            const n = new Notification(i18n('js.eventReminderPrefix') + data.minutes_before + i18n('js.eventReminderSuffix'), {
              body: data.title || i18n('js.upcomingEvent'),
              icon: '/static/img/bongo-favicon.png'
            });
            n.onclick = () => { window.focus(); n.close(); };
          }
        } catch (err) { /* ignore parse errors */ }
      });

      this.eventSource.onerror = () => {
        // Reconnect after 5 seconds.
        this.eventSource.close();
        this.eventSource = null;
        this.resyncOnOpen = true;
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = setTimeout(() => this.connectSSE(), 5000);
      };
    },

    resyncVisibleMailbox() {
      const mailbox = currentMailbox();
      if (!mailbox) return;
      const list = document.querySelector('.msg-list');
      const focused = list && list.querySelector('.msg-row.keyboard-focus');
      const focusedUID = focused && focused.dataset.uid;
      const scrollTop = list ? list.scrollTop : 0;
      htmx.ajax('GET', listReloadURL(mailbox), '#main-content').then(function() {
        const nextList = document.querySelector('.msg-list');
        if (nextList) nextList.scrollTop = scrollTop;
        if (!focusedUID) return;
        const row = document.getElementById('msg-' + focusedUID);
        if (row) row.classList.add('keyboard-focus');
      });
    },

    async applyMailboxChange(data) {
      const main = document.getElementById('main-content');
      if (!main || currentMailbox() !== data.mailbox) return;
      if (window.refreshUnreadCounts) window.refreshUnreadCounts();

      const oldRow = document.getElementById('msg-' + data.uid);
      if (data.action === 'remove') {
        if (!oldRow) return;
        const next = oldRow.nextElementSibling || oldRow.previousElementSibling;
        const hadFocus = oldRow.classList.contains('keyboard-focus');
        oldRow.remove();
        if (hadFocus && next && next.matches('.msg-row[data-uid]')) moveKeyboardFocus(next);
        return;
      }
      if (data.action !== 'upsert') return;

      const response = await fetch('/api/mail/' + encodeURIComponent(data.mailbox) + '/' + data.uid + '/row');
      if (!response.ok) return;
      const holder = document.createElement('div');
      holder.innerHTML = await response.text();
      const newRow = holder.firstElementChild;
      if (!newRow) return;

      if (oldRow) {
        if (oldRow.classList.contains('keyboard-focus')) newRow.classList.add('keyboard-focus');
        oldRow.replaceWith(newRow);
        htmx.process(newRow);
        return;
      }
      const list = main.querySelector('.msg-list');
      if (!list) {
        htmx.ajax('GET', listReloadURL(data.mailbox), '#main-content');
        return;
      }
      const before = list.scrollHeight;
      list.prepend(newRow);
      htmx.process(newRow);
      if (list.scrollTop > 0) list.scrollTop += list.scrollHeight - before;
    },

    destroy() {
      document.removeEventListener('htmx:afterSettle', this.navigationHandler);
      clearTimeout(this.reconnectTimer);
      if (this.eventSource) {
        this.eventSource.close();
        this.eventSource = null;
      }
    }
  }));

  // Spam scan progress component: starts a background scan and streams progress via SSE.
  Alpine.data('spamScan', (mailbox) => ({
    scanning: false,
    progress: { scanned: 0, spam: 0, total: 0, done: false },
    result: null,
    evtSource: null,

    async startScan() {
      if (this.scanning) return;
      this.scanning = true;
      this.result = null;
      this.progress = { scanned: 0, spam: 0, total: 0, done: false };

      try {
        const resp = await fetch('/api/mailbox/' + encodeURIComponent(mailbox) + '/scan', {
          method: 'POST'
        });
        if (resp.status === 409) {
          // Scan already running — just connect to the progress stream.
        } else if (!resp.ok) {
          this.scanning = false;
          this.result = i18n('js.scanFailedPrefix') + resp.status;
          return;
        }
      } catch (e) {
        this.scanning = false;
        this.result = i18n('js.scanErrorPrefix') + e.message;
        return;
      }

      this.evtSource = new EventSource('/api/mailbox/' + encodeURIComponent(mailbox) + '/scan/progress');
      this.evtSource.onmessage = (e) => {
        try {
          const p = JSON.parse(e.data);
          this.progress = p;
          if (p.done) {
            this.scanning = false;
            this.result = p.scanned + i18n('js.scannedInfix') + p.spam + i18n('js.movedToJunkSuffix');
            this.evtSource.close();
            this.evtSource = null;
          }
        } catch (err) { /* ignore */ }
      };
      this.evtSource.onerror = () => {
        this.scanning = false;
        if (this.evtSource) {
          this.evtSource.close();
          this.evtSource = null;
        }
      };
    },

    cancelScan() {
      if (!this.scanning) return;
      fetch('/api/mailbox/' + encodeURIComponent(mailbox) + '/scan', { method: 'DELETE' }).catch(() => {});
      if (this.evtSource) {
        this.evtSource.close();
        this.evtSource = null;
      }
      this.scanning = false;
      this.result = i18n('js.scanCancelled');
    },

    destroy() {
      if (this.evtSource) {
        this.evtSource.close();
        this.evtSource = null;
      }
    }
  }));

  // Context menu for message rows
  Alpine.data('contextMenu', () => ({
    visible: false,
    x: 0,
    y: 0,
    uid: null,
    mailbox: null,
    real: null,
    focusedItem: -1,
    tagOpen: false,
    tagInput: '',
    returnFocus: null,

    toggleTag() {
      this.tagOpen = !this.tagOpen;
    },

    tagEmpty() {
      return this.tagInput.trim() === '';
    },

    // op is "add" or "remove"; one method because the CSP build cannot parse
    // "action(...); tagInput = ''" in the template.
    tagSubmit(op) {
      const name = this.tagInput;
      this.tagInput = '';
      this.tagOpen = false;
      this.action('tag-' + op + ':' + name);
    },

    openAt(x, y, uid, mailbox, real, trigger) {
      this.uid = uid;
      this.mailbox = mailbox;
      // The mailbox the message really lives in. It differs from the list
      // mailbox only in the To me, From my contacts and Mailing lists views.
      this.real = real || mailbox;
      this.returnFocus = trigger || null;
      // Viewport clamping: keep menu within the visible window.
      const menuW = 200, menuH = 320;
      if (x + menuW > window.innerWidth) x = window.innerWidth - menuW - 8;
      if (y + menuH > window.innerHeight) y = window.innerHeight - menuH - 8;
      if (x < 0) x = 8;
      if (y < 0) y = 8;
      this.x = x;
      this.y = y;
      replacePrefixedClass(this.$el, 'ctx-left-', contextPositionClass('ctx-left-', x));
      replacePrefixedClass(this.$el, 'ctx-top-', contextPositionClass('ctx-top-', y));
      this.visible = true;
      this.focusedItem = -1;
      // Focus first item after render.
      this.$nextTick(() => {
        const items = this.$el.querySelectorAll('[role="menuitem"]');
        if (items.length > 0) {
          this.focusedItem = 0;
          items[0].focus();
        }
      });
    },

    close() {
      this.visible = false;
      this.focusedItem = -1;
      if (this.returnFocus && this.returnFocus.isConnected) this.returnFocus.focus();
      this.returnFocus = null;
    },

    dismiss() {
      this.visible = false;
      this.focusedItem = -1;
      this.returnFocus = null;
    },

    onKeydown(event) {
      const items = Array.from(this.$el.querySelectorAll('[role="menuitem"]'));
      if (items.length === 0) return;

      if (event.key === 'ArrowDown') {
        event.preventDefault();
        this.focusedItem = Math.min(this.focusedItem + 1, items.length - 1);
        items[this.focusedItem].focus();
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        this.focusedItem = Math.max(this.focusedItem - 1, 0);
        items[this.focusedItem].focus();
      } else if (event.key === 'Home') {
        event.preventDefault();
        this.focusedItem = 0;
        items[0].focus();
      } else if (event.key === 'End') {
        event.preventDefault();
        this.focusedItem = items.length - 1;
        items[this.focusedItem].focus();
      } else if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        if (this.focusedItem >= 0 && this.focusedItem < items.length) {
          items[this.focusedItem].click();
        }
      }
    },

    action(act) {
      const uid = this.uid;
      const view = this.mailbox;
      const mailbox = this.real || view;
      this.dismiss();
      if (!uid || !mailbox) return;

      switch (act) {
        case 'open':
          window.location.href = '/mail/' + encodeURIComponent(mailbox) + '/' + uid;
          break;
        case 'create-filter':
          window.location.href = '/settings/rules/new?mailbox=' + encodeURIComponent(mailbox) + '&uid=' + encodeURIComponent(uid);
          break;
        case 'reply':
          window.location.href = '/compose/reply/' + encodeURIComponent(mailbox) + '/' + uid;
          break;
        case 'reply-all':
          window.location.href = '/compose/reply/' + encodeURIComponent(mailbox) + '/' + uid;
          break;
        case 'forward':
          window.location.href = '/compose/forward/' + encodeURIComponent(mailbox) + '/' + uid;
          break;
        case 'spam':
          fetch('/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/spam', {
            method: 'POST'
          }).then(resp => {
            if (resp.ok) {
              htmx.ajax('GET', listReloadURL(view), '#main-content');
            }
          }).catch(() => {});
          break;
        case 'not-spam':
          fetch('/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/not-spam', {
            method: 'POST'
          }).then(resp => {
            if (resp.ok) {
              htmx.ajax('GET', listReloadURL(view), '#main-content');
            }
          }).catch(() => {});
          break;
        case 'mark-read':
        case 'mark-unread':
        case 'flag':
        case 'unflag':
        case 'delete': {
          const params = new URLSearchParams();
          params.set('action', act);
          params.append('uids', uid.toString());
          fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk', {
            method: 'POST',
            body: params
          }).then(resp => {
            if (resp.ok) {
              htmx.ajax('GET', listReloadURL(view), '#main-content');
            }
          }).catch(() => {});
          break;
        }
        default: {
          if (act.startsWith('tag-add:') || act.startsWith('tag-remove:')) {
            const colonIdx = act.indexOf(':');
            const tagAction = act.startsWith('tag-add:') ? 'add' : 'remove';
            const tagName = act.slice(colonIdx + 1).trim();
            if (tagName) {
              fetch('/mail/bulk/tags', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ action: tagAction, tags: [tagName], message_ids: [{ mailbox: mailbox, uid: uid }] })
              }).then(resp => {
                if (resp.ok) {
                  htmx.ajax('GET', listReloadURL(view), '#main-content');
                }
              }).catch(() => {});
            }
          }
          break;
        }
      }
    }
  }));

  // Contact editor rows (emails, phones, addresses), seeded from JSON in
  // data-emails, data-phones and data-addresses. Fields write through setRow:
  // the CSP build forbids property assignments, so x-model="em.value" never
  // updated the model, and adding or removing a row could lose typed values.
  Alpine.data('contactEditor', () => ({
    emails: [],
    phones: [],
    addresses: [],

    init() {
      const read = (key) => {
        try { return JSON.parse(this.$el.dataset[key] || '[]'); } catch (e) { return []; }
      };
      this.emails = read('emails');
      this.phones = read('phones');
      this.addresses = read('addresses');
    },

    blank(list) {
      if (list === 'addresses') return { type: '', street: '', city: '', region: '', postal: '', country: '' };
      return { value: '', type: '' };
    },

    addRow(list) {
      this[list].push(this.blank(list));
    },

    removeRow(list, i) {
      this[list].splice(i, 1);
    },

    setRow(list, i, field, value) {
      const row = this[list][i];
      if (row) row[field] = value;
    }
  }));

  // Event form state: the all-day toggle and, on a form opened from one
  // occurrence of a series, which instances a save or delete applies to. An
  // override of a single instance carries no repeat rule, so the repeat
  // field is hidden for "this event". The CSP build cannot evaluate an inline
  // comparison, hence the getter.
  // The Show as select starts on the default for the all-day state and
  // follows the checkbox until the user picks a value themselves.
  Alpine.data('eventForm', (allDay, scope, showAs, showAsTouched) => ({
    allDay: allDay,
    scope: scope,
    showAs: showAs,
    showAsTouched: showAsTouched,

    allDayChanged(event) {
      if (!this.showAsTouched) {
        this.showAs = event.target.checked ? 'free' : 'busy';
      }
    },

    showAsChanged() {
      this.showAsTouched = true;
    },

    get repeatHidden() {
      return this.scope === 'this';
    }
  }));

  // Calendar sidebar show/hide filter. Events carry data-cal-id; the choice
  // is re-applied after every HTMX swap of the calendar view, since
  // navigating replaces only the main pane, and remembered per browser.
  Alpine.data('calendarFilter', () => ({
    hidden: {},

    init() {
      try { this.hidden = JSON.parse(localStorage.getItem('bongo.hiddenCalendars') || '{}') || {}; } catch (e) { this.hidden = {}; }
      this.apply();
    },

    isShown(id) {
      return !this.hidden[id];
    },

    toggle(id, shown) {
      if (shown) {
        delete this.hidden[id];
      } else {
        this.hidden[id] = true;
      }
      try { localStorage.setItem('bongo.hiddenCalendars', JSON.stringify(this.hidden)); } catch (e) { /* private window: filter lasts for this page only */ }
      this.apply();
    },

    apply() {
      document.querySelectorAll('[data-cal-id]').forEach(el => {
        el.toggleAttribute('hidden', !!this.hidden[el.dataset.calId]);
      });
    }
  }));

  // Rule editor conditions. The initial list and the translated operator
  // labels arrive as JSON in data-conditions and data-op-labels; the CSP
  // build cannot evaluate an inline object with methods.
  Alpine.data('ruleConditions', () => ({
    conditions: [],
    opLabels: {},

    init() {
      try { this.conditions = JSON.parse(this.$el.dataset.conditions || '[]'); } catch (e) { this.conditions = []; }
      try { this.opLabels = JSON.parse(this.$el.dataset.opLabels || '{}'); } catch (e) { this.opLabels = {}; }
      if (this.conditions.length === 0) this.addCondition();
    },

    operatorsFor(cond) {
      if (cond.field === 'size') return ['greater_than', 'less_than'];
      if (cond.field === 'has_attachment') return ['equals'];
      if (cond.field === 'mime_type') return ['equals', 'contains', 'matches'];
      return ['contains', 'not_contains', 'equals', 'matches'];
    },

    isHeader(cond) {
      return cond.field === 'header';
    },

    // The one cond_field value a row posts: a header row posts header:Name, so
    // the field, operator and value lists stay the same length.
    postedField(cond) {
      return cond.field === 'header' ? 'header:' + (cond.header || '') : cond.field;
    },

    operatorLabel(op) {
      return this.opLabels[op] || op;
    },

    isAttachment(cond) {
      return cond.field === 'has_attachment';
    },

    // Row fields are written through these setters: the CSP build forbids
    // property assignments, so x-model="cond.field" cannot write back.
    setField(i, field) {
      const cond = this.conditions[i];
      if (!cond) return;
      cond.field = field;
      this.updateOperators(cond);
    },

    setHeader(i, name) {
      if (this.conditions[i]) this.conditions[i].header = name;
    },

    setOperator(i, op) {
      if (this.conditions[i]) this.conditions[i].operator = op;
    },

    setValue(i, value) {
      if (this.conditions[i]) this.conditions[i].value = value;
    },

    // Keep the operator (and value) valid for the newly chosen field.
    updateOperators(cond) {
      if (cond.field === 'size') {
        if (cond.operator !== 'less_than') cond.operator = 'greater_than';
      } else if (cond.field === 'has_attachment') {
        cond.operator = 'equals';
        if (cond.value !== 'false') cond.value = 'true';
      } else if (!this.operatorsFor(cond).includes(cond.operator)) {
        cond.operator = 'contains';
      }
    },

    addCondition() {
      this.conditions.push({ field: 'from', operator: 'contains', value: '' });
    },

    removeCondition(i) {
      if (this.conditions.length > 1) this.conditions.splice(i, 1);
    },

    canRemove() {
      return this.conditions.length > 1;
    }
  }));

  // Rule editor actions: one to ten rows, each with a type, a folder, a
  // forward address and (for the tag action) a tag list. The initial rows
  // arrive as JSON in data-actions. Every row posts action_type, action_target,
  // action_target_forward and action_tags exactly once, hidden or not, so the
  // server reads the four arrays by position; the tags travel comma-joined in
  // one hidden input because their count differs per row. The CSP build forbids
  // property assignment in expressions, so rows are written through setters.
  Alpine.data('ruleActions', () => ({
    actions: [],
    maxActions: 10,
    defaultFolder: '',

    init() {
      this.defaultFolder = this.$el.dataset.defaultFolder || '';
      try { this.actions = JSON.parse(this.$el.dataset.actions || '[]'); } catch (e) { this.actions = []; }
      if (this.actions.length === 0) this.addAction();
      // A select bound to an empty value has no selection and would post
      // nothing, which shifts the positional arrays; give every row a folder.
      for (const action of this.actions) {
        if (!action.target) action.target = this.defaultFolder;
      }
    },

    isForward(action) {
      return action.type === 'forward';
    },

    isTag(action) {
      return action.type === 'tag';
    },

    needsFolder(action) {
      return action.type === 'move' || action.type === 'copy';
    },

    setType(i, type) {
      const action = this.actions[i];
      if (!action) return;
      action.type = type;
      // A forward address left behind would block submission through the
      // hidden type=email field.
      if (type !== 'forward') action.forward = '';
    },

    setTarget(i, value) {
      if (this.actions[i]) this.actions[i].target = value;
    },

    setForward(i, value) {
      if (this.actions[i]) this.actions[i].forward = value;
    },

    setTagInput(i, value) {
      if (this.actions[i]) this.actions[i].tagInput = value;
    },

    addTag(i) {
      const action = this.actions[i];
      if (!action) return;
      const v = (action.tagInput || '').trim().replace(/,$/, '');
      if (v && !action.tags.includes(v)) action.tags.push(v);
      action.tagInput = '';
    },

    removeTag(i, idx) {
      if (this.actions[i]) this.actions[i].tags.splice(idx, 1);
    },

    // Tag names are folded to letters, numbers and hyphens on the server, so a
    // comma cannot be part of one.
    tagsValue(action) {
      return action.type === 'tag' ? action.tags.join(',') : '';
    },

    addAction() {
      if (this.actions.length >= this.maxActions) return;
      this.actions.push({ type: 'move', target: this.defaultFolder, forward: '', tags: [], tagInput: '' });
    },

    removeAction(i) {
      if (this.actions.length > 1) this.actions.splice(i, 1);
    },

    canAdd() {
      return this.actions.length < this.maxActions;
    },

    canRemove() {
      return this.actions.length > 1;
    }
  }));

  // Message detail tag chips: add/remove tags on a single message.
  // Initialised via x-data="messageTags(tags, allTags, mailbox, uid)".
  Alpine.data('messageTags', (initialTags, allTags, mailbox, uid) => ({
    tags: initialTags,          // [{name, colourIndex}, ...]  current tags on this message
    allTags: allTags,           // [{name, colourIndex}, ...]  all user-defined tags
    openDropdown: false,
    newTagName: '',
    tagError: '',
    viewTagLabel: '',
    removeTagLabel: '',
    tagErrorLabel: '',

    messageTagBGClass(index) {
      return 'message-tag-bg-' + (Number.isInteger(index) ? index : 0);
    },

    messageTagColourClass(index) {
      return 'message-tag-colour-' + (Number.isInteger(index) ? index : 0);
    },

    // The Alpine CSP build cannot parse template literals, so chip links and
    // labels are built here. The label templates ("View tag %s") are translated
    // server-side and carried on the root element.
    tagHref(tag) {
      return '/mail/tag/' + encodeURIComponent(tag.name);
    },

    viewLabel(tag) {
      return (this.viewTagLabel || '%s').replace('%s', tag.name);
    },

    removeLabel(tag) {
      return (this.removeTagLabel || '%s').replace('%s', tag.name);
    },

    get filteredAllTags() {
      const q = this.newTagName.trim().toLowerCase();
      if (!q) return this.allTags;
      return this.allTags.filter(t => t.name.toLowerCase().includes(q));
    },

    // foldedNewTagName previews the name the server will store, mirroring
    // tagname.Normalise: "My Tag" becomes "my-tag", punctuation is dropped.
    get foldedNewTagName() {
      return this.newTagName.trim().toLowerCase()
        .replace(/[ _]/g, '-')
        .replace(/[^a-z0-9-]/g, '')
        .replace(/-+/g, '-')
        .replace(/^-|-$/g, '');
    },

    get exactMatch() {
      const q = this.foldedNewTagName;
      return this.allTags.some(t => t.name === q) ||
             this.tags.some(t => t.name === q);
    },

    isTagged(name) {
      return this.tags.some(t => t.name === name);
    },

    async addTag(name) {
      if (this.isTagged(name)) { this.closeDropdown(); return; }
      const existing = this.allTags.find(t => t.name === name);
      const colourIndex = existing ? existing.colourIndex : 0;
      const resp = await fetch(
        '/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/tags',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: 'add', tags: [name] })
        }
      ).catch(() => null);
      if (resp && resp.ok) {
        const data = await resp.json().catch(() => null);
        if (data && Array.isArray(data.tags)) {
          this.tags = data.tags.map(n => {
            const t = this.tags.find(x => x.name === n) || this.allTags.find(x => x.name === n);
            return { name: n, colourIndex: t ? t.colourIndex : colourIndex };
          });
          data.tags.forEach(n => {
            if (!this.allTags.some(t => t.name === n)) {
              this.allTags.push({ name: n, colourIndex });
            }
          });
        }
        this.closeDropdown();
        return;
      }
      // Keep the dropdown open so the rejection is visible.
      this.tagError = this.tagErrorLabel;
    },

    async removeTag(name) {
      const resp = await fetch(
        '/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/tags',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: 'remove', tags: [name] })
        }
      ).catch(() => null);
      if (resp && resp.ok) {
        const data = await resp.json().catch(() => null);
        if (data && Array.isArray(data.tags)) {
          this.tags = data.tags.map(n => {
            const t = this.tags.find(x => x.name === n) || this.allTags.find(x => x.name === n);
            return { name: n, colourIndex: t ? t.colourIndex : 0 };
          });
        }
      }
    },

    async toggleTag(name) {
      if (this.isTagged(name)) {
        await this.removeTag(name);
      } else {
        await this.addTag(name);
      }
    },

    async addTagByInput() {
      if (!this.newTagName.trim()) return;
      const name = this.foldedNewTagName;
      if (!name) {
        this.tagError = this.tagErrorLabel;
        return;
      }
      await this.addTag(name);
    },

    closeDropdown() {
      this.openDropdown = false;
      this.newTagName = '';
      this.tagError = '';
    },

    init() {
      this.viewTagLabel = this.$el.dataset.viewTagLabel || '';
      this.removeTagLabel = this.$el.dataset.removeTagLabel || '';
      this.tagErrorLabel = this.$el.dataset.tagErrorLabel || '';
      this.$watch('newTagName', () => { this.tagError = ''; });
      this.$watch('openDropdown', (val) => {
        if (!val) this.tagError = '';
        if (val) this.$nextTick(() => this.$refs.tagInput && this.$refs.tagInput.focus());
      });
    }
  }));

  // Message view "More" menu. dismiss() closes without moving focus, for
  // outside clicks; close() returns focus to the button, for Escape.
  Alpine.data('messageMore', () => ({
    open: false,
    toggle() {
      this.open = !this.open;
    },
    dismiss() {
      this.open = false;
    },
    close() {
      if (!this.open) return;
      this.open = false;
      this.$refs.button.focus();
    }
  }));

  // "Load remote images" banner. Only the blocked placeholders inside the
  // article that holds the clicked banner are loaded; other messages in a
  // thread keep their own banners. Only http(s) sources are fetched.
  Alpine.data('remoteImages', () => ({
    load() {
      const banner = this.$root;
      const article = banner.closest('article') || banner.parentElement;
      article.querySelectorAll('.blocked-image[data-src]').forEach(function (el) {
        const src = el.dataset.src || '';
        if (!/^https?:\/\//i.test(src)) return;
        const img = document.createElement('img');
        img.src = src;
        img.className = 'loaded-remote-image';
        el.replaceWith(img);
      });
      banner.remove();
    }
  }));

  Alpine.data('quarantineBulk', () => ({
    selected: [],
    allIds: [],

    init() {
      this.refreshIds();
    },

    refreshIds() {
      this.allIds = Array.from(this.$el.querySelectorAll('tbody input[type=checkbox][value]')).map(el => el.value);
    },

    toggleAll(event) {
      this.selected = event.target.checked ? [...this.allIds] : [];
    },

    allSelected() {
      return this.allIds.length > 0 && this.selected.length === this.allIds.length;
    },

    // Empty junk deletes every listed message in one click, so ask first.
    confirmEmpty(event) {
      if (!window.confirm(event.target.dataset.confirm)) event.preventDefault();
    },

    appendIds(event) {
      var form = event.target;
      var container = form.querySelector('div[id^="bulk-ids"]');
      if (!container) return;
      container.innerHTML = '';
      this.selected.forEach(function(id) {
        var input = document.createElement('input');
        input.type = 'hidden';
        input.name = 'ids';
        input.value = id;
        container.appendChild(input);
      });
    }
  }));

  // Rich-text editor component. Expects the following structure inside the x-data root:
  //   [data-editor]       — contenteditable div for visual editing
  //   [data-source]       — <textarea> for raw HTML source view
  //   [data-img-input]    — <input type="file"> for image upload (hidden)
  //   data-initial-body   — attribute on the root element with the initial plain-text body
  //                         (sets editor.innerText; plain text is safely displayed, not parsed)
  //   data-initial-html   — attribute on the root element with pre-sanitized HTML to seed the
  //                         editor via innerHTML (used when the stored value is already HTML)
  //   data-field-name     — name of the <input type="hidden"> to keep in sync; defaults to
  //                         'body' when absent so the compose wiring is unaffected
  //   <input type="hidden" name="body"> — default hidden field; override via data-field-name
  //
  // Pure helper functions (escapeHTML, buildDataURI, insertImageHTML, resolveFieldName, etc.)
  // are provided by richeditor.js (window.RichEditor), which is loaded before bongo.js.
  Alpine.data('richEditor', () => ({
    isSourceMode: false,

    _editor() {
      return this.$el.querySelector('[data-editor]');
    },

    _source() {
      return this.$el.querySelector('[data-source]');
    },

    // _hidden locates the hidden input whose value is submitted with the form.
    // The field name is read from data-field-name on the component root, defaulting
    // to 'body' so existing compose wiring (which omits the attribute) is unaffected.
    // window.RichEditor.resolveFieldName is used to centralise the defaulting logic so
    // it can be unit-tested independently.
    _hidden() {
      var RE = window.RichEditor;
      var name = RE ? RE.resolveFieldName(this.$el.dataset) : (this.$el.dataset.fieldName || 'body');
      return this.$el.querySelector('input[type="hidden"][name="' + name + '"]');
    },

    _syncHidden() {
      var hidden = this._hidden();
      if (!hidden) return;
      if (this.isSourceMode) {
        var source = this._source();
        if (source) hidden.value = source.value;
      } else {
        var editor = this._editor();
        if (editor) hidden.value = editor.innerHTML;
      }
    },

    init() {
      this.$nextTick(() => {
        // Seed editor with initial content (e.g. quoted body on reply/forward).
        // data-initial-body is set by the template from the server-supplied body string.
        // innerText is used so plain-text newlines become visual line breaks and any
        // HTML characters in the server value are safely displayed, not parsed as markup.
        var initial = this.$el.dataset.initialBody;
        if (initial) {
          var editor = this._editor();
          if (editor) editor.innerText = initial;
        }
        // data-initial-html seeds the editor with pre-sanitized HTML (e.g. a stored
        // HTML signature). innerHTML is used because the value is already sanitized
        // on the server side (via sanitize.Compose) before being stored.
        // This path takes precedence over data-initial-body when both are set.
        var initialHtml = this.$el.dataset.initialHtml;
        if (initialHtml) {
          var editor = this._editor();
          if (editor) editor.innerHTML = initialHtml;
        }
        this._syncHidden();

        // Sync the hidden field on form submission regardless of which mode the
        // editor is in when the user clicks Send or Save Draft.
        var form = this.$el.closest('form');
        if (form) {
          var self = this;
          form.addEventListener('submit', function () { self._syncHidden(); });
        }
      });
    },

    // format executes a document editing command (bold, italic, underline, etc.)
    // on the current selection in the contenteditable editor.
    format(cmd) {
      if (this.isSourceMode) return;
      document.execCommand(cmd, false, null);
      this._syncHidden();
    },

    // setColor applies a foreground colour to the current selection.
    setColor(color) {
      if (this.isSourceMode || !color) return;
      document.execCommand('foreColor', false, color);
      this._syncHidden();
    },

    // promptLink shows a browser prompt for a URL then wraps the selection in a link.
    promptLink() {
      if (this.isSourceMode) return;
      var url = window.prompt('Enter URL:');
      if (url) this.insertLink(url);
    },

    // insertLink wraps the current selection in an anchor with the given URL.
    insertLink(url) {
      if (this.isSourceMode || !url) return;
      document.execCommand('createLink', false, url);
      this._syncHidden();
    },

    // triggerImagePicker clicks the hidden file input to open the OS picker.
    triggerImagePicker() {
      var input = this.$el.querySelector('[data-img-input]');
      if (input) input.click();
    },

    // handleImageFile is called from x-on:change on the file input.
    // It reads $event and delegates to insertImage so the file-picker path
    // obeys the same 1 MB cap as direct calls.
    handleImageFile(event) {
      var file = event && event.target && event.target.files && event.target.files[0];
      if (file) this.insertImage(file);
    },

    // insertImage reads a local File via window.RichEditor.fileToDataURI and
    // inserts an <img> into the editor. Files over 1 MB are silently rejected.
    insertImage(file) {
      if (this.isSourceMode || !file) return;
      var self = this;
      var RE = window.RichEditor;
      if (!RE) {
        console.error('richEditor: window.RichEditor not loaded — richeditor.js must precede bongo.js');
        return;
      }
      RE.fileToDataURI(file, RE.IMAGE_CAP_BYTES)
        .then(function (dataURI) {
          var html = RE.insertImageHTML(dataURI, file.name || '');
          document.execCommand('insertHTML', false, html);
          self._syncHidden();
        })
        .catch(function (err) {
          console.warn('richEditor: image rejected —', err.message);
        });
    },

    // toggleSource switches between the rendered contenteditable view and the
    // raw-HTML textarea, preserving content in both directions.
    toggleSource() {
      var editor = this._editor();
      var source = this._source();
      var hidden = this._hidden();
      if (!editor || !source) return;

      if (!this.isSourceMode) {
        // Visual → source: copy innerHTML to textarea.
        source.value = editor.innerHTML;
        this.isSourceMode = true;
      } else {
        // Source → visual: copy textarea back to innerHTML.
        editor.innerHTML = source.value;
        this.isSourceMode = false;
        if (hidden) hidden.value = editor.innerHTML;
      }
    },

    // onInput syncs the hidden field whenever the user types in the editor.
    onInput() {
      this._syncHidden();
    }
  }));

  Alpine.data('tagSettings', (palette) => ({
    palette: palette,
    tags: [],
    editingTag: null,
    editName: '',
    editColourIndex: 0,
    confirmDeleteTag: null,
    newTagName: '',
    newTagColourIndex: 0,
    savingTag: false,
    deletingTag: false,
    creatingTag: false,
    errorMsg: null,

    countLabel: '%s',
    colourFmt: '%s',
    colourNames: [],
    editFmt: '%s',
    deleteFmt: '%s',

    init() {
      const d = this.$el.dataset;
      this.countLabel = d.countLabel || this.countLabel;
      this.colourFmt = d.colourLabel || this.colourFmt;
      try { this.colourNames = JSON.parse(d.colourNames || '[]'); } catch (e) { this.colourNames = []; }
      this.editFmt = d.editTagLabel || this.editFmt;
      this.deleteFmt = d.deleteTagLabel || this.deleteFmt;
      this.loadTags();
    },

    countTitle(tag) {
      return this.countLabel.replace('%s', String(tag.count));
    },

    colourLabel(idx) {
      const name = this.colourNames[idx] || String(idx);
      return this.colourFmt.replace('%s', () => name);
    },

    editLabel(tag) {
      return this.editFmt.replace('%s', () => tag.name);
    },

    deleteLabel(tag) {
      return this.deleteFmt.replace('%s', () => tag.name);
    },

    tagPaletteBGClass(index) {
      return 'tag-palette-bg-' + (Number.isInteger(index) ? index : 0);
    },

    tagPaletteHex(index) {
      return this.palette[index] || this.palette[0] || '';
    },

    loadTags() {
      fetch('/api/tags')
        .then(r => r.ok ? r.json() : Promise.reject(r))
        .then(data => {
          this.tags = data.map(t => ({
            name: t.name,
            colourIndex: t.colour_index,
            count: t.count,
            sortOrder: t.sort_order
          }));
        })
        .catch(() => { this.errorMsg = i18n('js.tagsLoadFailed'); });
    },

    startEdit(tag) {
      this.editingTag = tag.name;
      this.editName = tag.name;
      this.editColourIndex = tag.colourIndex;
      this.confirmDeleteTag = null;
      this.errorMsg = null;
      this.$nextTick(() => {
        if (this.$refs.editInput) this.$refs.editInput.focus();
      });
    },

    cancelEdit() {
      this.editingTag = null;
      this.editName = '';
      this.editColourIndex = 0;
      this.errorMsg = null;
    },

    saveTag(tag) {
      if (this.savingTag) return;
      const name = this.editName.trim();
      if (!name) {
        this.errorMsg = i18n('js.tagNameEmpty');
        return;
      }

      const body = {};
      if (name !== tag.name) body.name = name;
      if (this.editColourIndex !== tag.colourIndex) body.colour_index = this.editColourIndex;

      if (Object.keys(body).length === 0) {
        this.cancelEdit();
        return;
      }

      this.savingTag = true;
      this.errorMsg = null;
      fetch('/api/tags/' + encodeURIComponent(tag.name), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      })
        .then(r => r.ok ? r.json() : r.text().then(t => Promise.reject(t)))
        .then(() => {
          this.cancelEdit();
          this.loadTags();
        })
        .catch(err => {
          this.errorMsg = typeof err === 'string' ? err : i18n('js.tagSaveFailed');
        })
        .finally(() => { this.savingTag = false; });
    },

    deleteTag(tag) {
      if (this.deletingTag) return;
      this.deletingTag = true;
      this.errorMsg = null;
      fetch('/api/tags/' + encodeURIComponent(tag.name), { method: 'DELETE' })
        .then(r => {
          if (!r.ok) return r.text().then(t => Promise.reject(t));
        })
        .then(() => {
          this.confirmDeleteTag = null;
          this.loadTags();
        })
        .catch(err => {
          this.errorMsg = typeof err === 'string' ? err : i18n('js.tagDeleteFailed');
        })
        .finally(() => { this.deletingTag = false; });
    },

    createTag() {
      const name = this.newTagName.trim();
      if (!name || this.creatingTag) return;
      this.creatingTag = true;
      this.errorMsg = null;
      fetch('/api/tags', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name })
      })
        .then(r => r.ok ? r.json() : r.text().then(t => Promise.reject(t)))
        .then(created => {
          if (this.newTagColourIndex !== created.colour_index) {
            return fetch('/api/tags/' + encodeURIComponent(created.name), {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ colour_index: this.newTagColourIndex })
            }).then(r => r.ok ? r.json() : r.text().then(t => Promise.reject(t)));
          }
        })
        .then(() => {
          this.newTagName = '';
          this.newTagColourIndex = 0;
          this.loadTags();
        })
        .catch(err => {
          this.errorMsg = typeof err === 'string' ? err : i18n('tag.createFailed');
        })
        .finally(() => { this.creatingTag = false; });
    }
  }));
});
