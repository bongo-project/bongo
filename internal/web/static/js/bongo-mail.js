// Bongo mailbox, reading pane and keyboard interactions.

// In the pane modes j and k also open the highlighted message, after a pause
// so that holding the key skims the list without opening, and marking read,
// every message on the way.
const PANE_OPEN_DELAY_MS = 300;
let paneOpenTimer = null;

// readingPaneVisible reports whether messages open in the reading pane: the
// layout is two-pane or three-pane and the screen is wide enough to show it.
function readingPaneVisible() {
  const pane = document.getElementById('reading-pane');
  return !!pane && !pane.hidden && pane.offsetParent !== null;
}

// Reading pane: intercept message link clicks in pane modes. The listener runs
// in the capture phase because the link also carries hx-get, and htmx's own
// handler on the link would otherwise replace the whole view before a
// bubbling listener here ever saw the click. The pane's visibility decides,
// not localStorage: the layout may come from server preferences, and on
// narrow screens CSS hides the pane in every mode.
document.addEventListener('click', (e) => {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const link = e.target.closest('a[data-pane-url]');
  if (link) {
    if (!readingPaneVisible()) return;
    e.preventDefault();
    e.stopPropagation();
    openInPane(link.closest('.msg-row'));
    return;
  }
  // Only sender and subject are links, but the whole row highlights, so a
  // click anywhere else on it opens the message too. The row's own controls
  // keep their actions, and a click that ends a text selection is left alone.
  const row = e.target.closest('.msg-list .msg-row[data-uid]');
  if (!row || e.target.closest('a, button, input, label, [hx-get], [hx-post]')) return;
  if (String(window.getSelection())) return;
  if (readingPaneVisible() && row.querySelector('a[data-pane-url]')) {
    openInPane(row);
    return;
  }
  const rowLink = row.querySelector('.msg-main a');
  if (rowLink) rowLink.click();
}, true);

// openInPane loads a row's message into the reading pane and moves the
// keyboard highlight to it, so j and k carry on from the message being read.
function openInPane(row) {
  clearTimeout(paneOpenTimer);
  const link = row && row.querySelector('a[data-pane-url]');
  if (!link) return;
  htmx.ajax('GET', link.dataset.paneUrl, '#reading-pane');
  markOpenRow(row);
  setKeyboardFocus(row);
}

// Highlight the row whose message is open in the reading pane.
function markOpenRow(row) {
  document.querySelectorAll('.msg-row.is-open').forEach(function(r) {
    r.classList.remove('is-open');
    r.removeAttribute('aria-current');
  });
  if (row) {
    row.classList.add('is-open');
    row.setAttribute('aria-current', 'true');
  }
}

function setKeyboardFocus(row) {
  document.querySelectorAll('.msg-list .msg-row.keyboard-focus').forEach(function(r) {
    r.classList.remove('keyboard-focus');
  });
  row.classList.add('keyboard-focus');
  row.scrollIntoView({ block: 'nearest' });
}

function moveKeyboardFocus(row) {
  setKeyboardFocus(row);
  clearTimeout(paneOpenTimer);
  if (!readingPaneVisible()) return;
  paneOpenTimer = setTimeout(function() {
    if (row.isConnected && !row.classList.contains('is-open')) openInPane(row);
  }, PANE_OPEN_DELAY_MS);
}

// currentMailbox returns the mailbox whose list is on screen. It is read from
// the list itself: #main-content's data-selected-mailbox is rendered once per
// full page load and keeps the old name after the sidebar swaps in another
// mailbox, which sent actions to the right UID in the wrong mailbox.
function currentMailbox() {
  const view = document.querySelector('.mail-view[data-mailbox]');
  return view ? view.dataset.mailbox : null;
}

// listReloadURL is the URL that reloads the list on screen. A filtered list
// (To me, unread only) says so in data-list-url, so a reload after an action
// keeps the filter. Any other list reloads from its mailbox name.
function listReloadURL(mailbox) {
  const view = document.querySelector('.mail-view[data-mailbox]');
  if (view && view.dataset.mailbox === mailbox && view.dataset.listUrl) {
    return view.dataset.listUrl;
  }
  return '/mail/' + encodeURIComponent(mailbox);
}

// bulkURL is where a bulk action on the list on screen is posted. A header
// view (To me, From my contacts, Mailing lists) names its own in data-bulk-url,
// because its mailbox is only a navigation key and a folder may be called
// "to-me"; a folder, and a virtual mailbox such as All Mail, posts to its own
// URL.
function bulkURL(mailbox) {
  const view = document.querySelector('.mail-view[data-mailbox]');
  if (view && view.dataset.mailbox === mailbox && view.dataset.bulkUrl) {
    return view.dataset.bulkUrl;
  }
  return '/mail/' + encodeURIComponent(mailbox) + '/bulk';
}

// actionMailbox is the mailbox an action on a row must target. In a list that
// spans mailboxes (To me, From my contacts, Mailing lists, All Mail, Unread,
// Starred) a row carries data-sel-key and the list mailbox is only a view
// name, so the message's real mailbox is used. Everywhere else the list
// mailbox is returned unchanged.
function actionMailbox(row, mailbox) {
  return (row && row.dataset.selKey && row.dataset.realMailbox) || mailbox;
}

// selectionKey identifies a row in the bulk selection. UIDs are only unique
// within one mailbox, so a list that spans mailboxes keys a row by
// "uid:mailbox".
function selectionKey(row) {
  return row.dataset.selKey || String(row.dataset.uid);
}

// addSelectionToParams adds a selection key to a bulk request: a plain UID
// under "uids", a "uid:mailbox" pair under "targets".
function addSelectionToParams(params, key) {
  params.append(key.indexOf(':') > 0 ? 'targets' : 'uids', key);
}

// selectionMessageId turns a selection key into a {mailbox, uid} pair for the
// bulk tag endpoint.
function selectionMessageId(key, mailbox) {
  const i = key.indexOf(':');
  if (i > 0) return { mailbox: key.slice(i + 1), uid: parseInt(key.slice(0, i), 10) };
  return { mailbox: mailbox, uid: parseInt(key, 10) };
}

// deleteRow moves a message to Trash, reloads the list and highlights the
// message that took its place. Trash is skipped: deleting there is permanent,
// which is too much for a single key.
function deleteRow(row, idx) {
  const mailbox = currentMailbox();
  const uid = row.dataset.uid;
  if (!mailbox || !uid || mailbox === 'Trash') return;
  const params = new URLSearchParams();
  params.set('action', 'delete');
  params.append('uids', uid);
  const url = listReloadURL(mailbox);
  fetch('/mail/' + encodeURIComponent(actionMailbox(row, mailbox)) + '/bulk', { method: 'POST', body: params })
    .then(resp => {
      if (!resp.ok) return;
      return htmx.ajax('GET', url, '#main-content').then(() => {
        const rows = document.querySelectorAll('.msg-list .msg-row[data-uid]');
        if (rows.length > 0) moveKeyboardFocus(rows[Math.min(idx, rows.length - 1)]);
      });
    })
    .catch(() => {});
}

function toggleReadRow(row, idx) {
  const mailbox = currentMailbox();
  if (!mailbox || !row) return;
  const markRead = row.classList.contains('unread');
  const params = new URLSearchParams();
  params.set('action', markRead ? 'mark-read' : 'mark-unread');
  addSelectionToParams(params, selectionKey(row));
  fetch(bulkURL(mailbox), { method: 'POST', body: params })
    .then(function(resp) {
      if (!resp.ok) return;
      const view = document.querySelector('.mail-view[data-list-url]');
      const unreadOnly = mailbox === 'unread' || (view && view.dataset.listUrl.indexOf('unread=1') !== -1);
      if (markRead && unreadOnly) {
        const rows = Array.from(document.querySelectorAll('.msg-list .msg-row[data-uid]'));
        const next = rows[idx + 1] || rows[idx - 1];
        row.remove();
        if (next) moveKeyboardFocus(next);
      } else {
        row.classList.toggle('unread', !markRead);
      }
      if (window.refreshUnreadCounts) window.refreshUnreadCounts();
    });
}

// archiveRow archives a message from the list (unarchives it when the list is
// Archive), reloads the list and highlights the message that took its place.
function archiveRow(row, idx) {
  const mailbox = currentMailbox();
  const uid = row.dataset.uid;
  if (!mailbox || !uid) return;
  // The row knows its real mailbox; the list mailbox is only right for a real
  // folder, not for a virtual view such as Starred.
  const real = row.dataset.realMailbox || mailbox;
  const verb = real === 'Archive' ? 'unarchive' : 'archive';
  const url = listReloadURL(mailbox);
  fetch('/mail/' + encodeURIComponent(real) + '/' + uid + '/' + verb, { method: 'POST' })
    .then(resp => {
      if (!resp.ok) return;
      return htmx.ajax('GET', url, '#main-content').then(() => {
        const rows = document.querySelectorAll('.msg-list .msg-row[data-uid]');
        if (rows.length > 0) moveKeyboardFocus(rows[Math.min(idx, rows.length - 1)]);
      });
    })
    .catch(() => {});
}

// archiveOpenMessage clicks the archive (or unarchive) button of the message
// being read, in the full view or the reading pane, so htmx, the CSRF header
// and the redirect are reused. A hidden reading pane can still hold a message
// loaded earlier, so only a visible button counts; otherwise the shortcut
// falls through to the focused row. It reports whether a button was clicked.
function archiveOpenMessage() {
  const btns = document.querySelectorAll('#main-content button[data-action="archive"], #main-content button[data-action="unarchive"]');
  for (const btn of btns) {
    if (btn.offsetParent !== null) {
      btn.click();
      return true;
    }
  }
  return false;
}

// Keyboard shortcut help modal
var shortcutModalOpen = false;
var shortcutModalReturnFocus = null;

var keyboardShortcuts = [
  { action: 'next', keys: ['j'], display: 'j', label: 'js.shortcutNextMessage' },
  { action: 'previous', keys: ['k'], display: 'k', label: 'js.shortcutPrevMessage' },
  { action: 'open', keys: ['Enter'], display: 'Enter', label: 'js.shortcutOpenMessage' },
  { action: 'reply', keys: ['r'], display: 'r', label: 'js.shortcutReply' },
  { action: 'delete', keys: ['d', '#', 'Delete'], display: 'd / # / Delete', label: 'js.shortcutDelete' },
  { action: 'archive', keys: ['e'], display: 'e', label: 'js.shortcutArchive' },
  { action: 'toggle-read', keys: ['u'], display: 'u', label: 'js.shortcutToggleRead' },
  { action: 'toggle-star', keys: ['s'], display: 's', label: 'js.shortcutToggleStar' },
  { action: 'search', keys: ['/'], display: '/', label: 'js.shortcutFocusSearch' },
  { action: 'search-global', keys: [], display: 'Ctrl/\u2318 K', label: 'js.shortcutFocusSearchK' },
  { action: 'compose', keys: ['c'], display: 'c', label: 'js.shortcutCompose' },
  { action: 'close', keys: [], display: 'Esc', label: 'js.shortcutCloseModal' },
  { action: 'help', keys: ['?'], display: '?', label: 'js.shortcutShowHelp' }
];

function keyboardShortcutAction(key) {
  const shortcut = keyboardShortcuts.find(function(item) { return item.keys.indexOf(key) !== -1; });
  return shortcut ? shortcut.action : '';
}

function keyboardShortcutHelp() {
  return keyboardShortcuts.map(function(item) {
    return '<dt><kbd>' + item.display + '</kbd></dt><dd>' + i18n(item.label) + '</dd>';
  }).join('');
}

function clickRowControl(row, selector) {
  if (!row) return;
  const control = row.querySelector(selector);
  if (control) control.click();
}

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
        '<dl>' + keyboardShortcutHelp() + '</dl>' +
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
    shortcutModalReturnFocus = document.activeElement;
    overlay.hidden = false;
    // Focus trap: focus the close button.
    document.getElementById('shortcut-modal-close').focus();
  } else {
    overlay.hidden = true;
    if (shortcutModalReturnFocus && shortcutModalReturnFocus.isConnected) shortcutModalReturnFocus.focus();
    shortcutModalReturnFocus = null;
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

// focusGlobalSearch moves focus to the top bar search input. It reports
// whether an input was found.
function focusGlobalSearch() {
  var input = document.getElementById('global-search');
  if (!input) return false;
  input.focus();
  input.select();
  return true;
}

// isTypingTarget reports whether keystrokes on el belong to a text field.
function isTypingTarget(el) {
  if (!el) return false;
  var tag = el.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || !!el.isContentEditable;
}

// Ctrl-K / Cmd-K focuses search. Registered separately because the shortcut
// listener below bails out on any modifier. It is ignored while typing so the
// composer and browser bindings keep their own meaning for the combination.
document.addEventListener('keydown', function(e) {
  if (shortcutModalOpen) return;
  if (e.altKey || e.shiftKey || e.ctrlKey === e.metaKey) return;
  if (String(e.key).toLowerCase() !== 'k') return;
  if (isTypingTarget(e.target)) return;
  if (focusGlobalSearch()) e.preventDefault();
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

  const shortcutAction = keyboardShortcutAction(e.key);

  if (shortcutAction === 'help') {
    e.preventDefault();
    toggleShortcutModal();
    return;
  }

  if (shortcutAction === 'compose') {
    e.preventDefault();
    window.location.href = '/compose';
    return;
  }

  if (shortcutAction === 'search') {
    if (focusGlobalSearch()) e.preventDefault();
    return;
  }

  // e: archive the open message. This sits before the empty-list return
  // because the full message view has no list rows. With no message open it
  // falls through to the keyboard-focused row below.
  if (shortcutAction === 'archive' && archiveOpenMessage()) {
    e.preventDefault();
    return;
  }

  const messageRows = document.querySelectorAll('.msg-list .msg-row[data-uid]');
  if (messageRows.length === 0) return;

  const current = document.querySelector('.msg-list .msg-row[data-uid].keyboard-focus');
  let idx = current ? Array.from(messageRows).indexOf(current) : -1;

  switch (shortcutAction) {
    case 'next':
      e.preventDefault();
      if (idx < messageRows.length - 1) moveKeyboardFocus(messageRows[idx + 1]);
      break;

    case 'previous':
      e.preventDefault();
      if (idx > 0) moveKeyboardFocus(messageRows[idx - 1]);
      break;

    case 'open':
      if (current) {
        e.preventDefault();
        clickRowControl(current, 'a[data-message-link]');
      }
      break;

    case 'reply':
      if (current) {
        e.preventDefault();
        const uid = current.dataset.uid;
        const mailbox = actionMailbox(current, currentMailbox());
        if (uid && mailbox) {
          window.location.href = '/compose/reply/' + encodeURIComponent(mailbox) + '/' + uid;
        }
      }
      break;

    case 'delete':
      if (current) {
        e.preventDefault();
        deleteRow(current, idx);
      }
      break;

    case 'toggle-read':
      if (current) {
        e.preventDefault();
        toggleReadRow(current, idx);
      }
      break;

    case 'archive':
      if (current) {
        e.preventDefault();
        archiveRow(current, idx);
      }
      break;

    case 'toggle-star':
      if (current) {
        e.preventDefault();
        clickRowControl(current, '.msg-star');
      }
      break;

  }
});

// Focus management after HTMX content swaps
document.body.addEventListener('htmx:afterSwap', function(evt) {
  var target = evt.detail.target;
  if (!target || target.id !== 'main-content') return;
  var focusable = target.querySelector('[autofocus], h1, h2, h3');
  if (focusable) {
    if (!focusable.hasAttribute('tabindex')) focusable.setAttribute('tabindex', '-1');
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
      const mailbox = currentMailbox();
      if (mailbox) row.dataset.srcMailbox = actionMailbox(row, mailbox);
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
            const row = document.querySelector('.msg-list .msg-row[data-uid="' + uid + '"]');
            if (row) row.remove();
          }
        });
      }
    });
  });
}
