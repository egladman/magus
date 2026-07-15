// main.ts - the deferred bundle entry and composition root. Each non-critical
// feature module exports a named init function; this file imports them all and
// calls each one explicitly, in a fixed order (below). Every init guards on the
// presence of its own DOM targets, so running them all on every page is safe and
// cheap. esbuild bundles this to gen/main.js; the heavy CDN libraries the
// syntax-highlight and mermaid modules pull in stay lazy dynamic import()s, which
// esbuild leaves as external runtime imports.
//
// Modules are imported with .js specifiers even though the sources are .ts: that is
// the standard TypeScript convention (the extension names the emitted file), and
// esbuild resolves each to its .ts source.
import { initNav } from "./nav.js";
import { initTocToggle, initScrollSpy } from "./toc.js";
import { initSearch } from "./search.js";
// ref-drawer + console-settings live in src/ui/ (shared nav panels, styled by
// styles/ui-panels.css) - loaded on the docs pages AND the console apps, not docs-only.
import { initRefDrawer } from "../ui/ref-drawer.js";
import { initConsoleSettings } from "../ui/console-settings.js";
import { initAnchors } from "./anchors.js";
import { initCodeCopy } from "./code-copy.js";
import { initSyntaxHighlight } from "./syntax-highlight.js";
import { initMermaid } from "./mermaid.js";
import { initHomeHeading } from "./home-heading.js";
import { initBackToTop } from "./back-to-top.js";
import { initPrefetch } from "./prefetch.js";
import { initRunExample } from "./run-example.js";
import { initKeyboardHelp } from "./keyboard-help.js";
import { initAnnouncement } from "./announcement.js";
import { initRelativeTime } from "./relative-time.js";
import { initOfflineBadge } from "./offline-badge.js";
import { initServiceWorker } from "./service-worker-register.js";

initNav();
initTocToggle();
initScrollSpy();
initSearch();
// ref-drawer runs AFTER search so it can pull the search bar (.page-tools) into the drawer.
initRefDrawer();
initConsoleSettings(); // the gear settings panel on the console apps (no-op on docs pages)
initAnchors();
initCodeCopy();
initSyntaxHighlight();
initMermaid();
initHomeHeading();
initBackToTop();
initPrefetch();
initRunExample();
initKeyboardHelp();
initAnnouncement();
initRelativeTime();
initOfflineBadge();
initServiceWorker();
