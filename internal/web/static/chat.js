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
  });
})();