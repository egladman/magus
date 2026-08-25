// main.ts - the console's Runs surface: every run this workspace has kept, browsable without a ref.
//
// It exists because the Log Viewer's side panel answers "take me to the next run" well and "I do not
// know where to start" badly. A rail-width tree can only offer a filter BOX, which asks a reader to
// know the grammar before they can use it. A page has room for the inverse: a facet rail listing
// every value that actually occurs, with counts, one click each - and the click writes its term into
// the visible query box, so using it teaches the syntax for the times you do want to type.
//
// It is an INDEX, not a reader. Selecting a run shows what it did (its targets, outcomes, durations,
// refs); "Open output" hands off to the Log Viewer through the #inv=/#ref= links it already
// understands. Neither surface re-hosts the other's rendering, so they cannot drift.
//
// Data comes from the same two read-only daemon feeds the side panel reads (/api/v1/outputs and
// /api/v1/runs), joined on each output's invocation id, and the grouping/filtering/faceting is the
// shared pure module (logs/runindex.ts) both browsers consume.

import {
  buildFacets,
  buildRunRows,
  durText,
  matchesFilter,
  parseRunFilter,
  relTime,
  toggleFilterTerm,
  type Facet,
  type RunLog,
  type RunRow,
  type RunSummary,
} from "../logs/runindex";
import {
  demoRunLogs,
  demoRuns,
  fetchRunLogs,
  fetchRuns,
  tickRelativeTimes,
  watchRuns,
} from "../logs/runtree";
import {
  adoptDaemonOrigin,
  getLiveToken,
  parseHash,
  resolveDaemonHost,
  wantsDemo,
} from "../../lib/daemon";
import { attachHelpPopover } from "../../ui/help-popover";
import { REFRESH, svgGlyph } from "../../ui/glyph";
import { h } from "../view";
import type { SurfaceInstance } from "../standalone";

// FILTER_HELP is the one place the grammar is written down for a reader. The facets teach it by
// example; this is for the reader who wants the whole vocabulary at once.
const FILTER_HELP =
  "Terms combine with AND, case-insensitive. Free text matches the project, target, ref, error " +
  "and command line. Keys: project: target: status:pass|fail trigger: ref: cmd:. " +
  "Clicking a facet on the left writes its term here.";

interface Refs {
  query: HTMLInputElement;
  count: HTMLElement;
  facets: HTMLElement;
  list: HTMLElement;
  detail: HTMLElement;
}

// activate builds the surface into host and returns the console's teardown handle. Everything below
// is per-activation, so reopening the tab is a clean slate.
export function activate(host: HTMLElement): SurfaceInstance {
  adoptDaemonOrigin();
  const demo = wantsDemo(parseHash());
  const host_ = resolveDaemonHost(parseHash()) ?? "";
  const token = getLiveToken();
  // The demo scenario is written RELATIVE to one instant, so it is stamped once; the "how long ago"
  // labels read the clock at paint time. Sharing one frozen value made every label a lie the moment
  // the page stopped being new - invisible while nothing refreshed, obvious the moment it does.
  const demoNow = Date.now();

  let runs: RunSummary[] = [];
  let logs: RunLog[] = [];
  let loaded = false;
  let selected: string | null = null;
  let stale = false;

  const refs = build(host, {
    onQuery: () => paint(),
    onRefresh: () => void load(),
  });

  function paint(): void {
    const now = Date.now();
    const filter = parseRunFilter(refs.query.value);
    const byInv = new Map<string, RunLog>();
    for (const l of logs) byInv.set(l.inv, l);
    const kept = runs.filter((r) => matchesFilter(r, byInv.get(r.inv || ""), filter));
    const rows = buildRunRows(kept, logs, filter.empty);
    renderFacets(refs.facets, buildFacets(runs, logs, filter), (key, value) => {
      refs.query.value = toggleFilterTerm(refs.query.value, key, value);
      paint();
    });
    if (rows.length) {
      renderList(refs.list, rows, now, selected, (inv) => {
        selected = inv;
        paint();
      });
    } else {
      renderEmpty(refs.list, {
        connected: demo || !!host_,
        loaded,
        filtered: !filter.empty,
        onClear: () => {
          refs.query.value = "";
          paint();
        },
      });
    }
    // The selection can survive a filter that hides it; showing its detail beside a list it is not
    // in reads as a bug, so a hidden selection falls back to the newest visible run.
    const shown = rows.find((r) => r.inv === selected) ?? rows[0];
    renderDetail(refs.detail, shown, now, demo);
    // "N of M" counts RUNS on both sides. M was the output count, which is larger and unrelated -
    // "2 runs of 31" beside a strip whose statuses summed to 28 is three numbers for two facts.
    const total = buildRunRows(runs, logs, true).length;
    refs.count.textContent = summary(rows, total, loaded, filter.empty);
  }

  async function load(): Promise<void> {
    if (demo) {
      runs = demoRuns(demoNow);
      logs = demoRunLogs(demoNow);
    } else if (host_) {
      [runs, logs] = await Promise.all([fetchRuns(host_, token), fetchRunLogs(host_, token)]);
    } else {
      runs = [];
      logs = [];
    }
    if (stale) return; // the tab closed while the fetch was in flight
    loaded = true;
    paint();
  }

  function summary(rows: RunRow[], total: number, done: boolean, unfiltered: boolean): string {
    if (!done) return "loading...";
    if (!rows.length) return "";
    const failed = rows.filter((r) => r.status === "fail" || r.status === "mixed").length;
    const head = rows.length + (rows.length === 1 ? " run" : " runs");
    const scope = unfiltered ? "" : " of " + total;
    return head + scope + (failed ? ", " + failed + " failed" : "");
  }

  paint();
  void load();

  // The list keeps itself current while the tab is on screen: a run you kick off in a terminal
  // appears here without anyone pressing Refresh, which is the difference between a page you check
  // and a page you leave open. The stream is torn down while the tab is backgrounded - a stream per
  // hidden pane is a cost nobody asked for, and coming back re-opens it and catches up.
  let unwatch: (() => void) | null = watchRuns(host_, token, () => void load());
  // Separate from the stream: the labels age whether or not anything runs, so the demo and an
  // offline page need this even though they never open a stream.
  let untick: (() => void) | null = tickRelativeTimes(host);
  return {
    setVisible: (visible: boolean): void => {
      if (visible && !unwatch) {
        unwatch = watchRuns(host_, token, () => void load());
        untick = tickRelativeTimes(host);
        void load(); // whatever happened while this pane was hidden
        return;
      }
      if (!visible && unwatch) {
        unwatch();
        unwatch = null;
        untick?.();
        untick = null;
      }
    },
    deactivate: () => {
      stale = true;
      unwatch?.();
      unwatch = null;
      untick?.();
      untick = null;
    },
  };
}

// build assembles the page: a toolbar over a three-column body (facets, list, detail). The columns
// are plain flex children that stack below the shell's own breakpoint - see runs.css.
function build(host: HTMLElement, on: { onQuery: () => void; onRefresh: () => void }): Refs {
  const page = h("section", "console-runs");

  const bar = h("header", "console-runs__bar");
  const search = h("div", "pf-v6-c-text-input-group console-runs__search");
  const main = h("div", "pf-v6-c-text-input-group__main pf-m-icon");
  const textWrap = h("span", "pf-v6-c-text-input-group__text");
  const query = document.createElement("input");
  query.type = "search";
  query.className = "pf-v6-c-text-input-group__text-input";
  query.placeholder = "Filter runs, or click a facet";
  query.setAttribute("aria-label", "Filter runs");
  query.spellcheck = false;
  query.autocomplete = "off";
  let debounce: ReturnType<typeof setTimeout>;
  query.addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(on.onQuery, 120);
  });
  query.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && query.value) {
      ev.stopPropagation();
      query.value = "";
      on.onQuery();
    }
  });
  textWrap.append(query);
  main.append(textWrap);
  search.append(main);

  // The shared help circle, identical to the graph explorer's and the log viewer's.
  const help = h("button", "console-render-help-glyph console-runs__help", "?");
  help.setAttribute("type", "button");
  help.setAttribute("aria-label", "Filter syntax");
  help.title = FILTER_HELP;
  attachHelpPopover(help);

  const count = h("span", "console-runs__count");
  const refresh = h("button", "pf-v6-c-button pf-m-secondary console-runs__refresh");
  refresh.setAttribute("type", "button");
  const refreshIcon = h("span", "pf-v6-c-button__icon pf-m-start");
  refreshIcon.append(svgGlyph(REFRESH, 14));
  refresh.append(refreshIcon, h("span", "pf-v6-c-button__text", "Refresh"));
  refresh.addEventListener("click", on.onRefresh);
  bar.append(search, help, count, refresh);

  // The facets are a full-width STRIP under the toolbar, not a third column. They are a control for
  // the query box directly above them, and a column gave that weight it does not carry - it cost a
  // third of the page's width permanently to hold what is usually a dozen short chips, and squeezed
  // the two things a reader is actually comparing (the run list and the run) into what was left.
  const facets = h("nav", "console-runs__facets");
  facets.setAttribute("aria-label", "Filter by");

  const body = h("div", "console-runs__body");
  const list = h("div", "console-runs__list");
  list.setAttribute("role", "list");
  const detail = h("div", "console-runs__detail");
  body.append(list, detail);

  page.append(bar, facets, body);
  host.replaceChildren(page);
  return { query, count, facets, list, detail };
}

// renderFacets paints the strip: each facet is its heading followed by its values, laid out inline.
// Every value shown occurs in the data and its count says how many runs clicking it would leave, so
// nothing here can lead to an empty list by surprise.
function renderFacets(
  box: HTMLElement,
  facets: Facet[],
  onPick: (key: string, value: string) => void,
): void {
  box.replaceChildren();
  if (!facets.length) return;
  for (const facet of facets) {
    const group = h("div", "console-runs__facet");
    group.append(h("h3", "console-runs__facet-head", facet.label));
    for (const v of facet.values) {
      const b = h("button", "console-runs__facet-value");
      b.setAttribute("type", "button");
      b.setAttribute("aria-pressed", String(v.active));
      if (v.active) b.classList.add("pf-m-selected");
      b.title = (v.active ? "Remove " : "Add ") + facet.key + ":" + v.value;
      const label = h("span", "console-runs__facet-label", v.label);
      const n = h("span", "console-runs__facet-count", String(v.count));
      if (facet.key === "status") {
        const dot = h("span", "console-runs__dot");
        dot.dataset.status = v.value;
        label.prepend(dot);
      }
      b.append(label, n);
      b.addEventListener("click", () => onPick(facet.key, v.value));
      group.append(b);
    }
    box.append(group);
  }
}

// renderList paints one row per run: the command that produced it, when, how long, and how many
// targets it kept output for. The command is the row's identity because it is what a person
// remembers a run by - the id is machine vocabulary and sits in the detail pane instead.
function renderList(
  box: HTMLElement,
  rows: RunRow[],
  now: number,
  selected: string | null,
  onPick: (inv: string) => void,
): void {
  box.replaceChildren();
  for (const row of rows) {
    const b = h("button", "console-runs__row");
    b.setAttribute("type", "button");
    b.setAttribute("role", "listitem");
    if (row.inv === selected) b.classList.add("pf-m-current");
    const dot = h("span", "console-runs__dot");
    if (row.status) dot.dataset.status = row.status;
    const cmd = h("span", "console-runs__row-cmd", row.command);
    const meta = h("span", "console-runs__row-meta");
    // The "how long ago" half is its own element carrying the instant, so tickRelativeTimes can
    // advance it in place; the rest of the line never changes and is one static node beside it.
    const when = h("span", "", relTime(row.startMs, now));
    if (row.startMs) when.dataset.time = String(row.startMs);
    const rest = [];
    if (row.durationMs) rest.push(durText(row.durationMs));
    rest.push(row.outputs.length + (row.outputs.length === 1 ? " target" : " targets"));
    if (row.trigger) rest.push(row.trigger);
    meta.append(when, h("span", "", rest.length ? "  ·  " + rest.join("  ·  ") : ""));
    const head = h("span", "console-runs__row-head");
    head.append(dot, cmd);
    b.append(head, meta);
    b.addEventListener("click", () => onPick(row.inv));
    box.append(b);
  }
}

// renderDetail shows what the selected run DID: every target it kept output for, with the outcome,
// duration and ref, each one step from its captured output in the Log Viewer.
function renderDetail(box: HTMLElement, row: RunRow | undefined, now: number, demo: boolean): void {
  box.replaceChildren();
  if (!row) return;
  const head = h("div", "console-runs__detail-head");
  const title = h("div", "console-runs__detail-title");
  title.append(h("h2", "console-runs__detail-cmd", row.command));
  if (row.status) title.append(statusPill(row.status));
  head.append(title);
  // A LABELLED list, not a "·"-joined sentence. Run together, the five facts read as one long string
  // a reader has to parse before they can find the one they came for; labelled, each is a lookup.
  head.append(
    facts([
      ["When", new Date(row.startMs).toLocaleString(), relTime(row.startMs, now), row.startMs],
      ["Duration", row.durationMs ? durText(row.durationMs) : "", ""],
      ["Trigger", row.trigger, ""],
      ["magus", row.log?.magus_version ?? "", ""],
      ["Run id", row.inv, ""],
    ]),
  );
  // The whole run opens as one journal, which is the reading a target list cannot give: the order
  // things ran in, and the waterfall over them.
  if (row.log) head.append(openLink("Open the whole run", viewerHref("inv", row.inv, demo)));
  box.append(head);

  if (!row.outputs.length) {
    box.append(
      note(
        row.log
          ? "This run kept no output. Every target it ran was already cached, or its output has since aged out of the store."
          : "This run's journal has aged out, so its command and timing are gone. The outputs below are what the store still holds.",
      ),
    );
    return;
  }

  box.append(
    h(
      "h3",
      "console-runs__section-head",
      row.outputs.length === 1 ? "1 target" : row.outputs.length + " targets",
    ),
  );
  const table = h("div", "console-runs__targets");
  table.setAttribute("role", "list");
  for (const o of row.outputs) {
    const item = h("div", "console-runs__target");
    item.setAttribute("role", "listitem");
    const dot = h("span", "console-runs__dot");
    dot.dataset.status = o.failed ? "fail" : "pass";
    const name = h(
      "span",
      "console-runs__target-name",
      (o.project ? o.project + ":" : "") + o.target,
    );
    const dur = h("span", "console-runs__target-dur", durText(o.duration_ms));
    const line = h("div", "console-runs__target-line");
    line.append(dot, name, dur);
    item.append(line);
    // Everything under the name is INDENTED to the name, so a target and its ref, error and link
    // read as one block rather than as four unrelated lines stacked down the pane.
    const under = h("div", "console-runs__target-body");
    under.append(h("code", "console-runs__target-ref", o.ref));
    if (o.error) under.append(h("p", "console-runs__target-error", o.error));
    under.append(openLink("Open output", viewerHref("ref", o.ref, demo)));
    item.append(under);
    table.append(item);
  }
  box.append(table);
}

// statusPill states the outcome as a word, not only as a dot. The dot alone carries the whole fact
// in colour, which is exactly the thing a reader who cannot separate red from green does not get.
function statusPill(status: "pass" | "fail" | "mixed"): HTMLElement {
  const text = status === "pass" ? "passed" : status === "fail" ? "failed" : "partly failed";
  const pill = h("span", "console-runs__pill", text);
  pill.dataset.status = status;
  return pill;
}

// facts renders the labelled metadata list. A row whose value is empty is DROPPED rather than shown
// blank: a run whose journal aged out genuinely has no trigger or version, and an empty cell beside
// a label reads as a failure to load rather than as an absence.
function facts(rows: [string, string, string, number?][]): HTMLElement {
  const dl = h("dl", "console-runs__facts");
  for (const [label, value, extra, timeMs] of rows) {
    if (!value) continue;
    dl.append(h("dt", "console-runs__fact-label", label));
    const dd = h("dd", "console-runs__fact-value", value);
    if (extra) {
      const el = h("span", "console-runs__fact-extra", extra);
      // A relative gloss ages like every other one on the page, so it carries its instant for
      // tickRelativeTimes rather than sitting frozen beside a clock time that never lies.
      if (timeMs) el.dataset.time = String(timeMs);
      dd.append(el);
    }
    dl.append(dd);
  }
  return dl;
}

function note(text: string): HTMLElement {
  return h("p", "console-runs__note", text);
}

// renderEmpty distinguishes the four ways this list can be empty, because only one of them is the
// reader's to fix and the others read as data loss if given the same words. A filtered-to-nothing
// list gets a CONTROL, not just a sentence: the way out of an over-narrow query should not require
// selecting text in a box.
function renderEmpty(
  box: HTMLElement,
  s: { connected: boolean; loaded: boolean; filtered: boolean; onClear: () => void },
): void {
  box.replaceChildren();
  const card = h("div", "console-runs__empty");
  if (!s.connected) {
    card.append(h("h2", "console-runs__empty-title", "No daemon connected"));
    card.append(
      note(
        "This page reads the runs your local daemon has kept. Start it with `magus server start`, " +
          "or set a daemon address in Settings.",
      ),
    );
  } else if (!s.loaded) {
    card.append(h("h2", "console-runs__empty-title", "Loading runs..."));
  } else if (s.filtered) {
    card.append(h("h2", "console-runs__empty-title", "No runs match"));
    card.append(note("Nothing here carries every term in the filter."));
    const clear = h("button", "pf-v6-c-button pf-m-secondary", "Clear the filter");
    clear.setAttribute("type", "button");
    clear.addEventListener("click", s.onClear);
    card.append(clear);
  } else {
    card.append(h("h2", "console-runs__empty-title", "No runs kept yet"));
    card.append(
      note(
        "Run a target and it shows up here. Every run is kept, and you never need its ref to find " +
          "it again. Try `magus run build` in this workspace, then Refresh.",
      ),
    );
  }
  box.append(card);
}

// openLink is the hand-off to the Log Viewer. A real <a href> rather than a click handler, so it
// carries every affordance a link has - middle-click, copy, open in a new tab - which is exactly
// what a reader comparing two runs wants.
function openLink(text: string, href: string): HTMLElement {
  const a = document.createElement("a");
  a.className = "console-runs__open";
  a.href = href;
  a.textContent = text;
  return a;
}

// viewerHref builds the Log Viewer deep link for one run: relative, so it works wherever the console
// is served (a daemon origin, the docs site under a base path, a dev port). The demo showcase
// carries its fragment through so a demo selection opens a demo run rather than reaching for a
// daemon that is not there.
//
// "logs/", NOT "../logs/". Every surface page ships with `<base href="../">` (see
// scripts/surface-stubs.mjs) so the shell's own relative assets resolve from /console/ - which means
// a link here already resolves against /console/, and the extra hop landed on /logs/ and 404'd.
function viewerHref(key: "inv" | "ref", value: string, demo: boolean): string {
  return "logs/#" + (demo ? "demo&" : "") + key + "=" + encodeURIComponent(value);
}
