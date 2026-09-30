// page.ts - the console "surface" contract. A surface is a thing you can open in a
// tab or a tiling pane (dashboard, graph, logs). The console drives these interfaces;
// each surface implements them. This file is PURE TYPES - no runtime, erased at build
// - so it is the seam the tab bar (tabs.ts), the tiling layout (tiling.ts), and the
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
  // Called when this surface's tab becomes active (true) or is hidden by another tab (false).
  //
  // EVERY surface implements this, whether or not it has anything to suppress today. Two things are
  // only correct while a surface is the visible one: writes to the SHARED bottom status bar (it is
  // per-tab, and the console detaches the hidden ones, so #console-conn and friends resolve to the
  // ACTIVE tab's bar), and background work nobody is watching (the graph's force simulation ticked
  // and repainted an invisible canvas until it grew one of these). Both were found as bugs rather
  // than designed away, which is why the hook is the convention and not the exception - a surface
  // with nowhere to put it is a surface that discovers the problem later.
  //
  // Required at the shell boundary. A static surface uses an explicit no-op, but it cannot bypass
  // the lifecycle: future streams, polling, or status-bar controls start from one enforced place.
  setVisible(visible: boolean): void;
  // The document this surface currently has open, for the console to title the tab after
  // (see TitleSource). A surface with no document concept omits it and keeps its static title.
  readonly docTitle?: TitleSource;
  // The surface's current state, for the console to inspect. Typed per surface via S; the
  // console treats it opaquely.
  readonly state?: S;
}

// TitleSource is how a surface reports the document it currently has open, so the console can
// title its tab after it the way a browser titles a tab after its page and an editor after its
// file. Read-and-subscribe only: the surface owns the value, the console merely observes it, so
// a title can never be pushed back into a surface that did not choose it.
//
// This is deliberately NOT view.ts's Signal (which also carries set) and deliberately structural:
// each surface is its own bundle, so the Signal a surface hands over comes from that bundle's own
// copy of view.ts. Nothing here depends on shared module identity, only on the shape.
//
// null means "no document open" - the console falls back to the surface's static PageModule.title.
export interface TitleSource {
  get(): string | null;
  subscribe(fn: (title: string | null) => void): () => void; // returns an unsubscribe
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
