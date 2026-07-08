// mermaid-vendor.js - single-line entry point for esbuild to bundle mermaid
// into gen/assets/mermaid.js. Mirrors the pattern of graph-explorer.js ->
// gen/graph/explorer.js: one committed artifact per npm-dep bundle.
// Version pin: mermaid@11.16.0 (package.json). To upgrade, bump both
// the package.json dep and regenerate; commit the new gen/assets/mermaid.js.
export * from "mermaid";
