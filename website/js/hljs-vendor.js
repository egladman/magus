// hljs-vendor.js - single-line entry point for esbuild to bundle highlight.js
// into gen/assets/hljs.js. Mirrors the mermaid-vendor.js pattern.
// Version pin: highlight.js@11.11.1 (package.json). To upgrade, bump both
// the package.json dep and regenerate; commit the new gen/assets/hljs.js.
// lib/common (not lib/core) is the ~35-language set matching the old CDN
// "common" build; core registers zero languages, which would silently drop
// highlighting for every fence except the hand-registered buzz grammar.
export * from "highlight.js/lib/common";
