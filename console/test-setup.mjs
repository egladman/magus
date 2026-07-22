// Preloaded via `node --import ./test-setup.mjs` before the test suite. It registers
// happy-dom's document/window as node globals so the DOM-touching tests (view-dom.test.ts)
// run under node:test. Loaded natively (not through the esbuild bundle) because happy-dom
// pulls in CommonJS dynamic requires that an esbuild ESM bundle cannot resolve.
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register();
