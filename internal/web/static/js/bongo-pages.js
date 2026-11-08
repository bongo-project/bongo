// Bongo page-specific initialisers and shared HTMX lifecycle hooks.

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
      var ids = Array.from(el.querySelectorAll(':scope > [data-id]')).map(function(r) { return r.dataset.id; });
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

var pendingContactReveal = null;

// Picking a vCard or ICS file submits the import form. Without JS, the contacts
// form keeps a visually hidden submit and the calendar form shows one in <noscript>.
document.addEventListener('change', function(e) {
  var input = e.target;
  if (!input || !input.matches || !input.matches('.contacts-head input[name="vcf"], .calendar-sidebar-form input[name="ics"]')) return;
  if (!input.files || input.files.length === 0 || !input.form) return;
  if (input.form.requestSubmit) input.form.requestSubmit();
});

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

  var editToggle = e.target.closest('[data-contact-edit-toggle]');
  if (editToggle) {
    var card = editToggle.closest('.contact-detail-card');
    var editor = card ? card.querySelector('.contact-editor') : null;
    if (!editor) return;
    editor.toggleAttribute('hidden');
    editToggle.setAttribute('aria-expanded', String(!editor.hasAttribute('hidden')));
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
    if (window.matchMedia && window.matchMedia('(max-width: 768px)').matches) {
      if (pendingContactReveal) document.body.removeEventListener('htmx:afterSettle', pendingContactReveal);
      pendingContactReveal = function revealContactDetail(ev) {
        if (!ev.target || ev.target.id !== 'contact-detail') return;
        document.body.removeEventListener('htmx:afterSettle', revealContactDetail);
        pendingContactReveal = null;
        ev.target.scrollIntoView({block: 'start'});
      };
      document.body.addEventListener('htmx:afterSettle', pendingContactReveal);
    }
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
        if (block.dataset.owner) form.set('owner', block.dataset.owner);

        fetch('/calendar/event/' + guid + '/reschedule', {
          method: 'POST',
          body: form
        }).then(resp => {
          if (resp.ok) {
            // Some invitations were not delivered: reload the view with the
            // warning the server describes.
            var warn = resp.headers.get('X-Invites-Query');
            if (warn) {
              var target = new URL(window.location.href);
              new URLSearchParams(warn).forEach(function(v, k) { target.searchParams.set(k, v); });
              window.location.assign(target.toString());
              return;
            }
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
  var row = btn.closest('#sortable-rules > [data-id]');
  if (!row) return;
  var dir = btn.dataset.moveDir;
  var sibling = dir === 'up' ? row.previousElementSibling : row.nextElementSibling;
  if (!sibling || !sibling.dataset.id) return;
  if (dir === 'up') {
    row.parentNode.insertBefore(row, sibling);
  } else {
    row.parentNode.insertBefore(sibling, row);
  }
  var ids = Array.from(row.parentNode.querySelectorAll(':scope > [data-id]')).map(function(r) { return r.dataset.id; });
  fetch('/settings/rules/reorder', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(ids)})
    .then(function(resp) {
      if (!resp.ok) htmx.ajax('GET', window.location.pathname, '#main-content');
    });
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

function initCalendarScroll(root) {
  (root || document).querySelectorAll('[data-calendar-scroll]').forEach(function(grid) {
    if (grid.dataset.scrollReady === 'true') return;
    grid.dataset.scrollReady = 'true';
    var hour = grid.querySelector('.day-column.today') ? new Date().getHours() : 8;
    var row = grid.querySelector('.hour-row[data-hour="' + Math.max(0, hour - 1) + '"]');
    if (row) grid.scrollTop = row.offsetTop;
  });
}

function initCalendarDynamicStyles(root) {
  return root;
}

// Flash message auto-dismiss and page-level component initialisation.
document.addEventListener('DOMContentLoaded', () => {
  syncAdminLayoutClass();
  initDragDrop();
  initEventDrag();
  initRulesSortable();
  initAdminLogViewer();
  initContactsPage();
  updateSidebarActive();
  initCalendarScroll(document);
  document.querySelectorAll('.flash').forEach(el => {
    setTimeout(() => {
      el.classList.add('flash-dismissing');
      setTimeout(() => el.remove(), 300);
    }, 5000);
  });
});

// Update page-level components after HTMX navigation.
document.addEventListener('htmx:pushedIntoHistory', updateSidebarActive);
document.addEventListener('htmx:afterSettle', function() {
  syncAdminLayoutClass();
  updateSidebarActive();
  initRulesSortable();
  initAdminLogViewer();
  initContactsPage();
  initCalendarScroll(document);
});

// htmx does not swap a 4xx/5xx response, so a form that names a status area
// (data-form-status as its hx-target) would show a failure as nothing at all.
// Put the response text, which the server translates, into that area.
document.addEventListener('htmx:responseError', function (evt) {
  var area = evt.detail && evt.detail.target;
  var text = (evt.detail.xhr && evt.detail.xhr.responseText || '').trim();
  if (!area || !area.hasAttribute || !area.hasAttribute('data-form-status')) {
    showClientStatus(text, 'error');
    return;
  }
  var msg = document.createElement('div');
  msg.className = 'flash flash-error';
  msg.setAttribute('role', 'alert');
  msg.textContent = text || i18n('js.actionFailed');
  area.replaceChildren(msg);
});

document.addEventListener('htmx:sendError', function () {
  showClientStatus('', 'error');
});
