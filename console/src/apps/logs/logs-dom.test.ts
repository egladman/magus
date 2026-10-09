// logs-dom.test.ts - what the log viewer puts on screen. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests.
//
// What is pinned HERE is what the audit found wrong and this change fixed:
//
//   - A SECTION HEAD IS NOT A BUTTON HOLDING BUTTONS. The fold toggle and the section's actions are
//     siblings, the toggle names what it controls, and the actions are reachable by keyboard.
//   - NOTHING IS COLOUR ALONE. A tree row's outcome and a waterfall bar's result are words (and, for
//     a cached bar, a pattern), and the drawing has a table twin for a reader that cannot see it.
//   - THE COLD SCREEN SAYS ONE THING. With nothing loaded there is no toolbar, and the empty state
//     is the shared connect prompt rather than a hint that contradicts the panel beside it.
//   - A RUN IS NAMED BY ITS COMMAND. The tab title is what a person typed, not an invocation id.
//   - EVERY FAILURE REACHES THE PERSON. A load failure is a toast as well as a strip.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test as nodeTest, type TestContext } from "node:test";
import { INV_CI } from "../../desktop/demo-scenario";
import { must } from "../../lib/guards";
import { NOTIFY_EVENT, type NotifyInput } from "../../lib/notifications";
import { activate, deactivate, docTitle } from "./main";
import { bindRefSlot, resolveDom, setRefIdentity, setStatus } from "./dom";
import { demoJournal } from "./demo";
import { applyFilterFromInput, mountFilterField } from "./filter";
import { render } from "./render";
import { buildModelMulti } from "./model";
import { buildRunTree, parseRunFilter } from "./runindex";
import { demoRunLogs, demoRuns, mountCollapsiblePanel, renderRunTree } from "./runtree";
import { state, waterfallSource } from "./state";
import { renderWaterfall } from "./waterfall";

const NOW = Date.now();
const notices: NotifyInput[] = [];
const onNotify = (e: Event): void => {
  notices.push((e as CustomEvent<NotifyInput>).detail);
};

// There is no file-level beforeEach on purpose: the DOM suite runs with
// --experimental-test-isolation=none, so a hook declared at file scope fires for every *-dom test in
// the process, siblings included. Each test resets what it touches through this wrapper instead.
function test(name: string, fn: (t: TestContext) => void | Promise<void>): void {
  nodeTest(name, async (t) => {
    document.body.replaceChildren();
    notices.length = 0;
    document.addEventListener(NOTIFY_EVENT, onNotify);
    state.timeline = false;
    state.pretty = true;
    state.model = null;
    state.currentJournal = null;
    state.currentJournals = null;
    state.focusWin = null;
    state.filterQuery = "";
    state.filterParsed = { groups: [], texts: [], empty: true };
    t.after(() => {
      document.removeEventListener(NOTIFY_EVENT, onNotify);
      location.hash = "";
      document.body.replaceChildren();
    });
    await fn(t);
  });
}

// panel is the smallest scaffold the renderers need: a body to fill, a status host, and the chip row.
function panel(): void {
  document.body.innerHTML =
    '<section class="console-render-panel" id="log-panel">' +
    '<div id="filter-chips" hidden></div><div id="log-status" hidden></div>' +
    '<div class="console-render-scroll" id="log-scroll">' +
    '<div class="console-render-body" id="log-body"></div><div id="log-empty"></div>' +
    "</div></section>";
  resolveDom();
}

function loadCi(): void {
  state.currentJournal = must(demoJournal(INV_CI));
  state.currentJournals = null;
  const built = buildModelMulti(waterfallSource());
  state.model = { sections: built.sections, titled: built.titled };
  state.rawLines = built.rawLines;
}

function text(el: Element | null): string {
  return (el?.textContent ?? "").trim();
}

test("a section head holds a toggle and its actions side by side, never one inside the other", () => {
  panel();
  loadCi();
  render();

  const heads = [...document.querySelectorAll<HTMLElement>(".console-render-section__head")];
  assert.ok(heads.length > 0, "the run has sections");
  assert.equal(document.querySelectorAll("button button").length, 0, "no button contains a button");
  for (const head of heads) {
    const toggle = must(
      head.querySelector<HTMLElement>(":scope > .console-render-section__toggle"),
    );
    assert.equal(toggle.tagName, "BUTTON");
    const lines = must(head.nextElementSibling);
    assert.equal(toggle.getAttribute("aria-controls"), lines.id, "the toggle names what it folds");
    const actions = must(head.querySelector(":scope > .console-render-section__actions"));
    assert.equal(actions.contains(toggle), false);
    assert.equal(actions.getAttribute("role"), "group");
    const labels = [...actions.querySelectorAll("button")].map((b) => text(b));
    assert.equal(labels[0], "Copy", "sentence case, the same voice as the toolbar");
  }

  const section = must(heads[0].parentElement);
  const toggle = must(heads[0].querySelector<HTMLElement>(".console-render-section__toggle"));
  const was = toggle.getAttribute("aria-expanded");
  toggle.click();
  assert.notEqual(toggle.getAttribute("aria-expanded"), was);
  assert.equal(section.hasAttribute("data-collapsed"), was === "true");
});

test("the waterfall names every result in words and has a table twin", () => {
  panel();
  loadCi();
  state.timeline = true;
  renderWaterfall();

  const svg = must(document.querySelector<SVGElement>(".console-log-waterfall__svg"));
  assert.equal(svg.getAttribute("aria-hidden"), "true", "the table is the accessible reading");
  assert.equal(svg.getAttribute("role"), null);

  const table = must(document.querySelector<HTMLTableElement>(".pf-v6-screen-reader table"));
  assert.match(text(table.querySelector("caption")), /Targets and steps/);
  const rows = [...table.querySelectorAll("tbody tr")];
  assert.ok(rows.length > 0);
  const words = rows.map((r) => text(r.querySelector("th")));
  assert.ok(
    words.some((w) => w.startsWith("Step: ")),
    "steps are rows too",
  );

  const bars = [...svg.querySelectorAll<SVGElement>(".console-log-waterfall__bar[data-status]")];
  assert.ok(bars.length > 0);
  const labelled = [...svg.querySelectorAll<SVGElement>(".console-log-waterfall__barlabel")].map(
    (l) => l.getAttribute("data-status"),
  );
  for (const bar of bars) {
    assert.ok(
      labelled.includes(bar.getAttribute("data-status")),
      "a " + bar.getAttribute("data-status") + " bar has its result written beside or in it",
    );
  }
  const cached = svg.querySelector<SVGElement>('.console-log-waterfall__bar[data-status="cached"]');
  if (cached) {
    assert.match(cached.getAttribute("fill") ?? "", /^url\(#console-wf-hatch-/);
    assert.ok(svg.querySelector("pattern"), "and the pattern it names exists");
  }
});

test("a filter that matches nothing offers a way back", () => {
  panel();
  loadCi();
  mountFilterField(must(document.body.appendChild(document.createElement("div"))));
  applyFilterFromInput("zzz-matches-nothing");

  const note = must(document.querySelector<HTMLElement>(".console-log-filter__empty"));
  assert.match(text(note), /No lines match the filter/);
  must(note.querySelector<HTMLElement>("button")).click();
  assert.equal(state.filterQuery, "", "Clear filter clears it");
  assert.equal(document.querySelector(".console-log-filter__empty"), null);
});

test("a failure is a strip with role=alert and a toast", () => {
  panel();
  setStatus("Could not decode the log: bad gzip.", true);

  const alert = must(document.querySelector<HTMLElement>("#log-status [role=alert]"));
  assert.match(text(alert), /Could not decode the log/);
  assert.ok(
    notices.some((n) => n.kind === "error" && n.toast === true),
    "the same failure is raised as a toast, since the strip is only seen while this tab is",
  );

  setStatus("");
  assert.equal(must(document.getElementById("log-status")).hidden, true);
});

test("a tree row's outcome is a word, selection is aria-selected, and the tree is named", () => {
  const box = document.createElement("div");
  document.body.append(box);
  const specs = buildRunTree({
    runs: demoRuns(NOW),
    logs: demoRunLogs(NOW),
    mode: "runs",
    filter: parseRunFilter(""),
    now: NOW,
  });
  const treeState = { expanded: new Set<string>(), current: null as string | null };
  const picked: string[] = [];
  renderRunTree(box, specs, treeState, (sel) => picked.push(sel.kind));

  const tree = must(box.querySelector<HTMLElement>('[role="tree"]'));
  assert.equal(tree.getAttribute("aria-label"), "Recent runs");
  assert.ok(must(tree.parentElement).classList.contains("pf-m-compact"));
  assert.ok(must(tree.parentElement).classList.contains("pf-m-truncate"));
  assert.equal(
    [...tree.querySelectorAll<HTMLElement>(".pf-v6-c-tree-view__node")].filter(
      (n) => n.tabIndex === 0,
    ).length,
    1,
    "one Tab stop",
  );

  const withStatus = [...tree.querySelectorAll<HTMLElement>("li")].filter((li) =>
    li.querySelector(".pf-v6-c-tree-view__node-title [data-status]"),
  );
  assert.ok(withStatus.length > 0);
  for (const li of withStatus) {
    const mark = must(
      li.querySelector<HTMLElement>(":scope > .pf-v6-c-tree-view__content [data-status]"),
    );
    assert.match(text(mark), /Passed|Failed|Partly failed/, "the outcome is in the row's text");
    assert.ok(mark.querySelector(".pf-v6-c-icon"), "with a shape");
  }

  const selectable = must(
    [...tree.querySelectorAll<HTMLElement>("li[aria-selected]")].find(
      (li) => li.getAttribute("aria-selected") === "false",
    ),
  );
  must(selectable.querySelector<HTMLElement>(".pf-v6-c-tree-view__node")).click();
  assert.equal(selectable.getAttribute("aria-selected"), "true");
  assert.equal(tree.querySelectorAll('li[aria-selected="true"]').length, 1);
  assert.deepEqual(picked.length, 1);
});

// The run browser floats over the content in a narrow pane. It takes focus when opened, closes on
// Escape or a tap outside it, and gives focus back to the control that opened it.
test("the floating run browser has a scrim and closes on Escape", () => {
  const realRO = (globalThis as { ResizeObserver?: unknown }).ResizeObserver;
  const callbacks: ((entries: unknown[]) => void)[] = [];
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = class {
    constructor(cb: (entries: unknown[]) => void) {
      callbacks.push(cb);
    }
    observe(): void {}
    disconnect(): void {}
  };
  try {
    document.body.innerHTML = '<section id="host"><div id="scroll"></div></section>';
    const scroll = must(document.getElementById("scroll"));
    const panelApi = must(
      mountCollapsiblePanel({
        scroll,
        title: "Recent runs",
        label: "Recent runs",
        bodyTitle: "Output",
        onRefresh: () => {},
        hideWhenEmpty: false,
      }),
    );
    const aside = must(document.querySelector<HTMLElement>(".console-log-runs"));
    const scrim = must(document.querySelector<HTMLElement>(".console-log-split__scrim"));
    const tell = (width: number): void =>
      callbacks.forEach((cb) => cb([{ contentRect: { width } }]));

    panelApi.applyDefault(true);
    tell(400);
    assert.equal(aside.hidden, true, "a narrow pane starts with the panel closed");

    panelApi.reopen.click();
    assert.equal(aside.hidden, false);
    assert.equal(scrim.hidden, false, "and a scrim sits behind it");
    assert.ok(aside.contains(document.activeElement), "focus moved into the panel");

    aside.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.equal(aside.hidden, true);
    assert.equal(scrim.hidden, true);
    assert.equal(document.activeElement, panelApi.reopen, "focus returns to the rail");

    tell(1200);
    panelApi.dispose();
  } finally {
    (globalThis as { ResizeObserver?: unknown }).ResizeObserver = realRO;
  }
});

test("the reference id lands in the body header whenever the slot arrives", () => {
  setRefIdentity("out1a2b3c", true);
  const slot = document.createElement("span");
  bindRefSlot(slot);
  assert.equal(slot.hidden, false);
  assert.match(text(slot), /Reference ID/);
  assert.equal(text(slot.querySelector(".console-log-body__meta-value")), "out1a2b3c");

  setRefIdentity("", false);
  assert.equal(slot.hidden, true, "an empty identity hides the slot");
  bindRefSlot(null);
});

// The cold screen. With no server and no demo there is nothing to load, so there is no toolbar
// offering buttons that could do nothing, and the empty state is the shared connect prompt.
test("with nothing loaded the toolbar is hidden and the empty state is the connect prompt", () => {
  document.body.innerHTML = readFileSync("src/apps/logs/scaffold.html", "utf8");
  location.hash = "";
  activate();
  try {
    const panelEl = must(document.getElementById("log-panel"));
    assert.equal(
      panelEl.hasAttribute("data-loaded"),
      false,
      "the toolbar's [data-loaded] gate is off",
    );
    assert.match(text(document.getElementById("log-empty-title")), /No server connected/);
    assert.equal(
      document.querySelectorAll("#log-empty-actions [data-empty-way] .pf-v6-c-button").length > 0,
      true,
      "with the same ways forward the other apps offer",
    );
    assert.equal(docTitle.get(), null);
  } finally {
    deactivate();
  }
});

test("a run opened from a link is titled by its command, not its id", async () => {
  document.body.innerHTML = readFileSync("src/apps/logs/scaffold.html", "utf8");
  location.hash = "#demo&inv=" + INV_CI;
  activate();
  try {
    await new Promise((r) => setTimeout(r, 60));
    assert.equal(docTitle.get(), "magus affected ci");
    const panelEl = must(document.getElementById("log-panel"));
    assert.equal(panelEl.hasAttribute("data-loaded"), true, "the toolbar appears with the log");
    assert.equal(must(document.getElementById("log-empty")).hidden, true);
    assert.equal(text(document.querySelector(".console-log-body__title")), "magus affected ci");
  } finally {
    deactivate();
  }
});
