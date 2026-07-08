// hljs-init.js - syntax highlighting for fenced code blocks, themed by the
// --syn-* variables in theme.css (so it tracks light/dark like everything else).
//
// goldmark emits ```lang fences as <pre><code class="language-lang">. We pull the
// highlight.js bundle from the committed same-origin artifact (gen/assets/hljs.js,
// built by `magus run build-hljs website` from js/hljs-vendor.js) only when a
// highlightable block exists, register a small Buzz grammar the common bundle lacks
// (magusfile examples are the site's most frequent code), then highlight each block
// whose language we actually know - unknown languages are left as legible plain text
// rather than mis-highlighted.
//
// The import path resolves relative to gen/main.js (where this module is bundled).
(function () {
  var blocks = document.querySelectorAll('pre > code[class*="language-"]');
  if (!blocks.length) return;

  // Skip mermaid fences: mermaid-init.js turns those into diagrams.
  var work = [];
  blocks.forEach(function (el) {
    if (!el.classList.contains("language-mermaid")) work.push(el);
  });
  if (!work.length) return;

  import("./assets/hljs.js")
    .then(function (m) {
      var hljs = m.default;

      // Minimal Buzz grammar: keywords, types, strings (plain and $"..."
      // interpolated), line/block comments, and numbers. Enough for the
      // magusfile snippets to read in color without a full language spec.
      hljs.registerLanguage("buzz", function (hl) {
        return {
          name: "Buzz",
          keywords: {
            keyword:
              "import export fun var final return if else while for foreach in " +
              "and or not throw try catch break continue is as from do resume " +
              "resolve yield test zdef extern object protocol enum mut",
            type: "str bool int float double void any obj pat ud fib rg",
            literal: "true false null void",
          },
          contains: [
            hl.C_LINE_COMMENT_MODE,
            hl.C_BLOCK_COMMENT_MODE,
            { className: "string", begin: /\$?"/, end: /"/, contains: [hl.BACKSLASH_ESCAPE] },
            hl.C_NUMBER_MODE,
            {
              className: "title.function_",
              begin: /\bfun\s+/,
              end: /[A-Za-z_]\w*/,
              excludeBegin: true,
              returnEnd: false,
            },
          ],
        };
      });

      hljs.configure({ ignoreUnescapedHTML: true });

      work.forEach(function (el) {
        var cls = (el.className.match(/language-([\w-]+)/) || [])[1];
        // Only highlight languages we actually have a grammar for; otherwise
        // leave the block as plain (but still styled) text.
        if (cls && hljs.getLanguage(cls)) hljs.highlightElement(el);
      });
    })
    .catch(function () {
      // Load failed (e.g. not yet cached offline): code blocks stay as legible plain text.
    });
})();
