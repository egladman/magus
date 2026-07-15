// page.ts - the console "surface" contract. A surface is a thing you can open in a
// tab or a tiling pane (dashboard, graph, logs). The console drives these interfaces;
// each surface implements them. This file is PURE TYPES - no runtime, erased at build
// - so it is the seam the tab strip (tabs.ts), the tiling layout (tiling.ts), and the
// shared search box are written against before any surface implements it.
//
// Two generics per surface: S is the surface's own state type, Q its search-query
// type. dashboard is PageModule<DashboardState, RowFilter>, logs is
// PageModule<LogState, ParsedQuery>, etc. - so each surface keeps its state and its
// query grammar strongly typed instead of collapsing to `any` at the console boundary.

export interface PageModule<S, Q> {
  readonly id: string; // "dashboard" | "graph" | "logs"
  readonly title: string;
  // Build the surface into `host` and return the controller the console drives. Async so
  // the heavy per-surface bundle (d3-force, protobuf) is a dynamic import() - a tab
  // stays cheap until it is actually opened.
  activate(host: HTMLElement): Promise<PageController<S, Q>>;
}

export interface PageController<S, Q> {
  readonly search: SearchProvider<Q>;
  // Tear down anything with a lifetime (chart instances, SSE streams, observers) when
  // the tab/pane closes, so the console can re-compose without leaks.
  deactivate(): void;
  // Called when this surface's tab becomes active (true) or is hidden by another tab (false). A
  // surface that writes the SHARED bottom status bar (via #console-conn etc.) must suppress those
  // writes while hidden, or a background stream (e.g. the dashboard's) leaks into the active tab's
  // per-tab status bar. Optional: static surfaces need not implement it.
  setVisible?(visible: boolean): void;
  // The surface's current state, for the console to inspect (title updates, etc.). Typed
  // per surface via S; the console treats it opaquely.
  readonly state?: S;
}

// The console owns the one search input, its debounce, the #q= deep link, and the
// "N matches" chip. The SURFACE owns the grammar via Q: it parses the raw text into
// its own query type and applies it to its own view.
export interface SearchProvider<Q> {
  readonly placeholder: string;
  parse(text: string): Q; // surface grammar -> its own query type
  apply(query: Q): SearchOutcome; // mutate the surface's view, report the match count
  serialize?(query: Q): string; // for #q= links; defaults to the raw text when absent
}

export interface SearchOutcome {
  readonly matches: number;
  readonly cursor?: MatchCursor; // only for find-in-page steppers (logs, graph)
}

export interface MatchCursor {
  next(): void;
  prev(): void;
  readonly index: number;
  readonly total: number;
}
