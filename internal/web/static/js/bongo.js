// Bongo-Go client-side JavaScript (Alpine.js components + keyboard shortcuts)

document.addEventListener('alpine:init', () => {
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
    }
  }));
});

// Keyboard shortcuts (only when not in an input/textarea)
document.addEventListener('keydown', (e) => {
  const tag = e.target.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.target.isContentEditable) {
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

// Flash message auto-dismiss
document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('.flash').forEach(el => {
    setTimeout(() => {
      el.style.transition = 'opacity 0.3s';
      el.style.opacity = '0';
      setTimeout(() => el.remove(), 300);
    }, 5000);
  });
});
