// Bongo-Go client-side JavaScript (Alpine.js components + keyboard shortcuts)

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
        const rows = Array.from(document.querySelectorAll('.msg-list .msg-row'));
        const uids = rows.map(r => parseInt(r.dataset.uid));
        const start = uids.indexOf(this.lastClicked);
        const end = uids.indexOf(uid);
        const [lo, hi] = start < end ? [start, end] : [end, start];
        for (let i = lo; i <= hi; i++) {
          this.selected[uids[i]] = true;
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
      const rows = document.querySelectorAll('.msg-list .msg-row');
      rows.forEach(r => { this.selected[parseInt(r.dataset.uid)] = true; });
      this._updateCount();
    },

    deselectAll() {
      this.selected = {};
      this.count = 0;
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
        const rows = Array.from(document.querySelectorAll('.msg-list .msg-row'));
        const uids = rows.map(r => parseInt(r.dataset.uid));
        const start = uids.indexOf(this.lastClicked);
        const end = uids.indexOf(uid);
        const [lo, hi] = start < end ? [start, end] : [end, start];
        for (let i = lo; i <= hi; i++) {
          this.selected[uids[i]] = true;
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
      const rows = document.querySelectorAll('.msg-list .msg-row');
      rows.forEach(r => { this.selected[parseInt(r.dataset.uid)] = true; });
      this._updateCount();
    },

    deselectAll() {
      this.selected = {};
      this.count = 0;
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
            const n = new Notification(data.from || 'New mail', {
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
            const n = new Notification('Event in ' + data.minutes_before + ' minutes', {
              body: data.title || 'Upcoming event',
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
          this.result = 'Scan failed: ' + resp.status;
          return;
        }
      } catch (e) {
        this.scanning = false;
        this.result = 'Scan error: ' + e.message;
        return;
      }

      this.evtSource = new EventSource('/api/mailbox/' + encodeURIComponent(mailbox) + '/scan/progress');
      this.evtSource.onmessage = (e) => {
        try {
          const p = JSON.parse(e.data);
          this.progress = p;
          if (p.done) {
            this.scanning = false;
            this.result = p.scanned + ' scanned, ' + p.spam + ' moved to Junk';
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
      this.result = 'Scan cancelled';
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
        .catch(() => { this.errorMsg = 'Failed to load tags.'; });
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
        this.errorMsg = 'Tag name cannot be empty.';
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
          this.errorMsg = typeof err === 'string' ? err : 'Failed to save tag.';
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
          this.errorMsg = typeof err === 'string' ? err : 'Failed to delete tag.';
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
          this.errorMsg = typeof err === 'string' ? err : 'Failed to create tag.';
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
        '<h3 id="shortcut-modal-title">Keyboard Shortcuts</h3>' +
        '<dl>' +
          '<dt><kbd>j</kbd></dt><dd>Next message</dd>' +
          '<dt><kbd>k</kbd></dt><dd>Previous message</dd>' +
          '<dt><kbd>Enter</kbd></dt><dd>Open message</dd>' +
          '<dt><kbd>r</kbd></dt><dd>Reply</dd>' +
          '<dt><kbd>d</kbd></dt><dd>Delete</dd>' +
          '<dt><kbd>s</kbd></dt><dd>Star / unstar</dd>' +
          '<dt><kbd>/</kbd></dt><dd>Focus search</dd>' +
          '<dt><kbd>c</kbd></dt><dd>Compose new message</dd>' +
          '<dt><kbd>Esc</kbd></dt><dd>Close modal / menu</dd>' +
          '<dt><kbd>?</kbd></dt><dd>Show this help</dd>' +
        '</dl>' +
        '<button type="button" class="outline secondary modal-close" id="shortcut-modal-close">Close</button>' +
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
      announcer.textContent = heading.textContent + ' loaded';
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

function connectAdminLogs() {
  var output = document.querySelector('[data-log-output]');
  var level = document.getElementById('log-level');
  var component = document.getElementById('log-component');
  if (!output || !level || !component) return;

  var state = getAdminLogState();
  if (state.source) state.source.close();

  var url = '/admin/logs/stream?level=' + encodeURIComponent(level.value) +
    '&component=' + encodeURIComponent(component.value);
  state.source = new EventSource(url);
  output.textContent = '';
  state.source.onmessage = function(e) {
    var el = document.createElement('div');
    el.className = 'log-line';
    el.textContent = e.data;
    output.appendChild(el);
    output.scrollTop = output.scrollHeight;
  };
  state.source.onerror = function() {
    var el = document.createElement('div');
    el.className = 'log-line log-line-error';
    el.textContent = '[connection lost]';
    output.appendChild(el);
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
