// copy.js - add a copy-to-clipboard button to each code block.
//
// goldmark emits code blocks as <pre><code>...</code></pre>. This wraps each one
// (except mermaid diagram sources, which mermaid-init.js replaces) in a
// positioned .code-block and drops a small button in the top-right corner;
// clipboard.js (window.copyFeedback) wires the copy + check-confirmation. No-ops
// where the Clipboard API or the shared helper is unavailable.
(function () {
  if (!navigator.clipboard || !window.copyFeedback) return;

  // Idle icon; sized by `.code-copy svg` in site.css (the shared check matches).
  var CLIPBOARD =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<rect x="9" y="9" width="13" height="13" rx="2"></rect>' +
    '<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>';

  document.querySelectorAll("pre > code").forEach(function (code) {
    if (code.classList.contains("language-mermaid")) return;
    var pre = code.parentElement;
    if (!pre || pre.parentElement.classList.contains("code-block")) return;

    var wrap = document.createElement("div");
    wrap.className = "code-block";
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);

    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "code-copy";
    btn.setAttribute("aria-label", "Copy code to clipboard");
    btn.innerHTML = CLIPBOARD;
    wrap.appendChild(btn);

    window.copyFeedback({
      el: btn,
      getText: function () { return code.textContent; },
      restIcon: CLIPBOARD,
      restLabel: "Copy code to clipboard",
      doneLabel: "Copied",
      failLabel: "Copy failed",
    });
  });
})();
