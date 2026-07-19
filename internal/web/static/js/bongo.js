// Bongo-Go client-side JavaScript (Alpine.js components + keyboard shortcuts)

// i18n returns a localized string exposed by the server as a JSON object on the
// #i18n-data element (see layout.templ / i18nJSON). CSP forbids inline scripts,
// so the translations travel in a data attribute. key is a message id such as
// 'shortcuts.title' or 'js.scanCancelled'.
function i18n(key) {
  if (i18n._cache === undefined) {
    const el = document.getElementById('i18n-data');
    try {
      i18n._cache = el ? JSON.parse(el.dataset.messages) : {};
    } catch (e) {
      i18n._cache = {};
    }
  }
  return i18n._cache[key] || '';
}

// CSRF: the server requires an X-CSRF-Token header on state-changing requests.
// HTMX sends it automatically via hx-headers on <body>, but Alpine's fetch()
// calls (bulk actions, settings auto-save, tag CRUD, etc.) do not, so wrap
// window.fetch to inject the token on same-origin, non-GET requests. The token
// is the same value rendered into the body's hx-headers attribute.
(function () {
  function csrfToken() {
    try {
      var hh = document.body && document.body.getAttribute('hx-headers');
      if (hh) return (JSON.parse(hh) || {})['X-CSRF-Token'] || '';
    } catch (e) { /* ignore */ }
    return '';
  }
  var origFetch = window.fetch;
  window.fetch = function (input, init) {
    init = init || {};
    var url = (typeof input === 'string') ? input : (input && input.url) || '';
    var method = (init.method || (typeof input === 'object' && input.method) || 'GET').toUpperCase();
    var sameOrigin = url.indexOf('/') === 0 || url.indexOf(window.location.origin) === 0;
    if (sameOrigin && method !== 'GET' && method !== 'HEAD') {
      var token = csrfToken();
      if (token) {
        var headers = new Headers(init.headers || (typeof input === 'object' ? input.headers : undefined) || {});
        if (!headers.has('X-CSRF-Token')) headers.set('X-CSRF-Token', token);
        init.headers = headers;
      }
    }
    return origFetch.call(this, input, init);
  };
})();

// CSRF for native (non-HTMX, non-fetch) <form> submissions: a full-page browser
// POST cannot set an X-CSRF-Token header, so inject the token as a hidden _csrf
// field on submit. GET forms are skipped so the token never lands in a URL (log
// / referrer leakage). Runs in the capture phase so the field is present before
// the browser serialises the form.
(function () {
  function csrfToken() {
    try {
      var hh = document.body && document.body.getAttribute('hx-headers');
      if (hh) return (JSON.parse(hh) || {})['X-CSRF-Token'] || '';
    } catch (e) { /* ignore */ }
    return '';
  }
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || form.tagName !== 'FORM') return;
    var method = (form.getAttribute('method') || 'GET').toUpperCase();
    if (method === 'GET' || method === 'HEAD') return;
    if (form.querySelector('input[name="_csrf"]')) return;
    var token = csrfToken();
    if (!token) return;
    var input = document.createElement('input');
    input.type = 'hidden';
    input.name = '_csrf';
    input.value = token;
    form.appendChild(input);
  }, true);
})();

document.addEventListener('alpine:init', () => {
  function replacePrefixedClass(el, prefix, nextClass) {
    if (!el) return;
    Array.from(el.classList).forEach(function(cls) {
      if (cls.indexOf(prefix) === 0) {
        el.classList.remove(cls);
      }
    });
    if (nextClass) {
      el.classList.add(nextClass);
    }
  }

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

  function calendarTopClass(minutes) {
    var clamped = Math.max(0, Math.min(1439, minutes));
    return 'cal-top-' + clamped;
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
    },

    setDensity(d) {
      this.density = d;
      localStorage.setItem('bongo-density', d);
      this.applyDensity();
      const form = new URLSearchParams();
      form.set('density', d);
      fetch('/settings', { method: 'POST', body: form }).catch(() => {});
    },

    toggleSidebar() {
      this.sidebarOpen = !this.sidebarOpen;
    }
  }));

  // Collapsible sidebar sections with localStorage persistence
  Alpine.data('sidebarSection', (key) => ({
    open: localStorage.getItem('bongo-sidebar-' + key) !== 'closed',
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

    init() {
      // Read the server-rendered value attribute to seed the query.
      // We use getAttribute('value') because it reads the HTML attribute
      // directly, not the DOM property which Alpine may have already cleared.
      const initial = this.$el.getAttribute('value');
      if (initial) {
        this.query = initial;
      }
    },

    async search() {
      if (this.query.length < 2) {
        this.results = [];
        this.show = false;
        return;
      }
      this.loading = true;
      try {
        const resp = await fetch('/api/gal?q=' + encodeURIComponent(this.query));
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
      this.query = entry.email;
      this.show = false;
      this.results = [];
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
        const resp = await fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk', {
          method: 'POST',
          body: params
        });
        if (resp.ok) {
          this.selected = {};
          this.count = 0;
          this.lastClicked = null;
          setToolbarSelectAllChecked(false);
          // Refresh message list via HTMX.
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }
      } catch (e) {
        console.error('Bulk action failed:', e);
      }
    }
  }));

  // Combined component for message list: merges messageSelect + swipeHandler.
  // Used as x-data="messageList" (Alpine.data registered names cannot be
  // called as functions inside inline x-data expressions).
  Alpine.data('messageList', () => ({
    // messageSelect state
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
        const resp = await fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk', {
          method: 'POST',
          body: params
        });
        if (resp.ok) {
          this.selected = {};
          this.count = 0;
          this.lastClicked = null;
          setToolbarSelectAllChecked(false);
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }
      } catch (e) {
        console.error('Bulk action failed:', e);
      }
    },

    async bulkTagAction(mailbox, action, tagName) {
      tagName = (tagName || '').trim();
      if (!tagName || this.count === 0) return;
      const messageIds = Object.keys(this.selected).map(uid => ({ mailbox: mailbox, uid: parseInt(uid) }));
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
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        } else {
          console.error('Bulk tag action failed:', resp.status);
        }
      } catch (e) {
        console.error('Bulk tag action error:', e);
      }
    },

    // swipeHandler state
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
        finalizeSwipeState(this.swipeTarget, 'left');
        var params = new URLSearchParams();
        params.set('destination', 'Trash');
        fetch('/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/move', {
          method: 'POST', body: params
        }).then(function() {
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }).catch(function() {});
      } else if (deltaX > 60) {
        finalizeSwipeState(this.swipeTarget, 'right');
        var params2 = new URLSearchParams();
        params2.set('action', 'delete');
        params2.append('uids', uid.toString());
        fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk', {
          method: 'POST', body: params2
        }).then(function() {
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
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
      this.$dispatch('ctx-menu', { x: event.clientX, y: event.clientY, uid: uid, mailbox: mailbox });
    }
  }));

  // Attachment drop-zone for the compose form.
  Alpine.data('dropZone', () => ({
    files: [],
    totalSize: 0,

    formatBytes(b) {
      if (b < 1024) return b + ' B';
      if (b < 1048576) return (b / 1024).toFixed(1) + ' KB';
      return (b / 1048576).toFixed(1) + ' MB';
    },

    updateFiles(el) {
      this.files = Array.from(el.files);
      this.totalSize = this.files.reduce(function(s, f) { return s + f.size; }, 0);
    }
  }));

  // Desktop notifications + SSE
  Alpine.data('notifications', () => ({
    permission: typeof Notification !== 'undefined' ? Notification.permission : 'denied',
    eventSource: null,

    init() {
      this.connectSSE();
    },

    requestPermission() {
      if (typeof Notification === 'undefined') return;
      Notification.requestPermission().then(p => {
        this.permission = p;
      });
    },

    connectSSE() {
      if (this.eventSource) return;
      this.eventSource = new EventSource('/events?mailbox=INBOX');

      this.eventSource.addEventListener('newmail', (e) => {
        try {
          const data = JSON.parse(e.data);
          if (this.permission === 'granted') {
            const n = new Notification(data.from || i18n('js.newMail'), {
              body: data.subject || '',
              icon: '/static/logo.png'
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

      this.eventSource.addEventListener('reminder', (e) => {
        try {
          const data = JSON.parse(e.data);
          if (this.permission === 'granted') {
            const n = new Notification(i18n('js.eventReminderPrefix') + data.minutes_before + i18n('js.eventReminderSuffix'), {
              body: data.title || i18n('js.upcomingEvent'),
              icon: '/static/logo.png'
            });
            n.onclick = () => { window.focus(); n.close(); };
          }
        } catch (err) { /* ignore parse errors */ }
      });

      this.eventSource.onerror = () => {
        // Reconnect after 5 seconds.
        this.eventSource.close();
        this.eventSource = null;
        setTimeout(() => this.connectSSE(), 5000);
      };
    },

    destroy() {
      if (this.eventSource) {
        this.eventSource.close();
        this.eventSource = null;
      }
    }
  }));

  // Mobile swipe gestures (touch devices only)
  Alpine.data('swipeHandler', () => ({
    startX: 0,
    startY: 0,
    currentX: 0,
    swiping: false,
    swipeTarget: null,

    start(event, uid) {
      if (!window.matchMedia('(pointer: coarse)').matches) return;
      const touch = event.touches[0];
      this.startX = touch.clientX;
      this.startY = touch.clientY;
      this.currentX = 0;
      this.swiping = false;
      this.swipeTarget = event.currentTarget;
    },

    move(event) {
      if (!this.swipeTarget) return;
      const touch = event.touches[0];
      const deltaX = touch.clientX - this.startX;
      const deltaY = touch.clientY - this.startY;
      if (Math.abs(deltaX) > 20 && Math.abs(deltaY) < 30) {
        this.swiping = true;
        this.currentX = deltaX;
        setSwipeDragState(this.swipeTarget, deltaX);
      }
    },

    end(event, uid, mailbox) {
      if (!this.swipeTarget || !this.swiping) {
        this.reset();
        return;
      }
      const deltaX = this.currentX;
      if (deltaX < -60) {
        // Swipe left: archive (move to Trash)
        finalizeSwipeState(this.swipeTarget, 'left');
        const params = new URLSearchParams();
        params.set('destination', 'Trash');
        fetch('/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/move', {
          method: 'POST', body: params
        }).then(() => {
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }).catch(() => {});
      } else if (deltaX > 60) {
        // Swipe right: delete
        finalizeSwipeState(this.swipeTarget, 'right');
        const params = new URLSearchParams();
        params.set('action', 'delete');
        params.append('uids', uid.toString());
        fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk', {
          method: 'POST', body: params
        }).then(() => {
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }).catch(() => {});
      } else {
        this.reset();
      }
    },

    reset() {
      if (this.swipeTarget) {
        resetSwipeState(this.swipeTarget);
      }
      this.swipeTarget = null;
      this.swiping = false;
      this.currentX = 0;
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
    focusedItem: -1,

    openAt(x, y, uid, mailbox) {
      this.uid = uid;
      this.mailbox = mailbox;
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
      const mailbox = this.mailbox;
      this.close();
      if (!uid || !mailbox) return;

      switch (act) {
        case 'open':
          window.location.href = '/mail/' + encodeURIComponent(mailbox) + '/' + uid;
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
              htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
            }
          }).catch(() => {});
          break;
        case 'not-spam':
          fetch('/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/not-spam', {
            method: 'POST'
          }).then(resp => {
            if (resp.ok) {
              htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
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
              htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
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
                  htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
                }
              }).catch(() => {});
            }
          }
          break;
        }
      }
    }
  }));

  // Message detail tag chips: add/remove tags on a single message.
  // Initialised via x-data="messageTags(tags, allTags, mailbox, uid)".
  Alpine.data('messageTags', (initialTags, allTags, mailbox, uid) => ({
    tags: initialTags,          // [{name, colourIndex}, ...]  current tags on this message
    allTags: allTags,           // [{name, colourIndex}, ...]  all user-defined tags
    openDropdown: false,
    newTagName: '',

    messageTagBGClass(index) {
      return 'message-tag-bg-' + (Number.isInteger(index) ? index : 0);
    },

    messageTagColourClass(index) {
      return 'message-tag-colour-' + (Number.isInteger(index) ? index : 0);
    },

    get filteredAllTags() {
      const q = this.newTagName.trim().toLowerCase();
      if (!q) return this.allTags;
      return this.allTags.filter(t => t.name.toLowerCase().includes(q));
    },

    get exactMatch() {
      const q = this.newTagName.trim().toLowerCase();
      return this.allTags.some(t => t.name.toLowerCase() === q) ||
             this.tags.some(t => t.name.toLowerCase() === q);
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
          if (!this.allTags.some(t => t.name === name)) {
            this.allTags.push({ name, colourIndex });
          }
        }
      }
      this.closeDropdown();
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
      const name = this.newTagName.trim();
      if (!name) return;
      await this.addTag(name);
      this.newTagName = '';
    },

    closeDropdown() {
      this.openDropdown = false;
      this.newTagName = '';
    },

    init() {
      this.$watch('openDropdown', (val) => {
        if (val) this.$nextTick(() => this.$refs.tagInput && this.$refs.tagInput.focus());
      });
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

    init() {
      this.loadTags();
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

// Reading pane: intercept message link clicks in pane modes.
document.addEventListener('click', (e) => {
  const link = e.target.closest('a[data-pane-url]');
  if (!link) return;
  const layout = localStorage.getItem('bongo-layout') || 'list';
  if (layout === 'list') return;
  e.preventDefault();
  e.stopPropagation();
  htmx.ajax('GET', link.dataset.paneUrl, '#reading-pane');
});

// Keyboard shortcut help modal
var shortcutModalOpen = false;

function toggleShortcutModal() {
  shortcutModalOpen = !shortcutModalOpen;
  var overlay = document.getElementById('shortcut-modal-overlay');
  if (!overlay) {
    overlay = document.createElement('div');
    overlay.id = 'shortcut-modal-overlay';
    overlay.className = 'shortcut-modal-overlay';
    overlay.setAttribute('role', 'presentation');
    overlay.innerHTML =
      '<div class="shortcut-modal" role="dialog" aria-labelledby="shortcut-modal-title" aria-modal="true">' +
        '<h3 id="shortcut-modal-title">' + i18n('shortcuts.title') + '</h3>' +
        '<dl>' +
          '<dt><kbd>j</kbd></dt><dd>' + i18n('js.shortcutNextMessage') + '</dd>' +
          '<dt><kbd>k</kbd></dt><dd>' + i18n('js.shortcutPrevMessage') + '</dd>' +
          '<dt><kbd>Enter</kbd></dt><dd>' + i18n('js.shortcutOpenMessage') + '</dd>' +
          '<dt><kbd>r</kbd></dt><dd>' + i18n('js.shortcutReply') + '</dd>' +
          '<dt><kbd>d</kbd></dt><dd>' + i18n('js.shortcutDelete') + '</dd>' +
          '<dt><kbd>s</kbd></dt><dd>' + i18n('js.shortcutToggleStar') + '</dd>' +
          '<dt><kbd>/</kbd></dt><dd>' + i18n('js.shortcutFocusSearch') + '</dd>' +
          '<dt><kbd>c</kbd></dt><dd>' + i18n('js.shortcutCompose') + '</dd>' +
          '<dt><kbd>Esc</kbd></dt><dd>' + i18n('js.shortcutCloseModal') + '</dd>' +
          '<dt><kbd>?</kbd></dt><dd>' + i18n('js.shortcutShowHelp') + '</dd>' +
        '</dl>' +
        '<button type="button" class="outline secondary modal-close" id="shortcut-modal-close">' + i18n('js.close') + '</button>' +
      '</div>';
    document.body.appendChild(overlay);
    overlay.addEventListener('click', function(ev) {
      if (ev.target === overlay) toggleShortcutModal();
    });
    document.getElementById('shortcut-modal-close').addEventListener('click', function() {
      toggleShortcutModal();
    });
  }
  if (shortcutModalOpen) {
    overlay.hidden = false;
    // Focus trap: focus the close button.
    document.getElementById('shortcut-modal-close').focus();
  } else {
    overlay.hidden = true;
  }
}

// Focus trap for shortcut modal
document.addEventListener('keydown', function(e) {
  if (!shortcutModalOpen) return;
  if (e.key === 'Escape') {
    e.preventDefault();
    toggleShortcutModal();
    return;
  }
  if (e.key === 'Tab') {
    // Trap focus inside modal.
    var modal = document.querySelector('.shortcut-modal');
    if (!modal) return;
    var focusable = modal.querySelectorAll('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])');
    if (focusable.length === 0) return;
    var first = focusable[0];
    var last = focusable[focusable.length - 1];
    if (e.shiftKey) {
      if (document.activeElement === first) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }
});

// Keyboard shortcuts (only when not in an input/textarea)
document.addEventListener('keydown', (e) => {
  if (shortcutModalOpen) return;

  // Ignore shortcuts when a modifier is held so browser/OS combos like
  // Ctrl/Cmd+C (copy), Cmd+R (reload) etc. are not hijacked as single-key
  // shortcuts (e.g. 'c' = compose).
  if (e.ctrlKey || e.metaKey || e.altKey) {
    return;
  }

  const tag = e.target.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.target.isContentEditable) {
    return;
  }

  if (e.key === '?') {
    e.preventDefault();
    toggleShortcutModal();
    return;
  }

  if (e.key === 'c') {
    e.preventDefault();
    window.location.href = '/compose';
    return;
  }

  const messageRows = document.querySelectorAll('.msg-list .msg-row');
  if (messageRows.length === 0) return;

  const current = document.querySelector('.msg-list .msg-row.keyboard-focus');
  let idx = current ? Array.from(messageRows).indexOf(current) : -1;

  switch (e.key) {
    case 'j': // Next message
      e.preventDefault();
      if (idx < messageRows.length - 1) {
        if (current) current.classList.remove('keyboard-focus');
        messageRows[idx + 1].classList.add('keyboard-focus');
        messageRows[idx + 1].scrollIntoView({ block: 'nearest' });
      }
      break;

    case 'k': // Previous message
      e.preventDefault();
      if (idx > 0) {
        if (current) current.classList.remove('keyboard-focus');
        messageRows[idx - 1].classList.add('keyboard-focus');
        messageRows[idx - 1].scrollIntoView({ block: 'nearest' });
      }
      break;

    case 'Enter': // Open focused message
      if (current) {
        e.preventDefault();
        const link = current.querySelector('a[data-message-link]');
        if (link) link.click();
      }
      break;

    case 'r': // Reply
      if (current) {
        e.preventDefault();
        const uid = current.dataset.uid;
        const mailbox = document.querySelector('[data-selected-mailbox]')?.dataset.selectedMailbox;
        if (uid && mailbox) {
          window.location.href = '/compose/reply/' + mailbox + '/' + uid;
        }
      }
      break;

    case 'd': // Delete
      if (current) {
        e.preventDefault();
        const deleteBtn = current.querySelector('[data-action="delete"]');
        if (deleteBtn) deleteBtn.click();
      }
      break;

    case 's': // Star/flag toggle
      if (current) {
        e.preventDefault();
        const starBtn = current.querySelector('.msg-star');
        if (starBtn) starBtn.click();
      }
      break;

    case '/': // Focus search
      e.preventDefault();
      const searchInput = document.querySelector('.search-input input, .search-input');
      if (searchInput) searchInput.focus();
      break;
  }
});

// Focus management after HTMX content swaps
document.body.addEventListener('htmx:afterSwap', function(evt) {
  var target = evt.detail.target;
  var focusable = target.querySelector('h1, h2, h3, [autofocus], a, button, input:not([type="hidden"])');
  if (focusable) {
    focusable.setAttribute('tabindex', '-1');
    focusable.focus();
  }
  // Announce swap to screen readers.
  var announcer = document.getElementById('aria-announcements');
  if (announcer) {
    var heading = target.querySelector('h1, h2, h3');
    if (heading) {
      announcer.textContent = heading.textContent + i18n('js.contentLoaded');
    }
  }
});

// Drag-and-drop: make message rows draggable to sidebar folders.
function initDragDrop() {
  const msgList = document.querySelector('.msg-list');
  if (!msgList || typeof Sortable === 'undefined') return;

  // Make message rows draggable (but not sortable within the list).
  Sortable.create(msgList, {
    group: { name: 'messages', pull: 'clone', put: false },
    sort: false,
    ghostClass: 'sortable-ghost',
    filter: 'input[type="checkbox"], .msg-star',
    onStart(evt) {
      // Store source mailbox and uid on the dragged element.
      const row = evt.item;
      const mailboxEl = document.querySelector('[data-selected-mailbox]');
      row.dataset.srcMailbox = mailboxEl ? mailboxEl.dataset.selectedMailbox : 'INBOX';
    }
  });

  // Make sidebar folder links drop targets.
  document.querySelectorAll('[data-folder]').forEach(el => {
    el.addEventListener('dragover', (e) => {
      e.preventDefault();
      el.classList.add('drag-over');
    });
    el.addEventListener('dragleave', () => {
      el.classList.remove('drag-over');
    });
    el.addEventListener('drop', (e) => {
      e.preventDefault();
      el.classList.remove('drag-over');
      // Find the dragged element.
      const ghost = document.querySelector('.sortable-ghost');
      const uid = ghost ? ghost.dataset.uid : null;
      const srcMailbox = ghost ? ghost.dataset.srcMailbox : null;
      const destFolder = el.dataset.folder;
      if (uid && srcMailbox && destFolder && srcMailbox !== destFolder) {
        const form = new URLSearchParams();
        form.set('destination', destFolder);
        fetch('/mail/' + encodeURIComponent(srcMailbox) + '/' + uid + '/move', {
          method: 'POST',
          body: form
        }).then(resp => {
          if (resp.ok) {
            // Remove the row from the list.
            const row = document.querySelector('tr[data-uid="' + uid + '"]');
            if (row) row.remove();
          }
        }).catch(() => {});
      }
    });
  });
}

function syncAdminLayoutClass() {
  if (!document.body) return;
  document.body.classList.toggle('admin-layout', !!document.querySelector('[data-admin-page]'));
}

function initRulesSortable() {
  if (typeof Sortable === 'undefined') return;
  var el = document.getElementById('sortable-rules');
  if (!el || el._sortableInit) return;
  el._sortableInit = true;

  Sortable.create(el, {
    handle: '.drag-handle',
    animation: 150,
    onEnd: function() {
      var ids = Array.from(el.querySelectorAll('tr[data-id]')).map(function(r) { return r.dataset.id; });
      fetch('/settings/rules/reorder', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(ids)
      });
    }
  });
}

function getAdminLogState() {
  if (!window.__bongoAdminLogState) {
    window.__bongoAdminLogState = { source: null };
  }
  return window.__bongoAdminLogState;
}

function clearAdminLogOutput() {
  var output = document.querySelector('[data-log-output]');
  if (output) output.textContent = '';
}

function appendAdminLogLine(output, text, className) {
  var el = document.createElement('div');
  el.className = className || 'log-line';
  el.textContent = text;
  output.appendChild(el);
  output.scrollTop = output.scrollHeight;
}

function connectAdminLogs() {
  var output = document.querySelector('[data-log-output]');
  var level = document.getElementById('log-level');
  var component = document.getElementById('log-component');
  var connectButton = document.getElementById('log-connect');
  if (!output || !level || !component) return;

  var state = getAdminLogState();
  if (state.source) state.source.close();

  var url = '/admin/logs/stream?level=' + encodeURIComponent(level.value) +
    '&component=' + encodeURIComponent(component.value);
  state.source = new EventSource(url);
  output.textContent = '';
  appendAdminLogLine(output, i18n('js.logConnecting'), 'log-line log-line-muted');
  state.source.onopen = function() {
    if (connectButton) connectButton.classList.add('connected');
    appendAdminLogLine(output, i18n('js.logConnected'), 'log-line log-line-muted');
  };
  state.source.onmessage = function(e) {
    appendAdminLogLine(output, e.data, 'log-line');
  };
  state.source.onerror = function() {
    if (connectButton) connectButton.classList.remove('connected');
    appendAdminLogLine(output, i18n('js.logConnectionLost'), 'log-line log-line-error');
  };
}

function initAdminLogViewer() {
  var viewer = document.querySelector('.log-viewer');
  if (!viewer || viewer.dataset.logInit === 'true') return;
  viewer.dataset.logInit = 'true';
  connectAdminLogs();
}

function initContactsPage() {
  var createForm = document.getElementById('create-form');
  if (!createForm) return;
  createForm.dataset.contactFormReady = 'true';
}

document.addEventListener('click', function(e) {
  var copyBtn = e.target.closest('[data-copy-previous]');
  if (copyBtn) {
    var previous = copyBtn.previousElementSibling;
    if (previous && navigator.clipboard) {
      navigator.clipboard.writeText(previous.textContent.trim()).catch(function() {});
    }
    return;
  }

  var printBtn = e.target.closest('[data-print-page]');
  if (printBtn) {
    window.print();
    return;
  }

  var folderToggle = e.target.closest('[data-folder-toggle]');
  if (folderToggle) {
    e.preventDefault();
    e.stopPropagation();
    var li = folderToggle.closest('.nav-has-children');
    if (li) {
      li.classList.toggle('collapsed');
      folderToggle.setAttribute('aria-expanded', String(!li.classList.contains('collapsed')));
    }
    return;
  }

  var contactToggle = e.target.closest('[data-contact-form-toggle]');
  if (contactToggle) {
    var createForm = document.getElementById('create-form');
    if (!createForm) return;
    var action = contactToggle.dataset.contactFormToggle;
    if (action === 'hide') {
      createForm.setAttribute('hidden', '');
    } else {
      createForm.toggleAttribute('hidden');
    }
    return;
  }

  var contactRow = e.target.closest('[data-contact-select]');
  if (contactRow) {
    document.querySelectorAll('.contact-card').forEach(function(card) {
      card.classList.remove('selected');
    });
    contactRow.classList.add('selected');
    var panes = document.getElementById('contacts-panes');
    if (panes) panes.classList.add('showing-detail');
    return;
  }

  var logAction = e.target.closest('[data-log-action]');
  if (logAction) {
    if (logAction.dataset.logAction === 'connect') {
      connectAdminLogs();
    } else if (logAction.dataset.logAction === 'clear') {
      clearAdminLogOutput();
    }
  }
});

// Re-init drag-drop after HTMX swaps.
document.addEventListener('htmx:afterSettle', initDragDrop);

// Re-init Alpine components after HTMX swaps so x-data/x-show works on new content.
document.addEventListener('htmx:afterSettle', function(evt) {
  if (window.Alpine && evt.detail && evt.detail.target) {
    Alpine.initTree(evt.detail.target);
  }
});

// Calendar: event drag-and-drop rescheduling in week/day views.
function initEventDrag() {
  document.querySelectorAll('.event-block[data-guid]').forEach(block => {
    if (block._dragInit) return;
    block._dragInit = true;

    block.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return;
      e.preventDefault();
      e.stopPropagation();

      const col = block.closest('.day-column');
      if (!col) return;

      const guid = block.dataset.guid;
      const startTs = parseInt(block.dataset.start);
      const endTs = parseInt(block.dataset.end);
      const duration = endTs - startTs;
      const colRect = col.getBoundingClientRect();
      const hourHeight = colRect.height / 24;

      block.classList.add('dragging');

      // Create ghost element.
      const ghost = document.createElement('div');
      ghost.className = 'event-ghost';
      Array.from(block.classList).forEach(function(cls) {
        if (cls.indexOf('cal-height-') === 0) {
          ghost.classList.add(cls);
        }
      });
      col.appendChild(ghost);

      const onMouseMove = (ev) => {
        const relY = ev.clientY - colRect.top;
        // Snap to 30-min increments.
        const halfHours = Math.round(relY / (hourHeight / 2));
        const minutes = Math.max(0, Math.min(1410, halfHours * 30));
        replacePrefixedClass(ghost, 'cal-top-', calendarTopClass(minutes));
      };

      const onMouseUp = (ev) => {
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
        block.classList.remove('dragging');
        ghost.remove();

        // Calculate new time from drop position.
        const targetCol = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.day-column');
        if (!targetCol) return;

        const targetRect = targetCol.getBoundingClientRect();
        const relY = ev.clientY - targetRect.top;
        const halfHours = Math.round(relY / (targetRect.height / 24 / 2));
        const minutes = halfHours * 30;
        const hours = Math.floor(minutes / 60);
        const mins = minutes % 60;

        const dateStr = targetCol.dataset.date;
        if (!dateStr) return;

        // Build new timestamp from date + dropped hour.
        const dateParts = dateStr.split('-');
        const newDate = new Date(
          parseInt(dateParts[0]),
          parseInt(dateParts[1]) - 1,
          parseInt(dateParts[2]),
          hours, mins, 0
        );
        const newStart = Math.floor(newDate.getTime() / 1000);
        const newEnd = newStart + duration;

        const form = new URLSearchParams();
        form.set('start_time', newStart.toString());
        form.set('end_time', newEnd.toString());

        fetch('/calendar/event/' + guid + '/reschedule', {
          method: 'POST',
          body: form
        }).then(resp => {
          if (resp.ok) {
            // Refresh current view.
            htmx.ajax('GET', window.location.pathname, '#main-content');
          }
        }).catch(() => {});
      };

      document.addEventListener('mousemove', onMouseMove);
      document.addEventListener('mouseup', onMouseUp);
    });
  });
}

document.addEventListener('htmx:afterSettle', initEventDrag);

// Rule move up/down buttons (keyboard alternative to drag-and-drop)
document.addEventListener('click', function(e) {
  var btn = e.target.closest('[data-move-dir]');
  if (!btn) return;
  var row = btn.closest('tr[data-id]');
  if (!row) return;
  var dir = btn.dataset.moveDir;
  var sibling = dir === 'up' ? row.previousElementSibling : row.nextElementSibling;
  if (!sibling || !sibling.dataset.id) return;
  if (dir === 'up') {
    row.parentNode.insertBefore(row, sibling);
  } else {
    row.parentNode.insertBefore(sibling, row);
  }
  var ids = Array.from(row.closest('tbody').querySelectorAll('tr[data-id]')).map(function(r) { return r.dataset.id; });
  fetch('/settings/rules/reorder', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(ids)});
  // Re-focus the button after DOM move.
  btn.focus();
});

// Sidebar active state: highlight the nav link matching the current URL.
function updateSidebarActive() {
  var path = decodeURIComponent(window.location.pathname);
  document.querySelectorAll('.nav-list a, .nav-item a, a.admin-nav-item').forEach(function(a) {
    var href = a.getAttribute('href');
    if (!href) return;
    try { href = decodeURIComponent(href); } catch(e) {}
    // Exact match, or prefix match only for thread views (/mail/INBOX/thread/xxx).
    // Plain prefix matching would incorrectly highlight parent folders when child folders are active.
    var rest = (href.length > 1 && path.startsWith(href + '/')) ? path.slice(href.length + 1) : '';
    var isActive = (path === href) || rest.startsWith('thread/');
    a.classList.toggle('active', isActive);
  });
}

// Settings tab switching (vanilla JS fallback for Alpine.js).
function initSettingsTabs() {
  var container = document.querySelector('.settings-container');
  if (!container) return;
  if (container.dataset.tabsInit === 'true') return;
  container.dataset.tabsInit = 'true';
  var tabs = container.querySelectorAll('.settings-tab');
  var sections = container.querySelectorAll('form > .settings-section');
  if (tabs.length === 0 || sections.length === 0) return;
  // Show only the first section by default.
  sections.forEach(function(s, i) { s.hidden = i !== 0; });
  tabs.forEach(function(tab, i) {
    tab.addEventListener('click', function() {
      tabs.forEach(function(t) { t.classList.remove('active'); });
      tab.classList.add('active');
      sections.forEach(function(s, j) { s.hidden = j !== i; });
    });
    // Set first tab as active.
    if (i === 0) tab.classList.add('active');
    else tab.classList.remove('active');
  });
}

function initCalendarDynamicStyles(root) {
  return root;
}

// Flash message auto-dismiss + init drag-drop + init settings tabs
document.addEventListener('DOMContentLoaded', () => {
  syncAdminLayoutClass();
  initDragDrop();
  initEventDrag();
  initRulesSortable();
  initAdminLogViewer();
  initContactsPage();
  updateSidebarActive();
  initSettingsTabs();
  document.querySelectorAll('.flash').forEach(el => {
    setTimeout(() => {
      el.classList.add('flash-dismissing');
      setTimeout(() => el.remove(), 300);
    }, 5000);
  });
});

// Update sidebar active state + settings tabs after HTMX navigation.
document.addEventListener('htmx:pushedIntoHistory', updateSidebarActive);
document.addEventListener('htmx:afterSettle', function() {
  syncAdminLayoutClass();
  updateSidebarActive();
  initRulesSortable();
  initAdminLogViewer();
  initContactsPage();
  initSettingsTabs();
});
