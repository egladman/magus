// vendor/hljs.js - single-line entry point for esbuild to bundle highlight.js
// into gen/assets/hljs.js. Mirrors the vendor/mermaid.js pattern (third-party
// bundle entries live in js/vendor/, distinct from js/syntax-highlight.js, the
// init module that consumes the built bundle).
// Version pin: highlight.js@11.11.1 (package.json). To upgrade, bump both
// the package.json dep and regenerate; commit the new gen/assets/hljs.js.
// lib/common (not lib/core) is the ~35-language set matching the old CDN
// "common" build; core registers zero languages, which would silently drop
// highlighting for every fence except the hand-registered buzz grammar.
//
// `export { default }`, NOT `export *`: highlight.js's API is its default export
// (the hljs instance), and `export *` re-exports named bindings only (never
// default), so consumers reading `m.default` would get undefined and throw.
export { default } from "highlight.js/lib/common";
