// code-copy.js - add a copy-to-clipboard button to each code block.
//
// goldmark emits code blocks as <pre><code>...</code></pre>. This wraps each one
// (except mermaid diagram sources, which the mermaid module replaces) in a
// positioned .code-block and drops a small button in the top-right corner;
// copyFeedback (lib/clipboard.js) wires the copy + check-confirmation. No-ops
// where the Clipboard API is unavailable.
import { copyFeedback } from "../lib/clipboard.js";

export function initCodeCopy(): void {
  if (!navigator.clipboard) return;

  // Idle icon; sized by `.code-copy svg` in site.css (the shared check matches).
  const CLIPBOARD =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<rect x="9" y="9" width="13" height="13" rx="2"></rect>' +
    '<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>';

  document.querySelectorAll("pre > code").forEach((code) => {
    if (code.classList.contains("language-mermaid")) return;
    const pre = code.parentElement;
    if (!pre) return;
    const parent = pre.parentElement;
    if (!parent || parent.classList.contains("code-block")) return;

    const wrap = document.createElement("div");
    wrap.className = "code-block";
    parent.insertBefore(wrap, pre);
    wrap.appendChild(pre);

    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "code-copy";
    btn.setAttribute("aria-label", "Copy code to clipboard");
    btn.setAttribute("title", "Copy code to clipboard");
    btn.setAttribute("data-tooltip", "Copy code");
    btn.setAttribute("data-placement", "left");
    btn.innerHTML = CLIPBOARD;
    wrap.appendChild(btn);

    copyFeedback({
      el: btn,
      getText: () => code.textContent,
      restIcon: CLIPBOARD,
      restLabel: "Copy code to clipboard",
      doneLabel: "Copied",
      failLabel: "Copy failed",
    });
  });
}
