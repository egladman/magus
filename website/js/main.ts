// main.ts - the deferred bundle entry. It imports every non-critical feature
// module for its side effects (each guards on the presence of its own DOM targets,
// so importing them all is safe and cheap). esbuild bundles this to gen/main.js;
// the heavy CDN libraries the syntax-highlight and mermaid modules pull in stay
// lazy dynamic import()s, which esbuild leaves as external runtime imports.
//
// Modules are imported with .js specifiers even though the sources are .ts: that is
// the standard TypeScript convention (the extension names the emitted file), and
// esbuild resolves each to its .ts source. Modules not yet converted are still .js
// and resolve directly; the mix is fine (allowJs + esbuild bundle both).
import "./nav.js";
import "./ref-drawer.js";
import "./toc.js";
import "./search.js";
import "./anchors.js";
import "./code-copy.js";
import "./syntax-highlight.js";
import "./mermaid.js";
import "./home-heading.js";
import "./back-to-top.js";
import "./prefetch.js";
import "./run-example.js";
import "./keyboard-help.js";
import "./announcement.js";
import "./relative-time.js";
import "./offline-badge.js";
import "./service-worker-register.js";
