// chat.js — wires the chat form's loading-state UX without
// using htmx's hx-on::* attributes.
//
// Why this file exists
// --------------------
// The chat landing page (rendered by the chatFallbackBody
// template in fallback_renderers.go) used to carry three
// hx-on::* attributes:
//
//   hx-on::before-request="…chat-send…add('is-loading')"
//   hx-on::after-request="…reset()…remove('is-loading')…focus()"
//   hx-on::response-error="…chat-send…remove('is-loading')"
//
// htmx processes those attributes by calling eval() / new
// Function() at runtime. The strict CSP (script-src 'self',
// no 'unsafe-inline', no 'unsafe-eval') blocks those calls,
// and the strict mode also disables hx-on via the
// `<meta name="htmx-config" content='{"allowEval":false}'>`
// tag in the chat page. Disabling eval is the right default —
// inline JavaScript in attributes is a known XSS vector — so
// instead of relying on hx-on we install the same behavior
// via three event listeners on the chat form.
//
// All three listeners are delegated to the form element
// directly so the script is order-independent from the
// htmx runtime: as long as chat.js runs after the form
// exists in the DOM, the listeners attach before any user
// submit triggers them. We use the `defer` attribute on the
// `<script>` tag to guarantee DOM-parse order.

(function () {
  'use strict';

  // ----------------------------------------------------------------
  // Toast notification helper (Phase 1.1 of plans/ux-overhaul.md).
  // ----------------------------------------------------------------
  // showToast(message, type, ttlMs) appends a daisyUI alert
  // into the page's #toast-container. The container is
  // rendered empty by every full page; this helper is the
  // only consumer. Toasts auto-dismiss after ttlMs (default
  // 4000ms; matches daisyUI's docs and every other product
  // a new operator has used).
  //
  // type is one of 'success', 'error', 'warning', 'info' —
  // the daisyUI alert-{type} variants. Invalid types fall
  // back to 'info' rather than throwing, so a forgotten
  // enum value renders a quiet informational toast instead
  // of breaking the form.
  //
  // The helper is exposed on window so per-page handlers
  // (Phase 4 documents delete, Phase 5 login error, etc.)
  // can call it without needing their own wiring.
  function showToast(message, type, ttlMs) {
    var container = document.getElementById('toast-container');
    if (!container) {
      // The page forgot to render the container (login
      // page in Phase 5, before its wire-up lands).
      // Fail quiet rather than throw — the user's
      // primary action is what matters; a missing toast
      // is a degraded UX, not a broken page.
      return;
    }

    // Defensive default: the daisyUI docs use 4000ms;
    // any product a new operator has used (GitHub,
    // GitLab, Linear, Slack) uses the same. A TTL of
    // 0 would render the toast for one frame and
    // dismiss it, which is the bug we're avoiding.
    var DEFAULT_TTL_MS = 4000;
    var ttl = typeof ttlMs === 'number' && ttlMs > 0 ? ttlMs : DEFAULT_TTL_MS;

    // Whitelist the type so an attacker who controls
    // the input (a future server-rendered error
    // message) can't smuggle in arbitrary class names.
    var ALLOWED = ['success', 'error', 'warning', 'info'];
    var safeType = ALLOWED.indexOf(type) === -1 ? 'info' : type;

    var alert = document.createElement('div');
    alert.className = 'alert alert-' + safeType;
    alert.setAttribute('role', 'status');
    // textContent (not innerHTML) so the message is
    // escaped — defense against the same XSS that the
    // html/template auto-escape buys us on the server.
    alert.textContent = String(message);
    container.appendChild(alert);

    setTimeout(function () {
      // Remove the node after the TTL. If the user
      // dismissed it manually (Phase 6 wires a
      // close button on the alert), the parent may
      // already not contain the node — that's a
      // no-op rather than an error.
      if (alert.parentNode) {
        alert.parentNode.removeChild(alert);
      }
    }, ttl);
  }

  // Expose showToast on window for the per-page callers.
  // The window.* prefix makes it clear in DevTools and
  // matches the same pattern as the openModal/closeModal
  // helpers added in Phase 1.2.
  window.showToast = showToast;

  // ----------------------------------------------------------------
  // Modal helpers (Phase 1.2 of plans/ux-overhaul.md).
  // ----------------------------------------------------------------
  // openModal(id) and closeModal(id) wrap the native
  // <dialog>.showModal() / <dialog>.close() browser APIs
  // so per-page flows (Phase 4 documents delete
  // confirmation, future flows) can open a daisyUI modal
  // with a one-liner rather than re-implementing the
  // lookup + showModal / close calls in every consumer.
  //
  // Pattern:
  //
  //   <button data-modal-open="confirm-delete-{{.ID}}">Delete</button>
  //   <dialog id="confirm-delete-{{.ID}}" class="modal">
  //     <div class="modal-box">…</div>
  //   </dialog>
  //
  //   // in chat.js (this file)
  //   document.addEventListener('click', function (e) {
  //     var trigger = e.target.closest('[data-modal-open]');
  //     if (trigger) {
  //       openModal(trigger.getAttribute('data-modal-open'));
  //     }
  //   });
  //
  // The <dialog> element is the recommended daisyUI modal
  // pattern (SKILL.md) over the popover API for two
  // reasons: (a) the browser handles focus management and
  // ESC-to-close for free, and (b) the daisyUI
  // .modal-box / .modal-action / .modal-backdrop
  // classes are all designed around the <dialog>
  // element. Phase 4 (documents delete) is the first
  // real consumer.
  function openModal(id) {
    var dlg = document.getElementById(id);
    if (!dlg) {
      // Fail quiet rather than throw — a missing
      // modal is a developer error, but the user's
      // primary action (the click) already fired
      // and the page should not break.
      return;
    }
    // showModal() opens the dialog as a modal —
    // backdrop, focus trap, ESC-to-close. The daisyUI
    // CSS keyframes fade the modal-box in on open.
    if (typeof dlg.showModal === 'function') {
      dlg.showModal();
    }
  }

  function closeModal(id) {
    var dlg = document.getElementById(id);
    if (!dlg) {
      return;
    }
    // close() dismisses the dialog and fires a
    // 'close' event. The form inside the modal
    // can also dismiss itself by submitting with
    // formmethod="dialog" — that path goes
    // through the browser's built-in dialog
    // close logic without needing this helper.
    if (typeof dlg.close === 'function') {
      dlg.close();
    }
  }

  // Expose on window so per-page event handlers
  // (Phase 4, Phase 5) can call them without
  // their own module pattern.
  window.openModal = openModal;
  window.closeModal = closeModal;

  function ready(fn) {
    // defer puts the script execution after the HTML is
    // parsed, so document.getElementById is enough — no
    // DOMContentLoaded race. The function is here as
    // defense-in-depth: a future contributor who removes
    // `defer` (e.g. to inline the script for some reason)
    // will still get the form wired up correctly.
    if (document.readyState !== 'loading') {
      fn();
    } else {
      document.addEventListener('DOMContentLoaded', fn);
    }
  }

  ready(function () {
    var form = document.getElementById('chat-form');
    if (!form) {
      // No chat form on this page — nothing to wire. The
      // login page and the loading templates do not carry a
      // #chat-form, so reaching this branch is normal.
      return;
    }

    var sendBtn = document.getElementById('chat-send');
    var input = document.getElementById('chat-input');

    // Phase 2.1 of plans/ux-overhaul.md: the empty
    // state placeholder carries clickable suggested
    // prompts. A delegated click handler on the
    // placeholder (rather than a per-button listener)
    // keeps the wiring alive even after the
    // placeholder is removed and a new one is
    // created. The handler:
    //
    //   1. Finds the closest [data-suggested-prompt]
    //      button (the delegated selector).
    //   2. Copies the prompt text into the chat
    //      textarea.
    //   3. Focuses the textarea (so the operator
    //      can edit before sending, or just hit
    //      Enter).
    //   4. Removes the placeholder from the DOM
    //      (the htmx swap-out flow expects the
    //      placeholder to be absent before the
    //      first message arrives; if the operator
    //      just sends the suggested prompt as-is,
    //      the swap-out happens naturally on the
    //      chat-message response).
    //
    // The handler is installed only if the placeholder
    // exists, so the login page (which doesn't carry
    // the suggested-prompts card) is unaffected.
    var placeholder = document.getElementById('chat-messages-placeholder');
    if (placeholder && input) {
      placeholder.addEventListener('click', function (e) {
        var target = e.target;
        // The data-suggested-prompt attribute may
        // sit on the button itself or on a child
        // element (the 🔍 emoji <span> is inside
        // the button). Walk up the DOM to find the
        // button.
        while (target && target !== placeholder) {
          if (target.dataset && target.dataset.suggestedPrompt) {
            break;
          }
          target = target.parentNode;
        }
        if (!target || !target.dataset || !target.dataset.suggestedPrompt) {
          return;
        }
        input.value = target.dataset.suggestedPrompt;
        input.focus();
        // Remove the placeholder from the DOM. The
        // htmx swap-out in chat_message.html targets
        // the placeholder by id, so it must be
        // absent before the first message lands.
        // (Submitting the form does not remove the
        // placeholder; the swap-out on the first
        // response does. Removing it on click is a
        // UX win — the user has "picked" a
        // suggestion and the empty-state chrome
        // should get out of the way.)
        if (placeholder.parentNode) {
          placeholder.parentNode.removeChild(placeholder);
        }
      });
    }

    // Keyboard submit shortcut: Ctrl+Enter (Windows / Linux)
    // and Cmd+Enter (macOS) submit the chat form. Plain Enter
    // is intentionally left alone — the textarea default of
    // inserting a newline is the right behavior for multi-line
    // prompts, and the Send button remains the universal
    // fallback for operators who don't know the shortcut.
    //
    // We key on KeyboardEvent.key === "Enter" rather than the
    // legacy keyCode === 13 to avoid the deprecated API, and
    // we explicitly preventDefault on a matched shortcut so
    // the browser doesn't insert a stray newline before the
    // form submit fires.
    //
    // The submit goes through form.requestSubmit() so the
    // browser fires a cancelable submit event; htmx is wired
    // to intercept form submissions via its hx-post, which
    // means the existing htmx:beforeRequest / htmx:afterRequest
    // listeners above still run — the loading-state CSS, the
    // textarea reset, and the focus-restore all keep working
    // for keyboard-driven submits exactly the way they do for
    // mouse clicks.
    if (input) {
      input.addEventListener('keydown', function (e) {
        if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
          e.preventDefault();
          if (form.requestSubmit) {
            form.requestSubmit();
          } else {
            // Defense in depth: very old browsers without
            // requestSubmit fall back to .submit(). The form
            // still has hx-post so htmx intercepts the
            // submission; the loading-state UX still works.
            form.submit();
          }
        }
      });
    }

    // Before the request goes out: flag the send button as
    // loading so the user gets visual feedback. The
    // .htmx-request / .htmx-indicator CSS in /static/chat.css
    // also surfaces the "Thinking…" tag in the chat header.
    form.addEventListener('htmx:beforeRequest', function () {
      if (sendBtn) {
        sendBtn.classList.add('is-loading');
      }
    });

    // After the request completes: clear the loading flag,
    // reset the textarea so the next message starts blank,
    // and put focus back in the textarea so the user can
    // type the next question immediately.
    form.addEventListener('htmx:afterRequest', function () {
      if (sendBtn) {
        sendBtn.classList.remove('is-loading');
      }
      form.reset();
      if (input) {
        input.focus();
      }
    });

    // If the server returns a 4xx/5xx the after-request
    // event still fires, but we strip the loading flag
    // explicitly here for any future handler that does its
    // own error UX. Today this is belt-and-suspenders; the
    // after-request handler already removes it.
    form.addEventListener('htmx:responseError', function () {
      if (sendBtn) {
        sendBtn.classList.remove('is-loading');
      }
    });

    // Issue #88 + follow-on: "Jump to latest" affordance
    // and "always-scroll-on-new-message" auto-scroll.
    //
    // The chat log scrolls independently of the page;
    // once the user scrolls up to read history they can
    // lose track of where new messages are landing. The
    // follow-on revision changed the auto-scroll from
    // "scroll only if already near the bottom" to
    // "always scroll to the bottom on every new message"
    // because the user reported the near-bottom check
    // left them stranded at a stale scroll position —
    // they didn't want to have to click "Jump to latest"
    // to see the newest reply. The trade-off (called out
    // in TestChat_AutoScrollOnEveryNewMessage) is that an
    // operator scrolled up to read history will be
    // scrolled away when a new message arrives; the
    // Jump-to-latest button still appears when they
    // manually scroll up after the fact, so they can
    // get back to the tail with one click.
    //
    // The button is rendered inside the chat-log
    // container (chatFallbackBody) and stays pinned to
    // its bottom-right via .jump-to-latest CSS. chat.js
    // toggles the .is-visible class on every scroll
    // event so the button only shows when the operator
    // is not at the tail. Click → smooth scroll to the
    // bottom.
    var log = document.getElementById('chat-messages');
    var jumpBtn = document.getElementById('jump-to-latest');
    if (log && jumpBtn) {
      var NEAR_BOTTOM_PX = 48;

      function isNearBottom(el) {
        // scrollTop + clientHeight within a small threshold
        // of scrollHeight → operator is at the tail.
        return el.scrollTop + el.clientHeight >= el.scrollHeight - NEAR_BOTTOM_PX;
      }

      function updateJumpVisibility() {
        if (isNearBottom(log)) {
          jumpBtn.classList.remove('is-visible');
        } else {
          jumpBtn.classList.add('is-visible');
        }
      }

      jumpBtn.addEventListener('click', function () {
        // scrollIntoView with smooth behavior animates the
        // jump; falls back to instant scroll in older browsers.
        log.scrollTo({top: log.scrollHeight, behavior: 'smooth'});
      });

      log.addEventListener('scroll', updateJumpVisibility, {passive: true});

      // On every new message (htmx:afterRequest fires after
      // the chat-form POST and the response fragment is
      // beforeend-swapped into the log), scroll to the
      // bottom unconditionally. The previous "near-bottom
      // check" gated the scroll on the user already being
      // at the tail — that left the user stranded at a
      // stale scroll position when a new message arrived
      // while they were reading history. The Jump-to-latest
      // button still appears when the user manually scrolls
      // up after the fact, so they can get back to the
      // tail with one click.
      form.addEventListener('htmx:afterRequest', function () {
        log.scrollTop = log.scrollHeight;
        updateJumpVisibility();
      });

      // Initial state — empty log is "near the bottom" so
      // the button stays hidden until the first reply.
      updateJumpVisibility();
    }

    // Phase 2.2 of plans/ux-overhaul.md: the chat
    // message fragment renders an action bar (Copy +
    // Regenerate buttons + "N sources" badge) on the
    // assistant bubble. The buttons use
    // data-action="copy" and data-action="regenerate"
    // attributes; chat.js wires them via a delegated
    // handler on the chat log so newly swapped-in
    // messages get the behavior without re-binding.
    //
    // The handler is installed only on the chat log
    // container (#chat-messages), not on document,
    // so the wiring stays scoped to the chat surface.
    // htmx swaps the new chat message fragment into
    // #chat-messages via beforeend; the delegated
    // handler picks up the new buttons without
    // needing a per-button listener.
    if (log) {
      log.addEventListener('click', function (e) {
        var target = e.target;
        // Walk up to find the button with the
        // data-action attribute. The user might
        // click the <span> inside the button (the
        // "Copy" / "↻" text), so closest() is the
        // right tool.
        var btn = target.closest && target.closest('button[data-action]');
        if (!btn) {
          return;
        }
        var action = btn.getAttribute('data-action');
        if (action === 'copy') {
          // Find the assistant's chat-bubble that
          // contains this button. The reply text is
          // the .chat-msg element inside the
          // bubble; we copy its textContent (plain
          // text, not the rendered HTML) so the
          // clipboard ends up with what the
          // operator reads.
          var bubble = btn.closest('.chat-bubble');
          if (!bubble) {
            return;
          }
          var msgEl = bubble.querySelector('.chat-msg');
          if (!msgEl) {
            return;
          }
          var text = msgEl.textContent || '';
          // The Clipboard API requires a secure
          // context (https or localhost) and a
          // user gesture (the click). Both are
          // present. navigator.clipboard is
          // supported in every modern browser;
          // the legacy execCommand('copy') path
          // is omitted as a deliberate
          // simplification (this app does not
          // target IE).
          if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(text).then(function () {
              if (typeof window.showToast === 'function') {
                window.showToast('Reply copied to clipboard', 'success', 2000);
              }
            }, function () {
              if (typeof window.showToast === 'function') {
                window.showToast('Could not copy to clipboard', 'error');
              }
            });
          } else {
            if (typeof window.showToast === 'function') {
              window.showToast('Clipboard API not available in this browser', 'warning');
            }
          }
        } else if (action === 'regenerate') {
          // The regenerate flow is a follow-on
          // enhancement (the current LLM
          // integration does not support
          // regeneration). For now, show an
          // informational toast; the wire-up
          // for the actual regenerate endpoint
          // is a separate phase.
          if (typeof window.showToast === 'function') {
            window.showToast('Regenerate is not yet implemented', 'info');
          }
        }
      });
    }

  });
})();