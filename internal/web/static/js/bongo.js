// Bongo client core: translation, status, CSRF and shared geometry helpers.

// Geometry helpers are shared by Alpine components and page initialisers.
function replacePrefixedClass(el, prefix, nextClass) {
  if (!el) return;
  Array.from(el.classList).forEach(function(cls) {
    if (cls.indexOf(prefix) === 0) el.classList.remove(cls);
  });
  if (nextClass) el.classList.add(nextClass);
}

function calendarTopClass(minutes) {
  var clamped = Math.max(0, Math.min(1439, minutes));
  return 'cal-top-' + clamped;
}

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

var clientStatusTimer = null;

// showClientStatus gives every client-side mutation a shared, accessible
// failure surface. Server text is already localized; the fallback covers
// network errors and empty responses.
function showClientStatus(message, level) {
  var el = document.getElementById('client-status');
  if (!el) return;
  clearTimeout(clientStatusTimer);
  el.textContent = String(message || i18n('js.actionFailed')).trim().slice(0, 300);
  el.className = 'client-status client-status-' + (level || 'error');
  el.hidden = false;
  clientStatusTimer = setTimeout(function() { el.hidden = true; }, 6000);
}

// Fetch callers retain their normal response handling, while failed mutations
// can no longer disappear silently. Read failures stay local to their widgets.
(function () {
  var origFetch = window.fetch;
  window.fetch = function(input, init) {
    var method = ((init && init.method) || (typeof input === 'object' && input.method) || 'GET').toUpperCase();
    var result = origFetch.apply(this, arguments);
    if (method === 'GET' || method === 'HEAD') return result;
    result.then(function(resp) {
      if (resp.ok) return;
      resp.clone().text()
        .then(function(body) { showClientStatus(body || i18n('js.actionFailed'), 'error'); })
        .catch(function() { showClientStatus('', 'error'); });
    }).catch(function() { showClientStatus('', 'error'); });
    return result;
  };
})();

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

// Live sidebar unread counts. After any mail request, HTMX or fetch (marking
// read, opening a message, moving, deleting, spam), and on new mail from the
// SSE stream, re-read /api/mail/unread-counts and update every
// [data-unread-for] count and the Unread view badge. Debounced, so a bulk
// action that fires several requests refreshes once.
(function () {
  var timer = null;
  var origFetch = window.fetch;

  function isMailPath(url) {
    try {
      var p = new URL(url, window.location.origin);
      return p.origin === window.location.origin && p.pathname.indexOf('/mail/') === 0;
    } catch (e) {
      return false;
    }
  }

  function apply(data) {
    var counts = (data && data.mailboxes) || {};
    document.querySelectorAll('[data-unread-for]').forEach(function (el) {
      var n = counts[el.dataset.unreadFor] || 0;
      el.textContent = n > 0 ? String(n) : '';
      el.toggleAttribute('hidden', n === 0);
      el.setAttribute('aria-label', (el.dataset.unreadLabel || '%d').replace('%d', n));
    });
    var badge = document.getElementById('unread-badge');
    if (badge) badge.textContent = data && data.inbox > 0 ? String(data.inbox) : '';
  }

  function refresh() {
    timer = null;
    if (!document.querySelector('[data-unread-for], #unread-badge')) return;
    origFetch.call(window, '/api/mail/unread-counts', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) { if (data) apply(data); })
      .catch(function () { /* the next action retries */ });
  }

  function schedule() {
    if (timer) clearTimeout(timer);
    timer = setTimeout(refresh, 250);
  }
  window.refreshUnreadCounts = schedule;

  var wrapped = window.fetch;
  window.fetch = function (input, init) {
    var url = (typeof input === 'string') ? input : (input && input.url) || '';
    var p = wrapped.apply(this, arguments);
    if (isMailPath(url)) {
      p.then(schedule, function () { /* failed request changed nothing */ });
    }
    return p;
  };

  document.addEventListener('htmx:afterRequest', function (evt) {
    var path = evt.detail && evt.detail.pathInfo && evt.detail.pathInfo.requestPath;
    if (path && isMailPath(path)) schedule();
  });
  document.addEventListener('DOMContentLoaded', schedule);
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
    // First child, so a multipart body carries _csrf as its first part (the
    // server only peeks at the start of an upload).
    form.insertBefore(input, form.firstChild);
  }, true);
})();
