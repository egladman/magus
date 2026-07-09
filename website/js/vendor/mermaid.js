// vendor/mermaid.js - single-line entry point for esbuild to bundle mermaid
// into gen/assets/mermaid.js. Mirrors the pattern of graph-explorer.js ->
// gen/graph/explorer.js: one committed artifact per npm-dep bundle. Lives in
// js/vendor/ (third-party bundle entries) to keep it distinct from js/mermaid.js,
// the init/theming module that consumes the built bundle.
// Version pin: mermaid@11.16.0 (package.json). To upgrade, bump both
// the package.json dep and regenerate; commit the new gen/assets/mermaid.js.
//
// `export { default }`, NOT `export *`: mermaid's public API is its default
// export, and `export *` re-exports named bindings only (never default), so
// consumers reading `m.default` would get undefined and throw. Keep this a
// default re-export.
export { default } from "mermaid";
