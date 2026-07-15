// vendor/mermaid.ts - single-line entry point for esbuild to bundle mermaid
// into gen/assets/mermaid.js. Mirrors the pattern of src/console/graph/main.ts
// -> gen/console/graph/explorer.js: one committed artifact per npm-dep bundle. Lives in
// src/vendor/ (third-party bundle entries) to keep it distinct from src/site/mermaid.ts,
// the init/theming module that consumes the built bundle. This file is not raw library
// source - mermaid is a pinned pnpm dependency; this only re-exports it.
// Version pin: mermaid@11.16.0 (package.json). To upgrade, bump both
// the package.json dep and regenerate; commit the new gen/assets/mermaid.js.
//
// `export { default }`, NOT `export *`: mermaid's public API is its default
// export, and `export *` re-exports named bindings only (never default), so
// consumers reading `m.default` would get undefined and throw. Keep this a
// default re-export.
export { default } from "mermaid";
