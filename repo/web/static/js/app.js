/**
 * District Materials Portal — app.js
 * Minimal vanilla JS utilities. No frameworks beyond HTMX + Alpine.
 */

(function () {
  'use strict';

  /* ------------------------------------------------------------------
     Toast: auto-hide Bootstrap toasts after 4 seconds.
     Watches #toast-container for new .toast elements added via
     hx-swap-oob and initialises Bootstrap's Toast + schedules hide.
  ------------------------------------------------------------------ */
  function initToastObserver() {
    var container = document.getElementById('toast-container');
    if (!container) return;

    function activateToasts(root) {
      root.querySelectorAll('.toast.show').forEach(function (el) {
        // Bootstrap Toast API (available when bootstrap.bundle is loaded)
        if (window.bootstrap && window.bootstrap.Toast) {
          var bsToast = window.bootstrap.Toast.getOrCreateInstance(el, { autohide: true, delay: 4000 });
          bsToast.show();
        } else {
          // Fallback: remove after 4s if Bootstrap JS is absent
          setTimeout(function () { el.remove(); }, 4000);
        }
      });
    }

    // Handle toasts already in the DOM at load time.
    activateToasts(container);

    // Watch for hx-swap-oob replacements.
    var observer = new MutationObserver(function (mutations) {
      mutations.forEach(function (m) {
        m.addedNodes.forEach(function (node) {
          if (node.nodeType !== 1) return;
          activateToasts(node.querySelectorAll ? node : container);
        });
      });
    });

    observer.observe(container, { childList: true, subtree: true });
  }

  /* ------------------------------------------------------------------
     HTMX: handle non-200 responses gracefully
  ------------------------------------------------------------------ */
  function initHtmxResponseHandler() {
    document.body.addEventListener('htmx:afterRequest', function (evt) {
      var detail = evt.detail;
      if (!detail) return;

      var xhr = detail.xhr;
      if (!xhr) return;

      // Ignore successful responses and redirects.
      if (xhr.status >= 200 && xhr.status < 300) return;
      if (xhr.status === 0) return; // network error handled below

      // Show a generic error toast for unexpected server errors.
      if (xhr.status >= 500) {
        showToast('A server error occurred. Please try again.', 'error');
      }
    });

    document.body.addEventListener('htmx:sendError', function () {
      showToast('Network error. Please check your connection.', 'error');
    });
  }

  /* ------------------------------------------------------------------
     showToast(message, type)
     Types: 'success' | 'error' | 'info' | 'warning'
     Updates #toast content; the MutationObserver auto-schedules hide.
  ------------------------------------------------------------------ */
  window.showToast = function (message, type) {
    var container = document.getElementById('toast-container');
    if (!container) return;

    var bgClass = 'bg-primary';
    if (type === 'success') bgClass = 'text-bg-success';
    else if (type === 'error') bgClass = 'text-bg-danger';
    else if (type === 'warning') bgClass = 'text-bg-warning';
    else bgClass = 'text-bg-primary';

    var el = document.createElement('div');
    el.className = 'toast show align-items-center ' + bgClass + ' border-0';
    el.setAttribute('role', 'alert');
    el.innerHTML =
      '<div class="d-flex">' +
        '<div class="toast-body">' + escapeHtml(message) + '</div>' +
        '<button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast" aria-label="Close"></button>' +
      '</div>';

    container.appendChild(el);

    if (window.bootstrap && window.bootstrap.Toast) {
      var bsToast = window.bootstrap.Toast.getOrCreateInstance(el, { autohide: true, delay: 4000 });
      bsToast.show();
    } else {
      setTimeout(function () { el.remove(); }, 4000);
    }
  };

  /* ------------------------------------------------------------------
     confirmAction(message) → bool
     Thin wrapper around window.confirm for use in hx-on or onclick.
  ------------------------------------------------------------------ */
  window.confirmAction = function (message) {
    return window.confirm(message || 'Are you sure?');
  };

  /* ------------------------------------------------------------------
     escapeHtml — prevent XSS in showToast
  ------------------------------------------------------------------ */
  function escapeHtml(str) {
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  /* ------------------------------------------------------------------
     Comment collapse / expand
  ------------------------------------------------------------------ */
  function initCommentCollapse() {
    document.body.addEventListener('click', function (e) {
      var btn = e.target.closest('.comment-toggle');
      if (!btn) return;

      var body = btn.previousElementSibling;
      if (!body) return;

      if (body.classList.contains('collapsed')) {
        body.classList.remove('collapsed');
        btn.textContent = 'Show less';
      } else {
        body.classList.add('collapsed');
        btn.textContent = 'Show more';
      }
    });
  }

  /* ------------------------------------------------------------------
     Boot
  ------------------------------------------------------------------ */
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }

  function boot() {
    initToastObserver();
    initHtmxResponseHandler();
    initCommentCollapse();
  }

}());

// ── HTMX global error handler ──────────────────────────────
// For any HTMX request that returns a non-2xx response and the
// response body is empty (or the server didn't return an error_inline
// partial), inject a fallback error message into the hx-target.
document.body.addEventListener('htmx:responseError', function (evt) {
  var xhr    = evt.detail.xhr;
  var target = evt.detail.target;
  if (!target) return;
  // If the server already sent an error_inline partial, it will have
  // been swapped in automatically — don't double-render.
  if (target.querySelector('.alert-danger, .error-inline')) return;
  var status = xhr.status;
  var msg = 'Something went wrong. Please try again.';
  if (status === 401) msg = 'Your session has expired. Please log in again.';
  if (status === 403) msg = 'You do not have permission to perform this action.';
  if (status === 429) msg = 'Too many requests. Please wait a moment before trying again.';
  target.insertAdjacentHTML('afterbegin',
    '<div class="alert alert-danger d-flex align-items-center gap-2 py-2 error-inline" role="alert">' +
      '<i class="bi bi-exclamation-triangle-fill flex-shrink-0"></i>' +
      '<span>' + msg + '</span>' +
    '</div>'
  );
});

// Auto-dismiss .error-inline elements added to the DOM.
(function () {
  function scheduleDismiss(el) {
    setTimeout(function () {
      el.style.transition = 'opacity 0.4s';
      el.style.opacity = '0';
      setTimeout(function () { el.remove(); }, 400);
    }, 6000);
  }
  // Elements present at load time.
  document.querySelectorAll('.error-inline').forEach(scheduleDismiss);
  // Elements injected later (HTMX swaps).
  var obs = new MutationObserver(function (mutations) {
    mutations.forEach(function (m) {
      m.addedNodes.forEach(function (node) {
        if (node.nodeType !== 1) return;
        if (node.classList && node.classList.contains('error-inline')) scheduleDismiss(node);
        node.querySelectorAll && node.querySelectorAll('.error-inline').forEach(scheduleDismiss);
      });
    });
  });
  obs.observe(document.body, { childList: true, subtree: true });
})();
