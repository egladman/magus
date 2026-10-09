// main.ts - the console's Runs app: every run this workspace has kept, browsable without a ref.
//
// It exists because the Log Viewer's side panel answers "take me to the next run" well and "I do not
// know where to start" badly. A rail-width tree can only offer a filter BOX, which asks a reader to
// know the grammar before they can use it. A page has room for the inverse: a facet strip listing
// every value that actually occurs, with counts, one click each - and the click writes its term into
// the visible query box, so using it teaches the syntax for the times you do want to type.
//
// It is an INDEX, not a reader. Selecting a run shows what it did (its targets, outcomes, durations,
// refs); "Open output" goes to the Log Viewer through the #inv=/#ref= links it already
// understands. Neither app re-hosts the other's rendering, so they cannot drift.
//
// Data comes from the same two read-only server feeds the side panel reads (/api/v1/outputs and
// /api/v1/runs), joined on each output's invocation id, and the grouping/filtering/faceting is the
// shared pure module (logs/runindex.ts) both browsers consume.

import {
  buildFacets,
  buildRunRows,
  durText,
  matchesFilter,
  parseRunFilter,
  statusWord,
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
  adoptServerOrigin,
  getLiveToken,
  parseHash,
  probeServer,
  resolveServerHost,
  wantsDemo,
} from "../../lib/server";
import { subscribeDefaultHost } from "../../lib/settings";
import {
  renderConnectPrompt,
  type ConnectPromptState,
  type EmptyStateSlots,
} from "../../desktop/connectPrompt";
import { attachHelpPopover, createHelpButton } from "../../ui/help-popover";
import { REFRESH, svgGlyph } from "../../ui/glyph";
import { statusGlyph, statusMark, type Status } from "../../ui/status";
import { createFilterField, type FilterField } from "../../render/filterField";
import { timeEl } from "../../render/time";
import { h } from "../../desktop/view";
import type { AppInstance } from "../../desktop/standalone";

// FILTER_HELP is the one place the grammar is written down for a reader. The facets teach it by
// example; this is for the reader who wants the whole vocabulary at once.
const FILTER_HELP =
  "Terms combine with AND, case-insensitive. Free text matches the project, target, ref, error " +
  "and command line. Keys: project: target: status:pass|fail trigger: ref: cmd:. " +
  "Clicking a facet below writes its term here.";

// The width of the page, not the window, below which the facets fold behind a disclosure. It is the
// 40rem the container query in runs.css uses.
const FACETS_WIDE_PX = 640;

interface Refs {
  field: FilterField;
  count: HTMLElement;
  filters: HTMLDetailsElement;
  filtersSummary: HTMLElement;
  facets: HTMLElement;
  list: HTMLElement;
  detail: HTMLElement;
  body: HTMLElement;
  dispose: () => void;
}

// How each run outcome is drawn: the shared mark's status and the PF Label colour that carries the
// same word beside the run's name.
const OUTCOME: Record<"pass" | "fail" | "mixed", { mark: Status; label: string }> = {
  pass: { mark: "success", label: "pf-m-success" },
  fail: { mark: "danger", label: "pf-m-danger" },
  mixed: { mark: "warning", label: "pf-m-warning" },
};

// activate builds the app into host and returns the console's teardown handle. Everything below
// is per-activation, so reopening the tab is a clean slate.
export function activate(host: HTMLElement): AppInstance {
  adoptServerOrigin();
  const demo = wantsDemo(parseHash());
  let host_ = resolveServerHost(parseHash()) ?? "";
  // Set when both run feeds came back empty and nothing answered at the address either: fetchRuns
  // reads a refused connection as an empty list, and "nothing kept yet" would send the reader to run
  // a target.
  let unreachable = false;
  const token = getLiveToken();
  // The demo scenario is written RELATIVE to one instant, so it is stamped once; the "how long ago"
  // labels read the clock at paint time. Sharing one frozen value made every label a lie the moment
  // the page stopped being new - invisible while nothing refreshed, obvious the moment it does.
  const demoNow = Date.now();

  let runs: RunSummary[] = [];
  let logs: RunLog[] = [];
  let loaded = false;
  // The run the reader chose. What the page SHOWS is `shown` in paint(): the choice when it is still
  // listed, else the newest run, and the list's highlight and the detail pane both read that one
  // value so they cannot disagree.
  let selected: string | null = null;
  let stale = false;
  let visible = true;
  // Bumped by every load, so the answer from an address the reader has since moved off is dropped.
  let loadGeneration = 0;

  const refs = build(host, {
    onQuery: () => paint(),
    onRefresh: () => void load(),
    onBack: () => focusRow(),
  });
  // Built once and re-attached on each paint, so a repaint with the prompt already on screen keeps
  // the same buttons and the reader's focus.
  const promptSlots: EmptyStateSlots = {
    title: h("span", "pf-v6-c-empty-state__title-text console-runs__empty-title"),
    message: h("p", "console-runs__note"),
    actions: h("div", "pf-v6-c-empty-state__actions"),
  };
  promptSlots.actions.dataset.emptyWays = "";

  // focusRow moves focus to the selected row, the way back from the detail pane in a stacked layout
  // and what a repaint restores after replacing the buttons.
  function focusRow(): void {
    const row = refs.list.querySelector<HTMLElement>('[aria-current="true"]');
    row?.focus();
    row?.scrollIntoView({ block: "nearest" });
  }

  function paint(): void {
    const now = Date.now();
    const filter = parseRunFilter(refs.field.value());
    const byInv = new Map<string, RunLog>();
    for (const l of logs) byInv.set(l.inv, l);
    const kept = runs.filter((r) => matchesFilter(r, byInv.get(r.inv || ""), filter));
    const rows = buildRunRows(kept, logs, filter.empty);
    const shown = rows.find((r) => r.inv === selected) ?? rows[0];
    renderFacets(refs.facets, buildFacets(runs, logs, filter), (key, value) => {
      refs.field.setValue(toggleFilterTerm(refs.field.value(), key, value));
      paint();
    });
    refs.filtersSummary.textContent = filtersLabel(filter.keyed.length);
    if (rows.length) {
      renderList(refs.list, rows, now, shown?.inv ?? null, (inv) => {
        selected = inv;
        paint();
        if (stacked(refs.body)) {
          refs.detail.focus({ preventScroll: true });
          refs.detail.scrollIntoView({ block: "start", behavior: motion() });
        } else {
          focusRow();
        }
      });
    } else {
      renderEmpty(refs.list, {
        connection: connectPromptState(),
        promptSlots,
        onRetry: () => void load(),
        loaded,
        filtered: !filter.empty,
        onClear: () => {
          refs.field.setValue("");
          paint();
          refs.field.focus();
        },
      });
    }
    renderDetail(refs.detail, shown, now, demo);
    // "N of M" counts RUNS on both sides. M was the output count, which is larger and unrelated -
    // "2 runs of 31" beside a strip whose statuses summed to 28 is three numbers for two facts.
    const total = buildRunRows(runs, logs, true).length;
    refs.count.textContent = summary(rows, total, loaded, filter.empty);
    refs.field.setResults(
      !filter.empty && loaded ? rows.length + " of " + total : "",
      !filter.empty && loaded
        ? rows.length === 0
          ? "No runs match the filter"
          : rows.length + " of " + total + " runs match the filter"
        : "",
    );
  }

  function connectPromptState(): ConnectPromptState | null {
    if (demo) return null;
    if (!host_) return { connection: "none" };
    return unreachable ? { connection: "disconnected", host: host_ } : null;
  }

  async function load(): Promise<void> {
    const generation = ++loadGeneration;
    let nextRuns: RunSummary[] = [];
    let nextLogs: RunLog[] = [];
    let nextUnreachable = false;
    if (demo) {
      nextRuns = demoRuns(demoNow);
      nextLogs = demoRunLogs(demoNow);
    } else if (host_) {
      [nextRuns, nextLogs] = await Promise.all([
        fetchRuns(host_, token),
        fetchRunLogs(host_, token),
      ]);
      if (nextRuns.length === 0 && nextLogs.length === 0) {
        nextUnreachable = !(await probeServer(host_)).ok;
      }
    }
    // The tab closed, or a newer load (another address, a Retry) owns the page now.
    if (stale || generation !== loadGeneration) return;
    runs = nextRuns;
    logs = nextLogs;
    unreachable = nextUnreachable;
    loaded = true;
    syncWatch();
    paint();
  }

  // The list keeps itself current while the tab is on screen: a run you kick off in a terminal
  // appears here without anyone pressing Refresh, which is the difference between a page you check
  // and a page you leave open. The stream is closed while the tab is backgrounded - a stream per
  // hidden pane is a cost nobody asked for - and while nothing answers at the address, because the
  // prompt on screen promises that nothing retries behind it.
  let unwatch: (() => void) | null = null;
  function syncWatch(): void {
    const wanted = visible && !unreachable && host_ !== "";
    if (wanted && !unwatch) unwatch = watchRuns(host_, token, () => void load());
    if (!wanted && unwatch) {
      unwatch();
      unwatch = null;
    }
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
  syncWatch();
  void load();

  // Separate from the stream: the labels age whether or not anything runs, so the demo and an
  // offline page need this even though they never open a stream.
  let untick: (() => void) | null = tickRelativeTimes(host);
  // A new address is followed only while no runs are on screen: a list the reader is reading keeps
  // the server it came from until they refresh.
  const unsubscribeHost = subscribeDefaultHost(() => {
    if (runs.length > 0 || logs.length > 0) return;
    unwatch?.();
    unwatch = null;
    host_ = resolveServerHost(parseHash()) ?? "";
    unreachable = false;
    loaded = false;
    paint();
    syncWatch();
    void load();
  });
  return {
    setVisible: (nowVisible: boolean): void => {
      visible = nowVisible;
      if (visible && !untick) {
        untick = tickRelativeTimes(host);
        syncWatch();
        void load(); // whatever happened while this pane was hidden
        return;
      }
      if (!visible) {
        syncWatch();
        untick?.();
        untick = null;
      }
    },
    deactivate: () => {
      stale = true;
      unsubscribeHost();
      unwatch?.();
      unwatch = null;
      untick?.();
      untick = null;
      refs.dispose();
    },
  };
}

// motion is the scroll behaviour that respects a reader who asked for less of it.
function motion(): ScrollBehavior {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
}

// stacked reports whether the body has folded its two columns into one, which a container query in
// runs.css decides and the computed style reports back.
function stacked(body: HTMLElement): boolean {
  return getComputedStyle(body).flexDirection === "column";
}

function filtersLabel(active: number): string {
  return active > 0 ? "Filters (" + active + " applied)" : "Filters";
}

// build assembles the page: a toolbar over a facet strip over a two-column body (the runs, and the
// one you picked). The columns are plain flex children that stack when the PAGE is narrow - see
// runs.css.
function build(
  host: HTMLElement,
  on: { onQuery: () => void; onRefresh: () => void; onBack: () => void },
): Refs {
  const page = h("section", "console-runs");

  const bar = h("header", "console-runs__bar");
  bar.dataset.controlSize = "default";

  const field = createFilterField({
    label: "Filter runs",
    placeholder: "Filter runs, or pick a facet",
    onChange: () => on.onQuery(),
    debounceMs: 120,
  });
  field.el.classList.add("console-runs__field");

  // The shared help circle, identical to the graph explorer's and the log viewer's.
  const help = createHelpButton("Filter syntax");
  const disposeHelp = attachHelpPopover(help, { text: FILTER_HELP, label: "Filter syntax" });
  const search = h("div", "console-runs__search");
  search.append(field.el, help);

  const count = h("span", "console-runs__count");
  count.setAttribute("role", "status");
  const refresh = h("button", "pf-v6-c-button pf-m-secondary console-runs__refresh");
  refresh.setAttribute("type", "button");
  const refreshIcon = h("span", "pf-v6-c-button__icon pf-m-start");
  refreshIcon.append(svgGlyph(REFRESH, 14));
  refresh.append(refreshIcon, h("span", "pf-v6-c-button__text", "Refresh"));
  refresh.addEventListener("click", on.onRefresh);
  bar.append(h("h1", "console-runs__label", "Runs"), search, count, refresh);

  // The facets are a strip under the toolbar, not a third column. They are a control for the query
  // box directly above them; a column gave that weight it does not carry. In a narrow page they fold
  // behind a disclosure so the list is not pushed off the screen by a dozen chips.
  const filters = document.createElement("details");
  filters.className = "console-runs__filters";
  filters.open = true;
  const filtersSummary = h("summary", "console-runs__filters-summary", "Filters");
  const facets = h("div", "console-runs__facets");
  filters.append(filtersSummary, facets);

  const body = h("div", "console-runs__body");
  const list = h("ul", "pf-v6-c-data-list pf-m-compact console-runs__list");
  list.setAttribute("role", "list");
  list.setAttribute("aria-label", "Runs");
  const detail = h("div", "console-runs__detail");
  detail.setAttribute("role", "region");
  detail.setAttribute("aria-label", "Run details");
  detail.tabIndex = -1;
  body.append(list, detail);

  page.append(bar, filters, body);
  host.replaceChildren(page);

  // The disclosure is open and summary-less on a wide page; on a narrow one it closes when the page
  // crosses the line, and a reader who then opens it keeps it open until the next crossing.
  let narrowNow = false;
  let observer: ResizeObserver | null = null;
  if (typeof ResizeObserver !== "undefined") {
    observer = new ResizeObserver((entries) => {
      const width = entries[entries.length - 1]?.contentRect.width ?? 0;
      if (width === 0) return;
      const narrow = width < FACETS_WIDE_PX;
      if (narrow === narrowNow) return;
      narrowNow = narrow;
      filters.open = !narrow;
    });
    observer.observe(page);
  }

  // Back to the list, for the stacked layout where the detail is below the fold.
  detail.addEventListener("click", (ev) => {
    if (ev.target instanceof Element && ev.target.closest(".console-runs__back")) on.onBack();
  });

  return {
    field,
    count,
    filters,
    filtersSummary,
    facets,
    list,
    detail,
    body,
    dispose: () => {
      field.dispose();
      disposeHelp();
      observer?.disconnect();
    },
  };
}

// renderFacets paints the strip: each facet is a labelled group of clickable PF Labels, each with a
// Badge for how many runs clicking it would leave. Every value shown occurs in the data, so nothing
// here can lead to an empty list by surprise.
function renderFacets(
  box: HTMLElement,
  facets: Facet[],
  onPick: (key: string, value: string) => void,
): void {
  box.replaceChildren();
  if (!facets.length) return;
  for (const facet of facets) {
    const group = h("div", "console-runs__facet");
    group.setAttribute("role", "group");
    const headId = "console-runs-facet-" + facet.key;
    group.setAttribute("aria-labelledby", headId);
    const head = h("span", "console-runs__facet-head", facet.label);
    head.id = headId;
    group.append(head);
    for (const v of facet.values) {
      // A clickable PF Label: the whole chip is the button. Outline when off, filled and ticked when
      // on, so the state is a shape and not only a hue; the status values also carry their mark.
      const chip = h(
        "span",
        "pf-v6-c-label pf-m-clickable console-runs__facet-value" +
          (v.active ? "" : " pf-m-outline"),
      );
      if (facet.key === "status")
        chip.classList.add(v.value === "fail" ? "pf-m-danger" : "pf-m-success");
      const b = h("button", "pf-v6-c-label__content");
      b.setAttribute("type", "button");
      b.setAttribute("aria-pressed", String(v.active));
      if (v.active) chip.classList.add("pf-m-selected");
      b.title = (v.active ? "Remove " : "Add ") + facet.key + ":" + v.value;
      if (facet.key === "status") {
        const mark = h("span", "pf-v6-c-label__icon");
        mark.append(statusGlyph(v.value === "fail" ? "danger" : "success"));
        b.append(mark);
      } else if (v.active) {
        const tick = h("span", "pf-v6-c-label__icon");
        tick.append(statusGlyph("success"));
        b.append(tick);
      }
      b.append(h("span", "pf-v6-c-label__text console-runs__facet-label", v.label));
      b.append(h("span", "pf-v6-c-badge pf-m-read console-runs__facet-count", String(v.count)));
      b.addEventListener("click", () => onPick(facet.key, v.value));
      chip.append(b);
      group.append(chip);
    }
    box.append(group);
  }
}

// renderList paints one row per run: the command that produced it, its outcome, when, how long, and
// how many targets it kept output for. The command is the row's identity because it is what a
// person remembers a run by - the id is machine vocabulary and sits in the detail pane instead.
//
// The list is a roving-tabindex group: one row is a Tab stop and the arrow keys move between rows, so
// a long history is not a wall of tab stops. A repaint (the feed refreshes on its own) restores focus
// to the row that held it, since the buttons are rebuilt.
function renderList(
  box: HTMLElement,
  rows: RunRow[],
  now: number,
  selected: string | null,
  onPick: (inv: string) => void,
): void {
  const focused =
    box.contains(document.activeElement) && document.activeElement instanceof HTMLElement
      ? document.activeElement.dataset.inv
      : undefined;
  box.replaceChildren();
  const buttons: HTMLButtonElement[] = [];
  for (const row of rows) {
    const item = h("li", "pf-v6-c-data-list__item pf-m-clickable");
    const b = h("button", "console-runs__row");
    b.setAttribute("type", "button");
    b.dataset.inv = row.inv;
    const current = row.inv === selected;
    if (current) {
      item.classList.add("pf-m-selected");
      b.setAttribute("aria-current", "true");
    }
    b.tabIndex = current ? 0 : -1;
    const head = h("span", "console-runs__row-head");
    // A run with no recorded outcome (interrupted before it wrote one) still gets a mark, so the
    // command column starts on the same x for every row.
    head.append(
      row.status
        ? statusMark(OUTCOME[row.status].mark, statusWord(row.status))
        : statusMark("neutral", "No outcome recorded"),
    );
    head.append(h("span", "console-runs__row-cmd", row.command));
    const meta = h("span", "console-runs__row-meta");
    // The "how long ago" half is its own <time> carrying the instant, so tickRelativeTimes can
    // advance it in place; the rest of the line never changes and is static beside it.
    const parts: (string | HTMLElement)[] = [];
    if (row.startMs) parts.push(timeEl(row.startMs, now));
    if (row.durationMs) parts.push(durText(row.durationMs));
    parts.push(row.outputs.length + (row.outputs.length === 1 ? " target" : " targets"));
    if (row.trigger) parts.push(row.trigger);
    for (const part of parts) {
      meta.append(typeof part === "string" ? h("span", "", part) : part);
    }
    b.append(head, meta);
    b.addEventListener("click", () => onPick(row.inv));
    item.append(b);
    box.append(item);
    buttons.push(b);
  }
  if (!buttons.some((b) => b.tabIndex === 0) && buttons[0]) buttons[0].tabIndex = 0;
  if (focused) buttons.find((b) => b.dataset.inv === focused)?.focus({ preventScroll: true });
  box.onkeydown = (ev: KeyboardEvent) => {
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    if (at < 0) return;
    let next = at;
    if (ev.key === "ArrowDown") next = Math.min(buttons.length - 1, at + 1);
    else if (ev.key === "ArrowUp") next = Math.max(0, at - 1);
    else if (ev.key === "Home") next = 0;
    else if (ev.key === "End") next = buttons.length - 1;
    else return;
    ev.preventDefault();
    for (const b of buttons) b.tabIndex = -1;
    buttons[next].tabIndex = 0;
    buttons[next].focus();
  };
}

// renderDetail shows what the selected run DID: every target it kept output for, with the outcome,
// duration and ref, each one step from its captured output in the Log Viewer.
function renderDetail(box: HTMLElement, row: RunRow | undefined, now: number, demo: boolean): void {
  box.replaceChildren();
  if (!row) return;
  // The way back to the list, shown only when the page is narrow enough to stack the columns.
  const back = h("button", "pf-v6-c-button pf-m-link console-runs__back", "Back to runs");
  back.setAttribute("type", "button");
  box.append(back);
  const head = h("div", "console-runs__detail-head");
  const title = h("div", "console-runs__detail-title");
  title.append(h("h2", "console-runs__detail-cmd", row.command));
  if (row.status) title.append(statusLabel(row.status));
  head.append(title);
  // A LABELLED list, not a "·"-joined sentence. Run together, the five facts read as one long string
  // a reader has to parse before they can find the one they came for; labelled, each is a lookup.
  head.append(
    facts(
      [
        { label: "When", time: row.startMs },
        { label: "Duration", text: row.durationMs ? durText(row.durationMs) : "" },
        { label: "Trigger", text: row.trigger },
        { label: "magus", text: row.log?.magus_version ?? "" },
        { label: "Run id", text: row.inv },
      ],
      now,
    ),
  );
  // The whole run opens as one journal, which is the reading a target list cannot give: the order
  // things ran in, and the waterfall over them.
  if (row.log) {
    head.append(
      openLink(
        "Open the whole run",
        viewerHref("inv", row.inv, demo),
        "Open the whole run: " + row.command,
      ),
    );
  }
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
  const table = h("ul", "console-runs__targets");
  table.setAttribute("role", "list");
  for (const o of row.outputs) {
    const item = h("li", "console-runs__target");
    const name = (o.project && o.project !== "." ? o.project + ":" : "") + o.target;
    const line = h("div", "console-runs__target-line");
    line.append(
      statusMark(o.failed ? "danger" : "success", o.failed ? "Failed" : "Passed"),
      h("span", "console-runs__target-name", name),
      h("span", "console-runs__target-dur", durText(o.duration_ms)),
    );
    item.append(line);
    // Everything under the name is INDENTED to the name, so a target and its ref, error and link
    // read as one block rather than as four unrelated lines stacked down the pane.
    const under = h("div", "console-runs__target-body");
    under.append(h("code", "console-runs__target-ref", o.ref));
    if (o.error) under.append(h("p", "console-runs__target-error", o.error));
    under.append(openLink("Open output", viewerHref("ref", o.ref, demo), "Open output of " + name));
    item.append(under);
    table.append(item);
  }
  box.append(table);
}

// statusLabel states the outcome as a PF status Label: an icon, a colour and the word. The dot it
// replaces carried the whole fact in hue, which is the one thing a reader who cannot separate red
// from green does not get.
function statusLabel(status: "pass" | "fail" | "mixed"): HTMLElement {
  const out = OUTCOME[status];
  const label = h("span", "pf-v6-c-label console-runs__pill " + out.label);
  label.dataset.status = status;
  const content = h("span", "pf-v6-c-label__content");
  const icon = h("span", "pf-v6-c-label__icon");
  icon.append(statusGlyph(out.mark));
  content.append(icon, h("span", "pf-v6-c-label__text", statusWord(status)));
  label.append(content);
  return label;
}

// facts renders the labelled metadata list. A row whose value is empty is DROPPED rather than shown
// blank: a run whose journal aged out genuinely has no trigger or version, and an empty cell beside
// a label reads as a failure to load rather than as an absence.
function facts(rows: { label: string; text?: string; time?: number }[], now: number): HTMLElement {
  const dl = h("dl", "console-runs__facts");
  for (const r of rows) {
    if (r.time) {
      dl.append(h("dt", "console-runs__fact-label", r.label));
      const dd = h("dd", "console-runs__fact-value", new Date(r.time).toLocaleString());
      // A relative gloss ages like every other one on the page, so it carries its instant for
      // tickRelativeTimes rather than sitting frozen beside a clock time that never lies.
      dd.append(timeEl(r.time, now, "console-runs__fact-extra"));
      dl.append(dd);
      continue;
    }
    if (!r.text) continue;
    dl.append(h("dt", "console-runs__fact-label", r.label));
    dl.append(h("dd", "console-runs__fact-value", r.text));
  }
  return dl;
}

function note(...parts: (string | Node)[]): HTMLElement {
  const p = h("p", "console-runs__note");
  p.append(...parts);
  return p;
}

// cmd is a command named inside a sentence. An element rather than backticks in the string, because
// backticks in a DOM text node are just backticks on screen.
function cmd(text: string): HTMLElement {
  return h("code", "console-runs__cmd", text);
}

// spinner is PF's Spinner with a name, for a load the reader is waiting on.
function spinner(label: string): SVGElement {
  const svg = statusGlyph("running");
  svg.setAttribute("class", "pf-v6-c-spinner pf-m-lg");
  svg.setAttribute("role", "progressbar");
  svg.setAttribute("aria-label", label);
  svg.removeAttribute("aria-hidden");
  return svg;
}

// emptyCard is PF's EmptyState around a title, a body and any actions.
function emptyCard(title: HTMLElement, body: (string | Node)[], actions: Node[] = []): HTMLElement {
  const card = h("div", "pf-v6-c-empty-state pf-m-sm console-runs__empty");
  const content = h("div", "pf-v6-c-empty-state__content");
  const header = h("div", "pf-v6-c-empty-state__header");
  const heading = h("h2", "pf-v6-c-empty-state__title");
  heading.append(title);
  header.append(heading);
  content.append(header);
  const text = h("div", "pf-v6-c-empty-state__body");
  text.append(...body);
  content.append(text);
  if (actions.length > 0) {
    const footer = h("div", "pf-v6-c-empty-state__footer");
    const group = h("div", "pf-v6-c-empty-state__actions");
    group.append(...actions);
    footer.append(group);
    content.append(footer);
  }
  card.append(content);
  return card;
}

function emptyTitle(text: string): HTMLElement {
  return h("span", "pf-v6-c-empty-state__title-text console-runs__empty-title", text);
}

// renderEmpty distinguishes the four ways this list can be empty, because only one of them is the
// reader's to fix and the others read as data loss if given the same words. A filtered-to-nothing
// list gets a CONTROL, not just a sentence: the way out of an over-narrow query should not require
// selecting text in a box.
function renderEmpty(
  box: HTMLElement,
  s: {
    connection: ConnectPromptState | null;
    promptSlots: EmptyStateSlots;
    onRetry: () => void;
    loaded: boolean;
    filtered: boolean;
    onClear: () => void;
  },
): void {
  box.replaceChildren();
  const item = h("li", "console-runs__empty-item");
  if (s.connection) {
    renderConnectPrompt(s.promptSlots, s.connection, {
      purpose: "This page reads the runs your local server has kept.",
      onRetry: s.onRetry,
    });
    item.append(emptyCard(s.promptSlots.title, [s.promptSlots.message], [s.promptSlots.actions]));
  } else if (!s.loaded) {
    item.append(emptyCard(emptyTitle("Loading runs"), [spinner("Loading runs")]));
  } else if (s.filtered) {
    const clear = h("button", "pf-v6-c-button pf-m-secondary", "Clear filter");
    clear.setAttribute("type", "button");
    clear.addEventListener("click", s.onClear);
    item.append(
      emptyCard(
        emptyTitle("No runs match"),
        ["Nothing here carries every term in the filter."],
        [clear],
      ),
    );
  } else {
    item.append(
      emptyCard(emptyTitle("No runs kept yet"), [
        "Run a target and it shows up here. Every run is kept, and you never need its ref to find it again. Try ",
        cmd("magus run build"),
        " in this workspace, then Refresh.",
      ]),
    );
  }
  box.append(item);
}

// openLink goes to the Log Viewer. A real <a href> rather than a click handler, so it carries every
// affordance a link has - middle-click, copy, open in a new tab - which is exactly what a reader
// comparing two runs wants. The name says which output, since a column of "Open output" links is
// indistinguishable to a reader that lists links.
function openLink(text: string, href: string, name: string): HTMLElement {
  const a = document.createElement("a");
  a.className = "pf-v6-c-button pf-m-link console-runs__open";
  a.href = href;
  a.textContent = text;
  a.setAttribute("aria-label", name);
  return a;
}

// viewerHref builds the Log Viewer deep link for one run: relative, so it works wherever the console
// is served (a server origin, the docs site under a base path, a dev port). The demo showcase
// carries its fragment through so a demo selection opens a demo run rather than reaching for a
// server that is not there.
//
// "logs/", NOT "../logs/". Every app page ships with `<base href="../">` (see
// scripts/app-stubs.mjs) so the shell's own relative assets resolve from /console/ - which means
// a link here already resolves against /console/, and the extra hop landed on /logs/ and 404'd.
function viewerHref(key: "inv" | "ref", value: string, demo: boolean): string {
  return "logs/#" + (demo ? "demo&" : "") + key + "=" + encodeURIComponent(value);
}
