// mermaid-init.js - render ```mermaid fenced code blocks as diagrams, themed to
// match Pico so the graphs share one visual language with the rest of the site.
//
// goldmark emits a mermaid fence as <pre><code class="language-mermaid">. We swap
// each one for a <div class="mermaid"> and run mermaid over it. The library is
// loaded from the committed same-origin bundle (gen/assets/mermaid.js, built by
// `magus run build-mermaid website` from js/mermaid-vendor.js) only on pages that
// actually contain a diagram, so other pages pay nothing.
//
// Mermaid's stock "default"/"dark" palettes know nothing about Pico and clash
// with it. Instead we drive mermaid's "base" theme from Pico's own CSS variables,
// read off the live page, so nodes read like Pico code blocks and edges/labels use
// the Pico text palette. Toggling the site theme re-reads the variables and re-renders
// the diagrams, so they track light/dark exactly like every other component.
//
// The bundle is NOT precached by the SW (3 MB; cache-first same-origin on first use).
// The import path resolves relative to gen/main.js (where this module is bundled),
// so ./assets/mermaid.js is correct regardless of the page's URL depth.
if (document.querySelector("code.language-mermaid")) {
  import("./assets/mermaid.js").then((m) => {
    const root = document.documentElement;

    // Capture each diagram's source once. Mermaid replaces the .mermaid node's
    // contents with an <svg> on render, so re-renders restore the source from here.
    const blocks = [];
    document.querySelectorAll("pre > code.language-mermaid").forEach((code) => {
      const div = document.createElement("div");
      div.className = "mermaid";
      div.textContent = code.textContent;
      code.parentElement.replaceWith(div);
      blocks.push({ el: div, src: code.textContent });
    });

    function render() {
      const dark =
        root.getAttribute("data-theme") === "dark" ||
        (root.getAttribute("data-theme") !== "light" &&
          matchMedia("(prefers-color-scheme: dark)").matches);

      // One computed-style read per render; pico() pulls each Pico custom
      // property off it, falling back when the variable is unset.
      const cs = getComputedStyle(root);
      const pico = (name, fallback) => cs.getPropertyValue(name).trim() || fallback;

      const bg = pico("--pico-background-color", dark ? "#13171f" : "#ffffff");
      // Pico's code-block tint: a surface that reads as distinct from the page
      // background, so nodes stay legible even when card-bg == page-bg (light mode).
      const surface = pico("--pico-code-background-color", dark ? "#1b1f29" : "#f3f4f6");
      const text = pico("--pico-color", dark ? "#c2c7d0" : "#373c44");
      const muted = pico("--pico-muted-color", dark ? "#7b8495" : "#646b79");
      const border = pico("--pico-muted-border-color", dark ? "#202632" : "#dce3eb");
      const font = pico("--pico-font-family", "system-ui, -apple-system, sans-serif");

      // Re-initialize on every render so a theme toggle takes effect cleanly.
      m.default.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        theme: "base",
        fontFamily: font,
        themeVariables: {
          darkMode: dark,
          background: bg,
          fontFamily: font,
          fontSize: "15px",
          // Nodes read like Pico code blocks: a tinted surface, muted border, body text.
          primaryColor: surface,
          mainBkg: surface,
          secondaryColor: surface,
          tertiaryColor: bg,
          primaryBorderColor: border,
          nodeBorder: border,
          primaryTextColor: text,
          textColor: text,
          nodeTextColor: text,
          titleColor: text,
          // Edges, subgraph frames and edge labels in the muted/border palette.
          lineColor: muted,
          clusterBkg: bg,
          clusterBorder: border,
          edgeLabelBackground: bg,
        },
      });

      // Restore each diagram's source, clearing any previously rendered svg, then
      // re-run mermaid over just these nodes.
      blocks.forEach((b) => {
        b.el.removeAttribute("data-processed");
        b.el.innerHTML = "";
        b.el.textContent = b.src;
      });
      // A diagram with invalid source rejects here; swallow it so one bad block
      // leaves its (legible) source text rather than an unhandled rejection.
      m.default.run({ nodes: blocks.map((b) => b.el) }).catch(() => {});
    }

    render();

    // Re-render when the site theme changes: the toggle button flips data-theme,
    // and under "auto" the OS preference can change. Debounce so the observer and
    // the media-query listener firing together still cause a single re-render.
    let timer = 0;
    const rerender = () => {
      clearTimeout(timer);
      timer = setTimeout(render, 0);
    };
    new MutationObserver(rerender).observe(root, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    matchMedia("(prefers-color-scheme: dark)").addEventListener("change", rerender);
  }).catch(function (err) {
    // Load or render failed (e.g. not yet cached offline, or a bad diagram):
    // leave the <pre> source visible rather than letting the import reject
    // unhandled - the diagram reads as its (legible) source text. Warn (do not
    // swallow): an empty catch here once hid a broken vendor bundle for a day.
    console.warn("mermaid render skipped:", err);
  });
}
