// state.ts - the log viewer's shared, mutable module state and the domain types the
// concern modules pass around. The original monolith kept these as top-level `let`s; the
// split hoists them into one `state` object so every module (render, waterfall, filter,
// live, demo) reads and writes the same instance. waterfallSource lives here too because it
// is the one function that reads across the whole state (the static journal, a multi list,
// and the live buffer) to produce the invocation-sources every view is built from.

import type { Command, Event, Invocation, Journal } from "../../gen/magus/viewer/v1/viewer_pb";
// Section/RenderModel are lifted to the shared render module so the activity view builds the
// same shape; re-exported here so the log viewer's modules keep their one import path.
import type { RenderModel, Section } from "../render/model";
export type { RenderModel, Section };

// The parsed filter query ({groups, texts, empty}) - see filter.ts parseQuery.
export interface FilterGroup {
  key: string;
  value: string;
}
export interface ParsedQuery {
  groups: FilterGroup[];
  texts: string[];
  empty: boolean;
}

// A waterfall step (child span) and target span, and the span trees built from them.
export interface Step {
  label: string;
  s: number;
  e: number;
}
export interface TargetSpan {
  label: string;
  status: string;
  ref: string;
  s: number;
  e: number;
  steps: Step[];
}
export interface SpanTree {
  t0: number;
  t1: number;
  targets: TargetSpan[];
}
export interface SpanGroup {
  label: string;
  targets: TargetSpan[];
}
export interface SpanMulti {
  t0: number;
  t1: number;
  groups: SpanGroup[];
}

// The active time-focus window, {a, b} in absolute ms, or null for the full run.
export interface FocusWin {
  a: number;
  b: number;
}

// The invocation as the model/waterfall layer consumes it: a full Journal Invocation, or a
// partial live/demo header (only command/start/end are ever read), or absent.
export type InvocationView = Partial<Invocation> | null | undefined;

// One invocation's events + header - the unit waterfallSource yields and the views iterate.
export interface Source {
  events: Event[];
  invocation: InvocationView;
}

// The one built model + summary a source stream produces (pretty view + raw lines).
export interface BuiltModel {
  sections: Section[];
  titled: number;
  rawLines: string[];
  summary: string;
}

// The whole mutable state of the viewer. One instance (`state`) is shared across modules.
export interface ViewerState {
  // model backs the pretty/raw views; rawText the copy-all; rawLines the RAW view in Journal mode.
  model: RenderModel | null;
  rawText: string;
  rawLines: string[] | null;
  // currentRef is the ref id from #ref=, if the log came from `magus query ... --open`.
  currentRef: string;
  // currentJournal holds the structured Journal when one is loaded (the #data protobuf path);
  // currentJournals, when set, is a LIST of journals rendered as separate groups (takes precedence).
  currentJournal: Journal | null;
  currentJournals: Journal[] | null;
  // pretty toggles the stylized structural view (default) vs the raw captured text.
  pretty: boolean;
  // timeline toggles the trace-waterfall view over the log/section view.
  timeline: boolean;
  // filterQuery is the raw filter string (mirrored to #q=); filterParsed is its parsed form.
  filterQuery: string;
  filterParsed: ParsedQuery;
  // focusWin is the active time-range focus, or null for the full run.
  focusWin: FocusWin | null;
  // The live-stream buffer (#live=) - also reused by the #demo reveal.
  liveEvents: Event[];
  liveInvocation: Partial<Invocation> | null;
  livePaused: boolean;
  liveRenderQueued: boolean;
  liveAbort: AbortController | null;
  // demoTimer drives the #demo showcase's incremental reveal; demoActive marks it in progress.
  demoTimer: number | null;
  demoActive: boolean;
  // highlightStart tracks the #L line-range anchor for shift-click.
  highlightStart: number | null;
}

// The shared instance. filterParsed is seeded to the empty parse (parseQuery("")) so state.ts
// need not depend on filter.ts at module-init - the value is identical.
export const state: ViewerState = {
  model: null,
  rawText: "",
  rawLines: null,
  currentRef: "",
  currentJournal: null,
  currentJournals: null,
  pretty: true,
  timeline: false,
  filterQuery: "",
  filterParsed: { groups: [], texts: [], empty: true },
  focusWin: null,
  liveEvents: [],
  liveInvocation: null,
  livePaused: false,
  liveRenderQueued: false,
  liveAbort: null,
  demoTimer: null,
  demoActive: false,
  highlightStart: null,
};

// waterfallSource returns the list of invocation-sources to render: one entry per invocation.
// Usually a single loaded/streaming invocation, but the log viewer is invocation-agnostic - a
// static currentJournals list (e.g. the multi-invocation demo) renders alongside the live
// buffer, so several invocations show as separate groups on a shared time axis.
export function waterfallSource(): Source[] {
  const out: Source[] = [];
  if (state.currentJournals) {
    state.currentJournals.forEach((j) => out.push({ events: j.events || [], invocation: j.invocation }));
  } else if (state.currentJournal) {
    return [{ events: state.currentJournal.events || [], invocation: state.currentJournal.invocation }];
  }
  if (state.currentJournals || state.liveEvents.length || state.liveInvocation) {
    out.push({ events: state.liveEvents, invocation: state.liveInvocation });
  }
  return out.length ? out : [{ events: state.liveEvents, invocation: state.liveInvocation }];
}

// A convenience re-export so consumers building demo/live command labels get the Command type
// without a second import path.
export type { Command };
