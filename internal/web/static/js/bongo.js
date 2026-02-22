// Bongo-Go client-side JavaScript (Alpine.js components + keyboard shortcuts)

document.addEventListener('alpine:init', () => {
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

  // GAL autocomplete for compose form
  Alpine.data('galAutocomplete', () => ({
    query: '',
    results: [],
    show: false,
    loading: false,
    selected: -1,

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
    selected: new Set(),
    lastClicked: null,

    toggle(uid, event) {
      if (event && event.shiftKey && this.lastClicked !== null) {
        // Shift-click range select
        const rows = Array.from(document.querySelectorAll('.message-list tbody tr'));
        const uids = rows.map(r => parseInt(r.dataset.uid));
        const start = uids.indexOf(this.lastClicked);
        const end = uids.indexOf(uid);
        const [lo, hi] = start < end ? [start, end] : [end, start];
        for (let i = lo; i <= hi; i++) {
          this.selected.add(uids[i]);
        }
      } else {
        if (this.selected.has(uid)) {
          this.selected.delete(uid);
        } else {
          this.selected.add(uid);
        }
      }
      this.lastClicked = uid;
    },

    selectAll() {
      const rows = document.querySelectorAll('.message-list tbody tr');
      rows.forEach(r => this.selected.add(parseInt(r.dataset.uid)));
    },

    deselectAll() {
      this.selected.clear();
    },

    isSelected(uid) {
      return this.selected.has(uid);
    },

    get count() {
      return this.selected.size;
    },

    async bulkAction(mailbox, action) {
      if (this.selected.size === 0) return;
      const params = new URLSearchParams();
      params.set('action', action);
      for (const uid of this.selected) {
        params.append('uids', uid.toString());
      }
      try {
        const resp = await fetch('/mail/' + encodeURIComponent(mailbox) + '/bulk', {
          method: 'POST',
          body: params
        });
        if (resp.ok) {
          this.selected.clear();
          this.lastClicked = null;
          // Refresh message list via HTMX.
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }
      } catch (e) {
        console.error('Bulk action failed:', e);
      }
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
        this.swipeTarget.style.transform = 'translateX(' + deltaX + 'px)';
        this.swipeTarget.style.transition = 'none';
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
        this.swipeTarget.style.transform = 'translateX(-100%)';
        this.swipeTarget.style.transition = 'transform 200ms ease';
        const params = new URLSearchParams();
        params.set('destination', 'Trash');
        fetch('/mail/' + encodeURIComponent(mailbox) + '/' + uid + '/move', {
          method: 'POST', body: params
        }).then(() => {
          htmx.ajax('GET', '/mail/' + encodeURIComponent(mailbox), '#main-content');
        }).catch(() => {});
      } else if (deltaX > 60) {
        // Swipe right: delete
        this.swipeTarget.style.transform = 'translateX(100%)';
        this.swipeTarget.style.transition = 'transform 200ms ease';
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
        this.swipeTarget.style.transform = '';
        this.swipeTarget.style.transition = 'transform 200ms ease';
      }
      this.swipeTarget = null;
      this.swiping = false;
      this.currentX = 0;
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

    open(event, uid, mailbox) {
      event.preventDefault();
      this.uid = uid;
      this.mailbox = mailbox;
      this.x = event.clientX;
      this.y = event.clientY;
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
      }
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
    overlay.style.display = 'flex';
    // Focus trap: focus the close button.
    document.getElementById('shortcut-modal-close').focus();
  } else {
    overlay.style.display = 'none';
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

  const messageRows = document.querySelectorAll('.message-list tbody tr');
  if (messageRows.length === 0) return;

  const current = document.querySelector('.message-list tbody tr.keyboard-focus');
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
        const starBtn = current.querySelector('.flag-star');
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
  const tbody = document.querySelector('.message-list tbody');
  if (!tbody || typeof Sortable === 'undefined') return;

  // Make message rows draggable (but not sortable within the list).
  Sortable.create(tbody, {
    group: { name: 'messages', pull: 'clone', put: false },
    sort: false,
    ghostClass: 'sortable-ghost',
    filter: 'input[type="checkbox"], .flag-star',
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

// Re-init drag-drop after HTMX swaps.
document.addEventListener('htmx:afterSettle', initDragDrop);

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
      ghost.style.height = block.style.height;
      col.appendChild(ghost);

      const onMouseMove = (ev) => {
        const relY = ev.clientY - colRect.top;
        // Snap to 30-min increments.
        const halfHours = Math.round(relY / (hourHeight / 2));
        const snappedY = halfHours * (hourHeight / 2);
        const pct = (snappedY / colRect.height) * 100;
        ghost.style.top = Math.max(0, Math.min(pct, 100 - parseFloat(block.style.height))) + '%';
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

// Flash message auto-dismiss + init drag-drop
document.addEventListener('DOMContentLoaded', () => {
  initDragDrop();
  initEventDrag();
  document.querySelectorAll('.flash').forEach(el => {
    setTimeout(() => {
      el.style.transition = 'opacity 0.3s';
      el.style.opacity = '0';
      setTimeout(() => el.remove(), 300);
    }, 5000);
  });
});
