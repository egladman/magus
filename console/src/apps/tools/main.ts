// main.ts - the console's Tools app: every binary this workspace's spells drive, the version
// each reported, the window it is held to and where it stands in its release line.
//
// It is the table the Dashboard's Toolchain tile used to be, given room: the tile keeps the counts
// and opens this. The filters answer the three questions a reader comes with (what is past end of
// life, what has upstream not scheduled, what has no one pinned) and combine, so each toggle names
// how many rows it would leave.
//
// Data is one ListTools read, polled on the operator's refresh rate. Behind it the server forks a
// version probe per tool and caches the answer, so the Probed column shows the reading's age.

import {
  adoptServerOrigin,
  getLiveToken,
  parseHash,
  resolveServerHost,
  wantsDemo,
} from "../../lib/server";
import { getPollMs, subscribeDefaultHost } from "../../lib/settings";
import {
  renderConnectPrompt,
  type ConnectPromptState,
  type EmptyStateSlots,
} from "../../desktop/connectPrompt";
import { REFRESH, svgGlyph } from "../../ui/glyph";
import { inlineAlert } from "../../ui/alert";
import { h } from "../../desktop/view";
import type { AppInstance } from "../../desktop/standalone";
import type { ToolsView } from "../dashboard/state";
import { SortableTable } from "../../ui/table";
import { demoToolsView } from "./demo";
import {
  FILTERS,
  age,
  applyFilters,
  columns,
  lifecycleNote,
  lifecycleSource,
  toolCounts,
  type ToolFilterKey,
} from "./model";
import { fetchToolsView } from "./transport";

const NO_TOOLS =
  "No project declares a probed tool. A spell declares what its ops need with supported; a project declares its own policy with the tools key.";
const NO_MATCH = "No tool matches every active filter.";

// activate builds the app into host and returns the console's teardown handle. Everything below
// is per-activation, so reopening the tab is a clean slate.
export function activate(host: HTMLElement): AppInstance {
  adoptServerOrigin();
  const demo = wantsDemo(parseHash());
  let server = resolveServerHost(parseHash()) ?? "";
  const token = getLiveToken();

  let view: ToolsView | null = null;
  let loaded = false;
  // Set when a read failed with nothing on screen. The server transport has already told the reader
  // why; this decides between the connect prompt and a table.
  let unreachable = false;
  // Set when a read failed while a table was on screen: the rows are still the last good reading,
  // and the page says how old it is rather than passing it off as current.
  let refreshFailed = false;
  let readAtMs = 0;
  let stale = false;
  let visible = true;
  // Bumped by every load, so the answer from an address the reader has since moved off is dropped.
  let loadGeneration = 0;
  const active = new Set<ToolFilterKey>();

  const table = new SortableTable(columns(), {
    label: "Tools",
    sortKey: "bin",
    emptyText: "Loading tools...",
  });
  const refs = build(host, table, {
    onToggle: (key) => {
      if (!active.delete(key)) active.add(key);
      paint();
    },
    onRefresh: () => void load(),
  });

  function connectPromptState(): ConnectPromptState | null {
    if (demo || view) return null;
    if (!server) return { connection: "none" };
    return unreachable ? { connection: "disconnected", host: server } : null;
  }

  function paint(): void {
    const rows = view?.rows ?? [];
    const prompt = connectPromptState();
    refs.prompt.hidden = prompt === null;
    refs.table.hidden = prompt !== null;
    if (prompt) {
      renderConnectPrompt(refs.promptSlots, prompt, {
        purpose: "Tools reads the binaries your local server probes for this workspace.",
        onRetry: () => void load(),
      });
    }
    const shown = applyFilters(rows, active);
    table.setEmptyText(!loaded ? "Loading tools..." : rows.length === 0 ? NO_TOOLS : NO_MATCH);
    table.setRows(shown);

    for (const f of FILTERS) {
      const btn = refs.filters.get(f.key);
      if (!btn) continue;
      const on = active.has(f.key);
      btn.setAttribute("aria-pressed", String(on));
      btn.classList.toggle("pf-m-selected", on);
      const text = btn.querySelector(".pf-v6-c-toggle-group__text");
      if (text) text.textContent = f.label + " (" + rows.filter(f.match).length + ")";
    }
    refs.count.textContent = summary(shown.length, rows.length, view);
    const source = lifecycleSource(view?.lifecycle);
    refs.note.textContent = [lifecycleNote(view?.lifecycle), source && "end of life from " + source]
      .filter(Boolean)
      .join("; ");

    refs.stale.replaceChildren();
    if (refreshFailed && view) {
      refs.stale.append(
        inlineAlert({
          variant: "warning",
          title: "Could not refresh the tools",
          body:
            "Showing the reading from " +
            age(readAtMs, Date.now()) +
            ". The page retries on its refresh rate; Refresh tries now.",
        }),
      );
    }
  }

  async function load(): Promise<void> {
    const generation = ++loadGeneration;
    let next: ToolsView | null = null;
    if (demo) next = demoToolsView(Date.now());
    else if (server) next = await fetchToolsView(server, token);
    // The tab closed, or a newer load (another address, a Retry) owns the page now.
    if (stale || generation !== loadGeneration) return;
    // A failed read keeps what is on screen: the next poll retries.
    if (next) {
      view = next;
      readAtMs = Date.now();
    }
    refreshFailed = !next && view !== null && !demo;
    unreachable = !next && !view && !demo && server !== "";
    loaded = true;
    syncPoll();
    paint();
  }

  // The reading ages whether or not anything changes, so the page re-reads while it is on screen.
  // It stops while hidden and while nothing answers at the address, because the prompt on screen
  // promises that nothing retries behind it.
  let timer: ReturnType<typeof setInterval> | null = null;
  function syncPoll(): void {
    const wanted = visible && !unreachable && !demo && server !== "";
    if (wanted && !timer) timer = setInterval(() => void load(), getPollMs());
    if (!wanted && timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  paint();
  void load();
  // A new address is followed only while no tools are on screen: a table the reader is reading
  // keeps the server it came from until they refresh.
  const unsubscribeHost = subscribeDefaultHost(() => {
    if (view) return;
    server = resolveServerHost(parseHash()) ?? "";
    unreachable = false;
    loaded = false;
    paint();
    void load();
  });
  return {
    setVisible: (nowVisible: boolean): void => {
      const revealed = nowVisible && !visible;
      visible = nowVisible;
      syncPoll();
      if (revealed) void load(); // whatever changed while this pane was hidden
    },
    deactivate: () => {
      stale = true;
      unsubscribeHost();
      if (timer) clearInterval(timer);
      timer = null;
    },
  };
}

// summary is the bar's one phrase. Zero outside-window is said outright: silence reads as
// unchecked. "Version window" because the end-of-life filter beside it is a different question: a
// tool can sit inside its window and still be past end of life.
function summary(shown: number, total: number, view: ToolsView | null): string {
  if (!view) return "";
  const outside = toolCounts(view.rows).outsideWindow;
  const scope = shown === total ? String(total) : shown + " of " + total;
  const tools = scope + (total === 1 ? " tool" : " tools");
  return (
    tools + ", " + (outside > 0 ? outside + " outside version window" : "all within version window")
  );
}

interface Refs {
  filters: Map<ToolFilterKey, HTMLButtonElement>;
  count: HTMLElement;
  note: HTMLElement;
  stale: HTMLElement;
  table: HTMLElement;
  prompt: HTMLElement;
  promptSlots: EmptyStateSlots;
}

// build assembles the page: a bar of filters over the table, with the connect prompt standing in
// for the table while there is no server to read.
function build(
  host: HTMLElement,
  table: { el: HTMLElement },
  on: { onToggle: (key: ToolFilterKey) => void; onRefresh: () => void },
): Refs {
  const page = h("section", "console-tools");
  page.setAttribute("aria-label", "Tools");
  page.append(h("h1", "pf-v6-screen-reader", "Tools"));

  const bar = h("header", "console-tools__bar");
  bar.dataset.controlSize = "default";
  // A PF toggle group in its multi-select form: each button is its own on/off, and they combine.
  const group = h("div", "pf-v6-c-toggle-group console-tools__filters");
  group.setAttribute("role", "group");
  group.setAttribute("aria-label", "Filter tools");
  const filters = new Map<ToolFilterKey, HTMLButtonElement>();
  for (const f of FILTERS) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button console-tools__filter");
    btn.type = "button";
    btn.setAttribute("aria-pressed", "false");
    btn.append(h("span", "pf-v6-c-toggle-group__text", f.label));
    btn.addEventListener("click", () => on.onToggle(f.key));
    item.append(btn);
    group.append(item);
    filters.set(f.key, btn);
  }
  const count = h("span", "console-tools__count");
  count.setAttribute("role", "status");
  count.setAttribute("aria-live", "polite");
  const refresh = h("button", "pf-v6-c-button pf-m-secondary console-tools__refresh");
  refresh.setAttribute("type", "button");
  const refreshIcon = h("span", "pf-v6-c-button__icon pf-m-start");
  refreshIcon.append(svgGlyph(REFRESH, 14));
  refresh.append(refreshIcon, h("span", "pf-v6-c-button__text", "Refresh"));
  refresh.addEventListener("click", on.onRefresh);
  bar.append(group, count, refresh);

  const stale = h("div", "console-tools__stale");
  const note = h("p", "console-tools__note");
  note.setAttribute("role", "status");
  const tableBox = h("div", "console-tools__table");
  tableBox.append(table.el);

  const prompt = h("div", "pf-v6-c-empty-state console-tools__empty");
  prompt.hidden = true;
  const content = h("div", "pf-v6-c-empty-state__content");
  const header = h("div", "pf-v6-c-empty-state__header");
  const titleBox = h("div", "pf-v6-c-empty-state__title");
  const promptSlots: EmptyStateSlots = {
    title: h("h2", "pf-v6-c-empty-state__title-text"),
    message: h("div", "pf-v6-c-empty-state__body"),
    actions: h("div", "pf-v6-c-empty-state__actions"),
  };
  promptSlots.actions.dataset.emptyWays = "";
  titleBox.append(promptSlots.title);
  header.append(titleBox);
  content.append(header, promptSlots.message, promptSlots.actions);
  prompt.append(content);

  page.append(bar, stale, note, tableBox, prompt);
  host.replaceChildren(page);
  return { filters, count, note, stale, table: tableBox, prompt, promptSlots };
}
